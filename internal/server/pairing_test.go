package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/pairing"
	"github.com/odmrs/ember/internal/profile"
	"golang.org/x/crypto/ssh"
)

const origin = "http://" + self

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(h, "POST", path, self, origin, body, "Content-Type", "application/json")
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	return call(h, "GET", path, self, "", "")
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

type apiError struct{ Error, Message string }

func TestPairingOverTheAPI(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	flow, _ := newFlow(t)
	h := New(self, "test", flow)

	if rec := post(t, h, "/api/pair/scan", `{"address":"`+fake.Addr()+`"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}
	var state pairing.State
	for deadline := time.Now().Add(10 * time.Second); state.Phase != pairing.PhaseFound; {
		if time.Now().After(deadline) {
			t.Fatal("scan did not finish")
		}
		time.Sleep(10 * time.Millisecond)
		state = decodeBody[pairing.State](t, get(t, h, "/api/pair"))
	}
	if len(state.Candidates) != 1 || state.Candidates[0].Survey == nil {
		t.Fatalf("state = %+v", state)
	}
	c := state.Candidates[0]

	rec := post(t, h, "/api/pair/confirm", `{"address":"`+c.Addr+`","fingerprint":"SHA256:nope"}`)
	if e := decodeBody[apiError](t, rec); rec.Code != http.StatusNotFound || e.Error != "unknown_device" {
		t.Fatalf("wrong fingerprint: %d %+v", rec.Code, e)
	}
	rec = post(t, h, "/api/pair/confirm", `{"address":"`+c.Addr+`","fingerprint":"`+c.Fingerprint+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}

	status := decodeBody[struct {
		State  string
		Device deviceView
	}](t, get(t, h, "/api/status"))
	if status.State != "paired" || status.Device.Fingerprint != ssh.FingerprintSHA256(fake.HostKey()) || status.Device.LibraryRoot != profile.DefaultLibraryRoot {
		t.Fatalf("status = %+v", status)
	}

	if got := decodeBody[map[string]bool](t, post(t, h, "/api/pair/lock", `{}`)); got["locked"] {
		t.Fatal("locked while passwordless is on")
	}
	fake.SetPasswordless(false)
	if got := decodeBody[map[string]bool](t, post(t, h, "/api/pair/lock", `{}`)); !got["locked"] {
		t.Fatal("not locked after switching passwordless off")
	}
}

func TestPublicKeyOnly(t *testing.T) {
	flow, _ := newFlow(t)
	h := New(self, "test", flow)
	rec := post(t, h, "/api/pair/key", `{}`)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"publicKey":"ssh-ed25519 `) {
		t.Fatalf("key: %d %s", rec.Code, body)
	}
	if strings.Contains(body, "PRIVATE") {
		t.Fatal("private key served")
	}
	if rec := get(t, h, "/api/pair/key"); rec.Code == http.StatusOK {
		t.Fatal("GET must not create keys")
	}
}

func TestStatusNeverLeaksThePrivateKey(t *testing.T) {
	flow, store := newFlow(t)
	if _, err := store.Signer(); err != nil {
		t.Fatal(err)
	}
	p := profile.New("192.168.0.42", 2222)
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	body := get(t, New(self, "test", flow), "/api/status").Body.String()
	if !strings.Contains(body, `"state":"paired"`) || strings.Contains(body, "PRIVATE") || strings.Contains(body, "hostKey") {
		t.Fatalf("status = %s", body)
	}
}

func TestHostKeyMismatchIsReported(t *testing.T) {
	fake := fakedevice.New(t)
	impostor := fakedevice.New(t, fakedevice.WithHostKeySeed(9))
	flow, store := newFlow(t)
	host, port, _ := net.SplitHostPort(impostor.Addr())
	n, _ := strconv.Atoi(port)
	p := profile.New(host, n)
	p.HostKey = profile.AuthorizedKey(fake.HostKey())
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	rec := post(t, New(self, "test", flow), "/api/pair/lock", `{}`)
	if e := decodeBody[apiError](t, rec); rec.Code != http.StatusConflict || e.Error != "host_key_mismatch" {
		t.Fatalf("lock: %d %+v", rec.Code, e)
	}
}

func TestPairingRequestsNeedJSON(t *testing.T) {
	flow, _ := newFlow(t)
	h := New(self, "test", flow)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		rec := call(h, "POST", "/api/pair/scan", self, origin, `{}`, "Content-Type", ct)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("content type %q: status = %d", ct, rec.Code)
		}
	}
	if rec := post(t, h, "/api/pair/scan", `{"address":"; reboot"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad address: status = %d", rec.Code)
	}
	if rec := post(t, h, "/api/pair/scan", `{"nope":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: status = %d", rec.Code)
	}
}
