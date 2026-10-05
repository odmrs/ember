package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/fakedevice"
)

func (r *settingsRig) disconnected() {
	r.t.Helper()
	if s := r.conn.State(); s.Phase != connector.PhaseWaiting || s.Reason != connector.ReasonDisconnected {
		r.t.Fatalf("state = %+v, want waiting after a disconnect", s)
	}
}

func TestDisconnectOverTheAPI(t *testing.T) {
	fake := fakedevice.New(t)
	r := newSettingsRig(t, fake, true)
	code, out := r.post("/api/connect/disconnect", `{"stopServer":false}`)
	if code != http.StatusOK || out["stopped"] != false {
		t.Fatalf("disconnect: %d %v", code, out)
	}
	r.disconnected()
	if !fake.Listening() {
		t.Fatal("the SSH server was stopped although not asked to")
	}

	if rec := post(t, r.h, "/api/connect/retry", "{}"); rec.Code != http.StatusNoContent {
		t.Fatalf("retry: %d", rec.Code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for r.conn.State().Phase != connector.PhaseLinked {
		if time.Now().After(deadline) {
			t.Fatalf("not linked after retry: %+v", r.conn.State())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDisconnectAndStopOverTheAPI(t *testing.T) {
	fake := fakedevice.New(t)
	r := newSettingsRig(t, fake, true)
	code, out := r.post("/api/connect/disconnect", `{"stopServer":true}`)
	if code != http.StatusOK || out["stopped"] != true {
		t.Fatalf("disconnect: %d %v", code, out)
	}
	r.disconnected()
	if fake.Listening() {
		t.Fatal("the SSH server still listens")
	}
}

func TestDisconnectNeedsALinkOverTheAPI(t *testing.T) {
	fake := fakedevice.New(t)
	r := newSettingsRig(t, fake, false)
	code, out := r.post("/api/connect/disconnect", `{"stopServer":true}`)
	if code != http.StatusServiceUnavailable || out["error"] != "not_linked" {
		t.Fatalf("disconnect while unlinked: %d %v", code, out)
	}
	if got := fake.Execs(); len(got) != 0 {
		t.Fatalf("ran %q", got)
	}
}

func TestDisconnectNeedsSameOriginJSONPost(t *testing.T) {
	fake := fakedevice.New(t)
	r := newSettingsRig(t, fake, true)
	body := `{"stopServer":true}`
	if rec := call(r.h, "GET", "/api/connect/disconnect", self, "", ""); rec.Code < 400 {
		t.Errorf("GET: %d", rec.Code)
	}
	if rec := call(r.h, "POST", "/api/connect/disconnect", self, "http://evil.example", body, "Content-Type", "application/json"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin: %d", rec.Code)
	}
	if rec := call(r.h, "POST", "/api/connect/disconnect", self, "", body, "Content-Type", "application/json"); rec.Code != http.StatusForbidden {
		t.Errorf("no origin: %d", rec.Code)
	}
	if rec := call(r.h, "POST", "/api/connect/disconnect", self, origin, body, "Content-Type", "application/json", "Sec-Fetch-Site", "cross-site"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site: %d", rec.Code)
	}
	if rec := call(r.h, "POST", "/api/connect/disconnect", self, origin, body, "Content-Type", "text/plain"); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form post: %d", rec.Code)
	}
	if rec := call(r.h, "POST", "/api/connect/disconnect", self, origin, `{"stopServer":true,"x":1}`, "Content-Type", "application/json"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}
	if s := r.conn.State(); s.Phase != connector.PhaseLinked {
		t.Fatalf("a refused request changed the link: %+v", s)
	}
	if got := fake.Execs(); len(got) != 1 {
		t.Fatalf("Execs = %q, want only keep-awake", got)
	}
}
