// Package server is ember's local HTTP server: the dashboard and its JSON API.
package server

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/pairing"
	"github.com/odmrs/ember/internal/web"
)

const csp = "default-src 'self'; connect-src 'self'; img-src 'self' data:; " +
	"style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// New returns the handler for a server listening on hostport, which must be a
// loopback address such as "127.0.0.1:8737". It panics otherwise: serving the
// API on any other interface would hand the Device to the whole network.
func New(hostport, version string, flow *pairing.Flow, conn *connector.Connector) http.Handler {
	host, _, err := net.SplitHostPort(hostport)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		panic("server: not a loopback address: " + hostport)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web.Static))
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		status(w, version, flow)
	})
	mux.HandleFunc("GET /api/events", events(conn))
	routePairing(mux, flow)
	return guard(hostport, mux)
}

// guard keeps the API reachable only from ember's own page. The server binds
// to loopback, but a website open in the same browser can still aim requests
// at it: by rebinding its own DNS name to 127.0.0.1 (caught by the Host check),
// by posting cross-origin (caught by the Origin check), or by firing no-cors
// GETs (caught by Sec-Fetch-Site, which browsers set and pages cannot forge).
func guard(hostport string, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(hostport)
	hosts := map[string]bool{hostport: true, net.JoinHostPort("localhost", port): true}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")

		switch {
		case !hosts[r.Host]:
			http.Error(w, "forbidden host", http.StatusForbidden)
		case crossSite(r):
			http.Error(w, "forbidden site", http.StatusForbidden)
		case r.Method != http.MethodGet && r.Method != http.MethodHead && !hosts[originHost(r)]:
			http.Error(w, "forbidden origin", http.StatusForbidden)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// crossSite reports whether the browser says the request came from another
// site. "none" is a user typing the URL or opening a bookmark. Clients that
// are not browsers send no header at all and are left to the other checks.
func crossSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return false
	}
	return true
}

func originHost(r *http.Request) string {
	host, ok := strings.CutPrefix(r.Header.Get("Origin"), "http://")
	if !ok {
		return ""
	}
	return host
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
