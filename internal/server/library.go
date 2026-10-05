package server

import (
	"errors"
	"net/http"
	"sync"

	"github.com/odmrs/ember/internal/connector"
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
func routeLibrary(mux *http.ServeMux, flow *pairing.Flow, conn *connector.Connector) {
	mux.HandleFunc("GET /api/library/folder", func(w http.ResponseWriter, r *http.Request) {
		lib, err := openLibrary(flow, conn)
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
		lib, err := openLibrary(flow, conn)
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
		lib, err := openLibrary(flow, conn)
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

	// Writes run one at a time, so two tabs cannot both pass the same
	// collision check before either renames.
	var mu sync.Mutex
	open := func() (*library.Library, error) { return openLibrary(flow, conn) }
	routeWrite(mux, "POST /api/library/mkdir", open, &mu, func(lib *library.Library, req mkdirRequest) (opResult, error) {
		p, err := lib.CreateFolder(req.Parent, req.Name)
		return opResult{Path: p}, err
	})
	routeWrite(mux, "POST /api/library/rename", open, &mu, func(lib *library.Library, req renameRequest) (opResult, error) {
		warns, err := lib.Rename(req.Path, req.Name, library.Options{ConfirmOpen: req.ConfirmOpen})
		return opResult{Warnings: warns}, err
	})
	routeWrite(mux, "POST /api/library/move", open, &mu, func(lib *library.Library, req moveRequest) (opResult, error) {
		if len(req.Paths) == 0 {
			return opResult{}, errNoPaths
		}
		warns, err := lib.Move(req.Paths, req.To, library.Options{ConfirmOpen: req.ConfirmOpen})
		return opResult{Warnings: warns}, err
	})
	routeWrite(mux, "POST /api/library/delete", open, &mu, func(lib *library.Library, req deleteRequest) (opResult, error) {
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

// openLibrary binds the paired Library root to the linked Device.
func openLibrary(flow *pairing.Flow, conn *connector.Connector) (*library.Library, error) {
	p, err := flow.Profile()
	if err != nil {
		return nil, err
	}
	d, err := conn.Device()
	if err != nil {
		return nil, err
	}
	return library.New(d, p.LibraryRoot), nil
}
