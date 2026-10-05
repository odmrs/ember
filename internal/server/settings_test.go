package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/pairing"
	"github.com/odmrs/ember/internal/profile"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type settingsRig struct {
	t     *testing.T
	h     http.Handler
	store *profile.Store
	conn  *connector.Connector
	clock *clock
}

// newSettingsRig serves a Profile paired with fake. With linked set it
// opens a Session and waits for the link.
func newSettingsRig(t *testing.T, fake *fakedevice.Device, linked bool) *settingsRig {
	t.Helper()
	store := pairedStore(t, fake)
	noLAN := func([]int) ([]string, error) { return nil, nil }
	conn := connector.New(connector.Config{Store: store, Targets: noLAN})
	t.Cleanup(conn.Close)
	if linked {
		_, end := conn.Session()
		t.Cleanup(end)
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := conn.Device(); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("never linked")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	c := &clock{now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	h := newHandler(self, "test", pairing.NewFlow(pairing.Config{Store: store, Targets: noLAN}), conn, c.Now)
	return &settingsRig{t: t, h: h, store: store, conn: conn, clock: c}
}

func (r *settingsRig) post(path, body string) (int, map[string]any) {
	r.t.Helper()
	rec := call(r.h, "POST", path, self, "http://"+self, body, "Content-Type", "application/json")
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		r.t.Fatalf("%s: %d %q", path, rec.Code, rec.Body)
	}
	return rec.Code, out
}

func (r *settingsRig) profile() profile.Profile {
	r.t.Helper()
	p, err := r.store.Load()
	if err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *settingsRig) requestToken() string {
	r.t.Helper()
	code, out := r.post("/api/settings/passwordless/request", `{}`)
	token, _ := out["token"].(string)
	if code != http.StatusOK || len(token) < 40 {
		r.t.Fatalf("request: %d %v", code, out)
	}
	return token
}

func (r *settingsRig) confirm(token string) (int, map[string]any) {
	r.t.Helper()
	body, _ := json.Marshal(map[string]string{"token": token})
	return r.post("/api/settings/passwordless/confirm", string(body))
}

func (r *settingsRig) mustBePasswordless(want bool) {
	r.t.Helper()
	if got := r.profile().Passwordless; got != want {
		r.t.Fatalf("passwordless = %v, want %v", got, want)
	}
}

func TestPasswordlessIsOffByDefault(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	r.mustBePasswordless(false)
	rec := call(r.h, "GET", "/api/status", self, "", "")
	var st struct{ Device deviceView }
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.Device.Passwordless {
		t.Fatalf("status: %s", rec.Body)
	}
}

// No single request turns passwordless mode on: not the settings form, not a
// confirmation without its request, not one with a made-up token.
func TestOneRequestCannotEnablePasswordless(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	for _, body := range []string{`{"passwordless":true}`, `{"keepAwake":true,"passwordless":true}`} {
		if code, out := r.post("/api/settings", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", body, code, out)
		}
	}
	for _, token := range []string{"", "x", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if code, out := r.confirm(token); code != http.StatusForbidden || out["error"] != "confirmation_required" {
			t.Errorf("token %q: %d %v", token, code, out)
		}
	}
	if code, _ := r.post("/api/settings/passwordless/confirm", `{}`); code != http.StatusForbidden {
		t.Errorf("no token: %d", code)
	}
	r.mustBePasswordless(false)

	r.requestToken()
	r.mustBePasswordless(false)
}

func TestPasswordlessNeedsRequestThenConfirm(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	token := r.requestToken()
	code, out := r.confirm(token)
	if code != http.StatusOK {
		t.Fatalf("confirm: %d %v", code, out)
	}
	if dev, _ := out["device"].(map[string]any); dev["passwordless"] != true {
		t.Fatalf("response = %v", out)
	}
	r.mustBePasswordless(true)

	// Turning it off is one request.
	if code, out := r.post("/api/settings/passwordless/off", `{}`); code != http.StatusOK {
		t.Fatalf("off: %d %v", code, out)
	}
	r.mustBePasswordless(false)

	// The token was spent.
	if code, _ := r.confirm(token); code != http.StatusForbidden {
		t.Fatalf("token reused: %d", code)
	}
	r.mustBePasswordless(false)
}

func TestPasswordlessTokenExpires(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	token := r.requestToken()
	r.clock.advance(confirmTTL + time.Second)
	if code, _ := r.confirm(token); code != http.StatusForbidden {
		t.Fatalf("expired token accepted: %d", code)
	}
	r.mustBePasswordless(false)

	token = r.requestToken()
	r.clock.advance(confirmTTL - time.Second)
	if code, out := r.confirm(token); code != http.StatusOK {
		t.Fatalf("token within its time refused: %d %v", code, out)
	}
}

// A wrong guess spends the pending token, so guessing gets one try per
// request the user makes.
func TestPasswordlessWrongTokenSpendsTheRealOne(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	token := r.requestToken()
	wrong := token[:len(token)-1] + "A"
	if wrong == token {
		wrong = token[:len(token)-1] + "B"
	}
	if code, _ := r.confirm(wrong); code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", code)
	}
	if code, _ := r.confirm(token); code != http.StatusForbidden {
		t.Fatalf("real token still valid after a wrong guess: %d", code)
	}
	// Nothing pending is not the same as an empty token matching.
	if code, _ := r.confirm(""); code != http.StatusForbidden {
		t.Fatalf("empty token accepted after the real one was spent: %d", code)
	}
	r.mustBePasswordless(false)
}

