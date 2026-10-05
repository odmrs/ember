package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const self = "127.0.0.1:8737"

func do(t *testing.T, method, path, host, origin string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	New(self, "test").ServeHTTP(rec, req)
	return rec
}

func TestServesDashboard(t *testing.T) {
	rec := do(t, "GET", "/", self, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<title>ember</title>") {
		t.Fatal("index.html not served")
	}
}

func TestStatusReportsUnpaired(t *testing.T) {
	rec := do(t, "GET", "/api/status", self, "")
	var body struct{ Version, State string }
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State != "unpaired" || body.Version != "test" {
		t.Fatalf("body = %+v", body)
	}
}

// A page on another site can rebind its DNS name to 127.0.0.1; the Host header
// is what gives it away.
func TestRejectsForeignHost(t *testing.T) {
	for _, host := range []string{"evil.example:8737", "127.0.0.1:9999", "192.168.0.10:8737", ""} {
		if rec := do(t, "GET", "/api/status", host, ""); rec.Code != http.StatusForbidden {
			t.Errorf("host %q: status = %d, want 403", host, rec.Code)
		}
	}
}

func TestAcceptsLocalhostAlias(t *testing.T) {
	if rec := do(t, "GET", "/api/status", "localhost:8737", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestStateChangingRequestsNeedSameOrigin(t *testing.T) {
	cases := map[string]bool{
		"":                       false,
		"https://evil.example":   false,
		"null":                   false,
		"127.0.0.1:8737":         false,
		"https://127.0.0.1:8737": false,
		"http://127.0.0.1:8737":  true,
		"http://localhost:8737":  true,
	}
	for origin, allowed := range cases {
		rec := do(t, "POST", "/api/nothing-here", self, origin)
		if got := rec.Code != http.StatusForbidden; got != allowed {
			t.Errorf("origin %q: status = %d, allowed = %v", origin, rec.Code, allowed)
		}
	}
}

// No-cors GETs from another site cannot read the answer, but they still reach
// the handler; the browser's Sec-Fetch-Site header is what exposes them.
func TestRejectsCrossSiteFetchMetadata(t *testing.T) {
	cases := map[string]bool{
		"":            true,
		"none":        true,
		"same-origin": true,
		"same-site":   false,
		"cross-site":  false,
	}
	for site, allowed := range cases {
		rec := do(t, "GET", "/api/status", self, "", "Sec-Fetch-Site", site)
		if got := rec.Code == http.StatusOK; got != allowed {
			t.Errorf("Sec-Fetch-Site %q: status = %d, allowed = %v", site, rec.Code, allowed)
		}
	}
}

func TestRefusesNonLoopbackAddress(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8737", "192.168.0.10:8737", "localhost:8737", "garbage"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%q) did not panic", addr)
				}
			}()
			New(addr, "test")
		}()
	}
}

func TestSecurityHeaders(t *testing.T) {
	if rec := do(t, "GET", "/", "evil.example:8737", ""); rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("rejected responses must carry the security headers too")
	}
	rec := do(t, "GET", "/", self, "")
	csp := rec.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP %q missing %q", csp, directive)
		}
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("missing Referrer-Policy")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}
