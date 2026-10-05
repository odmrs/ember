package profile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadWithoutProfile(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ember"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("err = %v, want ErrNotPaired", err)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ember")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := New("192.168.0.42", DefaultPort)
	p.HostKey = "ssh-ed25519 AAAA"
	p.Survey.MetadataMode = "doc"
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("got %+v, want %+v", got, p)
	}
	if !got.KeepAwake || got.Passwordless || got.LibraryRoot != "/mnt/us/documents" || got.User != "root" {
		t.Fatalf("defaults wrong: %+v", got)
	}
	if got.HostPort() != "192.168.0.42:2222" {
		t.Fatalf("HostPort = %q", got.HostPort())
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, filepath.Join(dir, profileFile), 0o600)
}

func TestSignerIsGeneratedOnce(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	a, err := s.Signer()
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Signer()
	if err != nil {
		t.Fatal(err)
	}
	if AuthorizedKey(a.PublicKey()) != AuthorizedKey(b.PublicKey()) {
		t.Fatal("key changed between calls")
	}
	if a.PublicKey().Type() != "ssh-ed25519" {
		t.Fatalf("key type %s", a.PublicKey().Type())
	}
	raw, _ := os.ReadFile(filepath.Join(dir, keyFile))
	if !strings.HasPrefix(string(raw), "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatal("private key is not in OpenSSH format")
	}
	assertMode(t, filepath.Join(dir, keyFile), 0o600)

	pub, err := s.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || strings.Contains(pub, "\n") {
		t.Fatalf("PublicKey = %q", pub)
	}

	reopened, _ := Open(dir)
	c, _ := reopened.Signer()
	if AuthorizedKey(c.PublicKey()) != AuthorizedKey(a.PublicKey()) {
		t.Fatal("key changed after reopening the store")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", filepath.Base(path), got, want)
	}
}

func TestUpdateChangesTheSavedProfile(t *testing.T) {
	s, _ := Open(t.TempDir())
	p := New("192.168.0.42", DefaultPort)
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Update(func(p *Profile) error {
		p.Port, p.KeepAwake = 22, false
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != reloaded || reloaded.Port != 22 || reloaded.KeepAwake {
		t.Fatalf("returned %+v, saved %+v", got, reloaded)
	}
}

func TestUpdateSavesNothingOnError(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.Save(New("192.168.0.42", DefaultPort)); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("refused")
	_, err := s.Update(func(p *Profile) error {
		p.Port = 22
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v", err)
	}
	if p, _ := s.Load(); p.Port != DefaultPort {
		t.Fatalf("port saved anyway: %d", p.Port)
	}
}

func TestUpdateWithoutProfile(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.Update(func(*Profile) error { return nil }); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("err = %v, want ErrNotPaired", err)
	}
}

func TestForgetDropsThePairingAndTheKey(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	old, err := s.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(New("192.168.0.42", DefaultPort)); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("Load after Forget: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
	fresh, err := s.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("the forgotten Device's key is still ember's key")
	}
	if err := s.Forget(); err != nil {
		t.Fatalf("second Forget: %v", err)
	}
}
