package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/library"
	"github.com/odmrs/ember/internal/pairing"
	"github.com/odmrs/ember/internal/profile"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const deviceTimeout = 30 * time.Second

// deviceView is the paired Device as the dashboard sees it. It is built from
// the Profile field by field so nothing private can slip into a response.
type deviceView struct {
	Address      string         `json:"address"`
	Port         int            `json:"port"`
	User         string         `json:"user"`
	LibraryRoot  string         `json:"libraryRoot"`
	Fingerprint  string         `json:"fingerprint"`
	Survey       profile.Survey `json:"survey"`
	KeepAwake    bool           `json:"keepAwake"`
	Passwordless bool           `json:"passwordless"`
}

func viewOf(p profile.Profile) deviceView {
	v := deviceView{
		Address: p.Address, Port: p.Port, User: p.User, LibraryRoot: p.LibraryRoot,
		Survey: p.Survey, KeepAwake: p.KeepAwake, Passwordless: p.Passwordless,
	}
	if key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p.HostKey)); err == nil {
		v.Fingerprint = ssh.FingerprintSHA256(key)
	}
	return v
}

func status(w http.ResponseWriter, version string, flow *pairing.Flow) {
	p, err := flow.Profile()
	switch {
	case errors.Is(err, profile.ErrNotPaired):
		writeJSON(w, map[string]any{"version": version, "state": "unpaired"})
	case err != nil:
		writeError(w, err)
	default:
		writeJSON(w, map[string]any{"version": version, "state": "paired", "device": viewOf(p)})
	}
}

func routePairing(mux *http.ServeMux, flow *pairing.Flow) {
	mux.HandleFunc("GET /api/pair", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, flow.State())
	})

	mux.HandleFunc("POST /api/pair/scan", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		}
		if !decode(w, r, &req) {
			return
		}
		if err := flow.Scan(req.Address, req.Port); err != nil {
			writeError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusAccepted, flow.State())
	})

	mux.HandleFunc("POST /api/pair/confirm", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Address     string `json:"address"`
			Fingerprint string `json:"fingerprint"`
		}
		if !decode(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), deviceTimeout)
		defer cancel()
		p, err := flow.Confirm(ctx, req.Address, req.Fingerprint)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"device": viewOf(p)})
	})

	mux.HandleFunc("POST /api/pair/lock", func(w http.ResponseWriter, r *http.Request) {
		if !decode(w, r, &struct{}{}) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), deviceTimeout)
		defer cancel()
		locked, err := flow.CheckLock(ctx)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"locked": locked})
	})

	// Creating the key pair is a write, hence POST even though it reads like
	// a getter. Only the public half is ever returned.
	mux.HandleFunc("POST /api/pair/key", func(w http.ResponseWriter, r *http.Request) {
		if !decode(w, r, &struct{}{}) {
			return
		}
		pub, err := flow.PublicKey()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]string{"publicKey": pub})
	})
}

// decode reads a small JSON body into v. Requiring the JSON content type
// keeps plain HTML forms, which cannot set it, from reaching the handler.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeMax(w, r, v, 4<<10)
}

func decodeMax(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeErrorCode(w, http.StatusUnsupportedMediaType, "bad_request", "expected application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "bad_request", err.Error())
		return false
	}
	return true
}

// writeError maps an error to a stable code the dashboard translates, plus
// the raw message for the log line.
func writeError(w http.ResponseWriter, err error) {
	var mismatch *device.HostKeyMismatchError
	var netErr net.Error
	var opErr *net.OpError
	var nameErr *library.NameError
	var noSpace *library.NoSpaceError
	switch {
	case errors.As(err, &mismatch):
		writeErrorCode(w, http.StatusConflict, "host_key_mismatch", err.Error())
	case errors.Is(err, pairing.ErrBadAddress):
		writeErrorCode(w, http.StatusBadRequest, "bad_address", err.Error())
	case errors.Is(err, pairing.ErrAlreadyPaired):
		writeErrorCode(w, http.StatusConflict, "already_paired", err.Error())
	case errors.Is(err, pairing.ErrUnknownDevice):
		writeErrorCode(w, http.StatusNotFound, "unknown_device", err.Error())
	case errors.Is(err, pairing.ErrNotConfirmed):
		writeErrorCode(w, http.StatusConflict, "not_confirmed", err.Error())
	case errors.Is(err, pairing.ErrKeyNotAccepted):
		writeErrorCode(w, http.StatusUnprocessableEntity, "key_not_accepted", err.Error())
	case errors.Is(err, pairing.ErrVerifyFailed):
		writeErrorCode(w, http.StatusBadGateway, "verify_failed", err.Error())
	case errors.Is(err, device.ErrAuthRejected):
		writeErrorCode(w, http.StatusBadGateway, "auth_rejected", err.Error())
	case errors.Is(err, profile.ErrNotPaired):
		writeErrorCode(w, http.StatusConflict, "not_paired", err.Error())
	case errors.Is(err, connector.ErrNotLinked):
		writeErrorCode(w, http.StatusServiceUnavailable, "not_linked", err.Error())
	case errors.Is(err, errNoPaths):
		writeErrorCode(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.As(err, &noSpace):
		writeJSONStatus(w, http.StatusInsufficientStorage, map[string]any{"error": "no_space", "need": noSpace.Need, "free": noSpace.Free, "message": err.Error()})
	case errors.Is(err, library.ErrIncomplete):
		writeErrorCode(w, http.StatusBadRequest, "incomplete", err.Error())
	case errors.Is(err, library.ErrNotFile):
		writeErrorCode(w, http.StatusConflict, "not_file", err.Error())
	case errors.As(err, &nameErr):
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid_name", "reason": nameErr.Reason, "message": err.Error()})
	case errors.Is(err, library.ErrPartial):
		writeErrorCode(w, http.StatusInternalServerError, "partial_failure", err.Error())
	case errors.Is(err, library.ErrExists):
		writeErrorCode(w, http.StatusConflict, "collision", err.Error())
	case errors.Is(err, library.ErrOpenNow):
		writeErrorCode(w, http.StatusConflict, "open_now_needs_confirm", err.Error())
	case errors.Is(err, library.ErrMetadataMode):
		writeErrorCode(w, http.StatusConflict, "metadata_mode_unsupported", err.Error())
	case errors.Is(err, library.ErrIntoItself):
		writeErrorCode(w, http.StatusConflict, "into_itself", err.Error())
	case errors.Is(err, library.ErrRoot):
		writeErrorCode(w, http.StatusBadRequest, "library_root", err.Error())
	case errors.Is(err, library.ErrExtension):
		writeErrorCode(w, http.StatusBadRequest, "extension_change", err.Error())
	case errors.Is(err, library.ErrOutside):
		writeErrorCode(w, http.StatusBadRequest, "outside_library", err.Error())
	case errors.Is(err, library.ErrNotFound):
		writeErrorCode(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, library.ErrNotFolder):
		writeErrorCode(w, http.StatusConflict, "not_folder", err.Error())
	case errors.As(err, &opErr), errors.As(err, &netErr), errors.Is(err, context.DeadlineExceeded), errors.Is(err, io.EOF),
		errors.Is(err, sftp.ErrSSHFxConnectionLost), errors.Is(err, sftp.ErrSSHFxNoConnection):
		writeErrorCode(w, http.StatusBadGateway, "unreachable", err.Error())
	default:
		writeErrorCode(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func writeErrorCode(w http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(w, status, map[string]string{"error": code, "message": message})
}
