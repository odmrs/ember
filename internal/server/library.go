package server

import (
	"errors"
	"log"
	"net/http"
	"sync"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/library"
	"github.com/odmrs/ember/internal/pairing"
)

// maxOpBody bounds a write request; a move or delete of a whole folder's
// worth of selected paths fits comfortably.
const maxOpBody = 256 << 10

// opResult is the answer to a write: the folder the dashboard shows, read
// again after the change, and anything the change did differently.
type opResult struct {
	// Folder is null when the folder in view could not be read afterwards,
	// for instance because the change removed it.
	Folder   *library.Folder   `json:"folder"`
	Path     string            `json:"path,omitempty"`
	Warnings []library.Warning `json:"warnings"`
}

// routeLibrary serves the Library. Paths are Library paths; the library
// package decides what they may reach. Reads are GETs, every change is a
// POST, so the same-origin guard covers all of them.
func routeLibrary(mux *http.ServeMux, a *access) {
	mux.HandleFunc("GET /api/library/folder", func(w http.ResponseWriter, r *http.Request) {
		lib, err := a.open()
		if err != nil {
			writeError(w, err)
			return
		}
		f, err := lib.List(r.URL.Query().Get("path"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, f)
	})

	mux.HandleFunc("GET /api/library/stats", func(w http.ResponseWriter, r *http.Request) {
		lib, err := a.open()
		if err != nil {
			writeError(w, err)
			return
		}
		st, err := lib.Stats()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, st)
	})

	mux.HandleFunc("GET /api/library/preview", func(w http.ResponseWriter, r *http.Request) {
		paths := r.URL.Query()["path"]
		if len(paths) == 0 {
			writeErrorCode(w, http.StatusBadRequest, "bad_request", "no path")
			return
		}
		lib, err := a.open()
		if err != nil {
			writeError(w, err)
			return
		}
		p, err := lib.Preview(paths)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, p)
	})

	mux.HandleFunc("GET /api/library/clean", func(w http.ResponseWriter, r *http.Request) {
		lib, err := a.open()
		if err != nil {
			writeError(w, err)
			return
		}
		ps, err := lib.ProposeNames(r.URL.Query().Get("path"))
		if err != nil {
			writeError(w, err)
			return
		}
		if ps == nil {
			ps = []library.Proposal{}
		}
		writeJSON(w, map[string]any{"proposals": ps})
	})

	routeTransfer(mux, a)

	open, mu := a.open, &a.mu
	routeWrite(mux, "POST /api/library/mkdir", open, mu, func(lib *library.Library, req mkdirRequest) (opResult, error) {
		p, err := lib.CreateFolder(req.Parent, req.Name)
		return opResult{Path: p}, err
	})
	routeWrite(mux, "POST /api/library/rename", open, mu, func(lib *library.Library, req renameRequest) (opResult, error) {
		warns, err := lib.Rename(req.Path, req.Name, library.Options{ConfirmOpen: req.ConfirmOpen})
		return opResult{Warnings: warns}, err
	})
	routeWrite(mux, "POST /api/library/rename-all", open, mu, func(lib *library.Library, req renameAllRequest) (opResult, error) {
		if len(req.Changes) == 0 {
			return opResult{}, errNoPaths
		}
		warns, err := lib.RenameAll(req.Changes, library.Options{ConfirmOpen: req.ConfirmOpen})
		return opResult{Warnings: warns}, err
	})
	routeWrite(mux, "POST /api/library/move", open, mu, func(lib *library.Library, req moveRequest) (opResult, error) {
		if len(req.Paths) == 0 {
			return opResult{}, errNoPaths
		}
		warns, err := lib.Move(req.Paths, req.To, library.Options{ConfirmOpen: req.ConfirmOpen})
		return opResult{Warnings: warns}, err
	})
	routeWrite(mux, "POST /api/library/delete", open, mu, func(lib *library.Library, req deleteRequest) (opResult, error) {
		if len(req.Paths) == 0 {
			return opResult{}, errNoPaths
		}
		warns, err := lib.Delete(req.Paths, library.Options{ConfirmOpen: req.ConfirmOpen})
		return opResult{Warnings: warns}, err
	})
}

var errNoPaths = errors.New("no paths given")

// inView is the folder the dashboard shows, listed again in the answer.
type inView struct {
	View string `json:"view"`
}

func (v inView) view() string { return v.View }