func TestPasswordlessNewRequestReplacesTheOld(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	first := r.requestToken()
	second := r.requestToken()
	if first == second {
		t.Fatal("same token twice")
	}
	if code, _ := r.confirm(first); code != http.StatusForbidden {
		t.Fatalf("replaced token accepted: %d", code)
	}
	r.mustBePasswordless(false)
}

func TestPasswordlessNeedsAPairedDevice(t *testing.T) {
	flow, _ := newFlow(t)
	h := New(self, "test", flow, idleConnector(t))
	rec := call(h, "POST", "/api/settings/passwordless/request", self, "http://"+self, `{}`, "Content-Type", "application/json")
	if rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "not_paired" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestSettingsArePersisted(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	code, out := r.post("/api/settings", `{"port":22,"user":"admin","keepAwake":false}`)
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
	p := r.profile()
	if p.Port != 22 || p.User != "admin" || p.KeepAwake || p.LibraryRoot != profile.DefaultLibraryRoot || p.HostKey == "" {
		t.Fatalf("profile = %+v", p)
	}
	if dev, _ := out["device"].(map[string]any); dev["port"] != float64(22) || dev["keepAwake"] != false {
		t.Fatalf("response = %v", out)
	}

	// Fields left out keep their value.
	if code, out := r.post("/api/settings", `{"keepAwake":true}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
	if p := r.profile(); p.Port != 22 || p.User != "admin" || !p.KeepAwake {
		t.Fatalf("profile = %+v", p)
	}
}

func TestSettingsRefuseBadValues(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	before := r.profile()
	cases := map[string]string{
		`{"port":0}`:                "bad_port",
		`{"port":65536}`:            "bad_port",
		`{"user":""}`:               "bad_user",
		`{"user":"root; reboot"}`:   "bad_user",
		`{"user":"a b"}`:            "bad_user",
		`{"hostKey":"ssh-ed25519"}`: "bad_request",
		`{"address":"10.0.0.1"}`:    "bad_request",
	}
	for body, want := range cases {
		if code, out := r.post("/api/settings", body); code != http.StatusBadRequest || out["error"] != want {
			t.Errorf("%s: %d %v, want %s", body, code, out, want)
		}
	}
	if after := r.profile(); after != before {
		t.Fatalf("profile changed: %+v", after)
	}
}

func TestLibraryRootIsValidatedOnTheDevice(t *testing.T) {
	fake := fakedevice.New(t)
	fake.WriteFile(t, "/mnt/us/books/Dune.epub", "dune")
	fake.WriteFile(t, "/mnt/us/notes.txt", "x")
	r := newSettingsRig(t, fake, true)
	cases := map[string]struct {
		status int
		code   string
	}{
		`{"libraryRoot":"/mnt/us/missing"}`:   {http.StatusNotFound, "not_found"},
		`{"libraryRoot":"/mnt/us/notes.txt"}`: {http.StatusConflict, "not_folder"},
		`{"libraryRoot":"/mnt/us"}`:           {http.StatusBadRequest, "root_outside"},
		`{"libraryRoot":"/etc"}`:              {http.StatusBadRequest, "root_outside"},
		`{"libraryRoot":"/mnt/us/../../etc"}`: {http.StatusBadRequest, "root_outside"},
	}
	for body, want := range cases {
		if code, out := r.post("/api/settings", body); code != want.status || out["error"] != want.code {
			t.Errorf("%s: %d %v, want %d %s", body, code, out, want.status, want.code)
		}
	}
	if p := r.profile(); p.LibraryRoot != profile.DefaultLibraryRoot {
		t.Fatalf("root changed to %q", p.LibraryRoot)
	}

	if code, out := r.post("/api/settings", `{"libraryRoot":"/mnt/us/books/"}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
	if p := r.profile(); p.LibraryRoot != "/mnt/us/books" {
		t.Fatalf("root = %q", p.LibraryRoot)
	}
	rec := call(r.h, "GET", "/api/library/folder?path=", self, "", "")
	var f struct{ Books int }
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil || f.Books != 1 {
		t.Fatalf("library after the change: %s", rec.Body)
	}
}

func TestLibraryRootCannotChangeWhileUnlinked(t *testing.T) {
	r := newSettingsRig(t, fakedevice.New(t), false)
	if code, out := r.post("/api/settings", `{"libraryRoot":"/mnt/us/documents/sub"}`); code != http.StatusServiceUnavailable || out["error"] != "not_linked" {
		t.Fatalf("%d %v", code, out)
	}
	// Sending the root unchanged is not a change.
	if code, out := r.post("/api/settings", `{"libraryRoot":"/mnt/us/documents","keepAwake":false}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
}

func TestKeepAwakeSettingReachesTheLink(t *testing.T) {
	fake := fakedevice.New(t)
	r := newSettingsRig(t, fake, true)
	deadline := time.Now().Add(10 * time.Second)
	for !fake.PreventScreenSaver() {
		if time.Now().After(deadline) {
			t.Fatal("keep-awake never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if code, out := r.post("/api/settings", `{"keepAwake":false}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
	for fake.PreventScreenSaver() || fake.Running() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("keep-awake still running after it was switched off")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestForgetReturnsToPairing(t *testing.T) {
	fake := fakedevice.New(t)
	r := newSettingsRig(t, fake, true)
	token := r.requestToken()
	if code, _ := r.post("/api/settings/forget", `{}`); code != http.StatusBadRequest {
		t.Fatalf("forget without confirm: %d", code)
	}
	if code, out := r.post("/api/settings/forget", `{"confirm":true}`); code != http.StatusOK {
		t.Fatalf("forget: %d %v", code, out)
	}
	if _, err := r.store.Load(); err == nil {
		t.Fatal("still paired")
	}
	rec := call(r.h, "GET", "/api/status", self, "", "")
	var st struct{ State string }
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || st.State != "unpaired" {
		t.Fatalf("status: %s", rec.Body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := r.conn.Device(); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still linked to the forgotten Device")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if code, _ := r.confirm(token); code != http.StatusForbidden && code != http.StatusConflict {
		t.Fatalf("token from before forget: %d", code)
	}
}
