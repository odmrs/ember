package server

import (
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/odmrs/ember/internal/library"
)

// routeTransfer serves uploads and downloads.
//
// An upload is one POST per file whose body is the file itself, streamed to
// the Device as it arrives; the target and options travel in the query. Its
// size is the Content-Length, which the dashboard always sends. No timeout
// applies to the body, so a large file over slow Wi-Fi is never cut off.
//
// A download is a GET: it changes nothing, and the guard still refuses it
// from any other site.
func routeTransfer(mux *http.ServeMux, a *access) {
	mux.HandleFunc("GET /api/library/upload/check", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		size, err := strconv.ParseInt(q.Get("size"), 10, 64)
		if err != nil || size < 0 {
			writeErrorCode(w, http.StatusBadRequest, "bad_request", "size must be a byte count")
			return
		}
		lib, err := a.open()
		if err != nil {
			writeError(w, err)
			return
		}
		arrival, err := lib.CheckUpload(q.Get("folder"), q.Get("name"), size)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, arrival)
	})

	mux.HandleFunc("POST /api/library/upload", func(w http.ResponseWriter, r *http.Request) {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/octet-stream" {
			writeErrorCode(w, http.StatusUnsupportedMediaType, "bad_request", "expected application/octet-stream")
			return
		}
		if r.ContentLength < 0 {
			writeErrorCode(w, http.StatusLengthRequired, "bad_request", "the upload needs a Content-Length")
			return
		}
		q := r.URL.Query()
		opt := library.UploadOptions{Replace: q.Get("replace") == "1", ConfirmOpen: q.Get("confirmOpen") == "1"}
		lib, err := a.open()
		if err != nil {
			writeError(w, err)
			return
		}
		p, warns, err := a.upload(lib, q.Get("folder"), q.Get("name"), r.ContentLength, r.Body, opt)
		if err != nil {
			writeError(w, err)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		res := opResult{Path: p, Warnings: warns}
		if res.Warnings == nil {
			res.Warnings = []library.Warning{}
		}
		if f, err := lib.List(q.Get("view")); err == nil {
			res.Folder = &f
		}
		writeJSON(w, res)
	})

	mux.HandleFunc("GET /api/library/download", func(w http.ResponseWriter, r *http.Request) {
		lib, err := a.open()
		if err != nil {
			writeError(w, err)
			return
		}
		dl, err := lib.Download(r.URL.Query().Get("path"))
		if err != nil {
			writeError(w, err)
			return
		}
		defer dl.Body.Close()
		h := w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", attachment(dl.Name))
		h.Set("Content-Length", strconv.FormatInt(dl.Size, 10))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, dl.Body)
		}
	})
}

// upload runs the checks and the final rename under the write lock and
// streams the data without it, so a large file over slow Wi-Fi does not hold
// up every other write. The rename checks the name again.
func (a *access) upload(lib *library.Library, folder, name string, size int64, body io.Reader, opt library.UploadOptions) (string, []library.Warning, error) {
	a.mu.Lock()
	u, err := lib.BeginUpload(folder, name, size, a.reserved(), opt)
	if err == nil {
		a.uploads[u.Part] = uint64(size) + library.UploadMargin
	}
	a.mu.Unlock()
	if err != nil {
		return "", nil, err
	}
	defer func() {
		a.mu.Lock()
		delete(a.uploads, u.Part)
		a.mu.Unlock()
	}()
	if err := u.Write(body); err != nil {
		return "", nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return u.Finish()
}

// attachment names the saved file. Non-ASCII names go in the RFC 2231 form
// browsers read; a name it cannot encode leaves the browser to pick one.
func attachment(name string) string {
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
		return v
	}
	return "attachment"
}