type mkdirRequest struct {
	inView
	Parent string `json:"parent"`
	Name   string `json:"name"`
}

type renameRequest struct {
	inView
	Path        string `json:"path"`
	Name        string `json:"name"`
	ConfirmOpen bool   `json:"confirmOpen"`
}

type renameAllRequest struct {
	inView
	Changes     []library.NameChange `json:"changes"`
	ConfirmOpen bool                 `json:"confirmOpen"`
}

type moveRequest struct {
	inView
	Paths       []string `json:"paths"`
	To          string   `json:"to"`
	ConfirmOpen bool     `json:"confirmOpen"`
}

type deleteRequest struct {
	inView
	Paths       []string `json:"paths"`
	ConfirmOpen bool     `json:"confirmOpen"`
}

// routeWrite serves one write. The change and the listing after it run
// under mu.
func routeWrite[T interface{ view() string }](mux *http.ServeMux, pattern string, open func() (*library.Library, error), mu *sync.Mutex, apply func(*library.Library, T) (opResult, error)) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		var req T
		if !decodeMax(w, r, &req, maxOpBody) {
			return
		}
		lib, err := open()
		if err != nil {
			writeError(w, err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		res, err := apply(lib, req)
		var (
			moveErr   *library.MoveError
			renameErr *library.RenameError
			ans       map[string]any
		)
		switch {
		case errors.As(err, &moveErr):
			ans = map[string]any{"error": "move_incomplete", "message": err.Error(), "moved": moveErr.Moved, "failed": moveErr.Failed, "folder": nil}
		case errors.As(err, &renameErr):
			ans = map[string]any{"error": "rename_incomplete", "message": err.Error(), "renamed": renameErr.Renamed, "failed": renameErr.Failed, "folder": nil}
		}
		if ans != nil {
			// Part of the batch went: the dashboard needs the folder as it is
			// now as much as the error.
			if f, lerr := lib.List(req.view()); lerr == nil {
				ans["folder"] = f
			}
			writeJSONStatus(w, http.StatusInternalServerError, ans)
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		if res.Warnings == nil {
			res.Warnings = []library.Warning{}
		}
		if f, err := lib.List(req.view()); err == nil {
			res.Folder = &f
		}
		writeJSON(w, res)
	})
}

// access opens the Library on the linked Device and holds the lock writes
// take, so they run one at a time and two tabs cannot both pass the same
// collision check before either renames. An upload holds it only for its
// checks and its final rename, never while its data streams in.
type access struct {
	flow *pairing.Flow
	conn *connector.Connector
	mu   sync.Mutex

	// uploads maps the part of every upload in progress to the bytes it
	// will take, so the clean-up leaves it alone and the free-space check
	// of the next one counts it. Guarded by mu.
	uploads map[string]uint64
}

func newAccess(flow *pairing.Flow, conn *connector.Connector) *access {
	a := &access{flow: flow, conn: conn, uploads: map[string]uint64{}}
	conn.OnLink(a.linked)
	return a
}

// open binds the paired Library root to the linked Device.
func (a *access) open() (*library.Library, error) {
	p, err := a.flow.Profile()
	if err != nil {
		return nil, err
	}
	d, err := a.conn.Device()
	if err != nil {
		return nil, err
	}
	return library.New(d, p.LibraryRoot), nil
}

// linked runs when the Connector makes a new link: the parts interrupted
// uploads left on the Device are removed in the background.
func (a *access) linked(d *device.Device) {
	p, err := a.flow.Profile()
	if err != nil {
		return
	}
	go a.cleanParts(library.New(d, p.LibraryRoot))
}

// cleanParts walks the whole Library, so it runs in the background, under
// the write lock. The parts of uploads in progress are kept.
func (a *access) cleanParts(lib *library.Library) {
	a.mu.Lock()
	defer a.mu.Unlock()
	removed, err := lib.CleanParts(func(part string) bool {
		_, ok := a.uploads[part]
		return ok
	})
	for _, p := range removed {
		log.Printf("library: removed %s, left by an interrupted upload", p)
	}
	if err != nil {
		log.Printf("library: cleaning up interrupted uploads: %v", err)
	}
}

// reserved is the space uploads in progress still need; a.mu must be held.
func (a *access) reserved() uint64 {
	var n uint64
	for _, b := range a.uploads {
		n += b
	}
	return n
}
