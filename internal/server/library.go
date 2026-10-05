package server

import (
	"net/http"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/library"
	"github.com/odmrs/ember/internal/pairing"
)

// routeLibrary serves the Library read-only. Paths in the query are Library
// paths; the library package decides what they may reach.
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
