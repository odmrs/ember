package pairing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/profile"
	"golang.org/x/crypto/ssh"
)

func TestSubnetTargetsCoverOnlyTheLocal24(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.0.42"), Mask: net.CIDRMask(16, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.0.7"), Mask: net.CIDRMask(24, 32)}, // same /24
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("169.254.3.4"), Mask: net.CIDRMask(16, 32)},
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPAddr{IP: net.ParseIP("10.0.0.1")},
	}
	got := subnetTargets(addrs, []int{2222, 22})
	if len(got) != 254*2 {
		t.Fatalf("got %d targets, want %d", len(got), 254*2)
	}
	for _, want := range []string{"192.168.0.1:2222", "192.168.0.1:22", "192.168.0.254:2222"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, a := range got {
		if !strings.HasPrefix(a, "192.168.0.") || strings.HasPrefix(a, "192.168.0.0:") || strings.HasPrefix(a, "192.168.0.255:") {
			t.Errorf("unexpected target %s", a)
		}
	}
}

// otherSSH answers like an OpenSSH server and closes.
func otherSSH(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func closedPort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestFindKeepsOnlyDropbear(t *testing.T) {
	open := fakedevice.New(t, fakedevice.Passwordless())
	locked := fakedevice.New(t, fakedevice.WithHostKeySeed(7))
	got := Find(context.Background(), []string{closedPort(t), otherSSH(t), open.Addr(), locked.Addr()})
	if len(got) != 2 {
		t.Fatalf("found %d candidates: %+v", len(got), got)
	}
	o, l := got[0], got[1]
	if o.Addr != open.Addr() || o.Fingerprint != ssh.FingerprintSHA256(open.HostKey()) || o.Banner != fakedevice.Banner {
		t.Errorf("open candidate = %+v", o)
	}
	if !o.Passwordless || o.Survey == nil || o.Survey.KOReaderVersion != "v2026.07.1" || o.Error != "" {
		t.Errorf("open candidate survey = %+v, %+v", o, o.Survey)
	}
	if l.Fingerprint != ssh.FingerprintSHA256(locked.HostKey()) || l.Passwordless || l.Survey != nil || l.Error != "" {
		t.Errorf("locked candidate = %+v", l)
	}
}

func TestFindWritesNothing(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	before := snapshot(t, fake.Path("/"))
	Find(context.Background(), []string{fake.Addr()})
	if after := snapshot(t, fake.Path("/")); !slices.Equal(before, after) {
		t.Fatalf("device changed:\nbefore %v\nafter  %v", before, after)
	}
	if got := fake.Execs(); len(got) != 0 {
		t.Fatalf("ran commands: %q", got)
	}
}

func snapshot(t *testing.T, root string) []string {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprint(p, fi.Mode(), fi.ModTime(), fi.Size()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func passwordlessDial(t *testing.T, fake *fakedevice.Device) *device.Device {
	t.Helper()
	d, err := device.Dial(context.Background(), device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestSurvey(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	fake.WriteFile(t, fakedevice.Settings, `return {
    ["document_metadata_folder"] = "hash",
    ["home_dir"] = "/mnt/us/documents",
    ["lastfile"] = "/mnt/us/documents/Dune.epub",
}`)
	s, err := Survey(passwordlessDial(t, fake), profile.DefaultLibraryRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := profile.Survey{
		KOReaderVersion: "v2026.07.1", HomeDir: "/mnt/us/documents", MetadataMode: "hash",
		LastFile: "/mnt/us/documents/Dune.epub", LibraryExists: true, FreeBytes: 3 << 28, TotalBytes: 1 << 30,
	}
	if s != want {
		t.Fatalf("survey = %+v\nwant     %+v", s, want)
	}
}

func TestSurveyOfABareDevice(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	for _, p := range []string{fakedevice.Settings, fakedevice.GitRev, fakedevice.Documents} {
		if err := os.Remove(fake.Path(p)); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Survey(passwordlessDial(t, fake), profile.DefaultLibraryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if s != (profile.Survey{}) {
		t.Fatalf("survey = %+v, want zero", s)
	}
}

func newKey(t *testing.T) ssh.PublicKey {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s.PublicKey()
}

func TestInstallKey(t *testing.T) {
	key := newKey(t)
	line := profile.AuthorizedKey(key) + " ember\n"
	other := profile.AuthorizedKey(newKey(t)) + " laptop"

	cases := []struct {
		name, before, after string
		added               bool
	}{
		{"fresh", "", line, true},
		{"existing", other + "\n", other + "\n" + line, true},
		{"existing without newline", "# mine\n" + other, "# mine\n" + other + "\n" + line, true},
		{"duplicate", other + "\n" + profile.AuthorizedKey(key) + " old-comment\n", other + "\n" + profile.AuthorizedKey(key) + " old-comment\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := fakedevice.New(t, fakedevice.Passwordless())
			if c.before != "" {
				fake.WriteFile(t, fakedevice.AuthorizedKeys, c.before)
			}
			d := passwordlessDial(t, fake)
			added, err := InstallKey(d, key, "ember")
			if err != nil || added != c.added {
				t.Fatalf("added = %v, err = %v", added, err)
			}
			again, err := InstallKey(d, key, "ember")
			if err != nil || again {
				t.Fatalf("second install: added = %v, err = %v", again, err)
			}
			if got := fake.ReadFile(t, fakedevice.AuthorizedKeys); got != c.after {
				t.Fatalf("authorized_keys = %q\nwant %q", got, c.after)
			}
		})
	}
}

func newFlow(t *testing.T, targets ...string) (*Flow, *profile.Store) {
	t.Helper()
	store, err := profile.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := NewFlow(Config{Store: store, Targets: func([]int) ([]string, error) { return targets, nil }})
	return f, store
}

func scan(t *testing.T, f *Flow, address string) State {
	t.Helper()
	if err := f.Scan(address, 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := f.State(); s.Phase == PhaseFound {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scan did not finish")
	return State{}
}

func TestPairingThroughPasswordless(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	f, store := newFlow(t, otherSSH(t), fake.Addr())
	ctx := context.Background()

	s := scan(t, f, "")
	if len(s.Candidates) != 1 {
		t.Fatalf("candidates = %+v", s.Candidates)
	}
	c := s.Candidates[0]
	if fake.ReadFile(t, fakedevice.AuthorizedKeys) != "" {
		t.Fatal("wrote to the device before confirmation")
	}
	if _, err := f.Confirm(ctx, c.Addr, "SHA256:not-it"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("wrong fingerprint: err = %v", err)
	}

	p, err := f.Confirm(ctx, c.Addr, c.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if p.HostKey != profile.AuthorizedKey(fake.HostKey()) || p.Survey.KOReaderVersion != "v2026.07.1" || p.Passwordless || !p.KeepAwake {
		t.Fatalf("profile = %+v", p)
	}
	saved, err := store.Load()
	if err != nil || saved != p {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	pub, _ := store.PublicKey()
	if got := fake.ReadFile(t, fakedevice.AuthorizedKeys); got != pub+"\n" {
		t.Fatalf("authorized_keys = %q", got)
	}
	if f.State().Phase != PhaseLock {
		t.Fatalf("phase = %s", f.State().Phase)
	}

	locked, err := f.CheckLock(ctx)
	if err != nil || locked {
		t.Fatalf("locked = %v, err = %v while passwordless is still on", locked, err)
	}
	fake.SetPasswordless(false)
	locked, err = f.CheckLock(ctx)
	if err != nil || !locked {
		t.Fatalf("locked = %v, err = %v after switching passwordless off", locked, err)
	}
	if f.State().Phase != PhaseDone {
		t.Fatalf("phase = %s", f.State().Phase)
	}

	if err := f.Scan("", 0); !errors.Is(err, ErrAlreadyPaired) {
		t.Fatalf("scan after pairing: err = %v", err)
	}
}

// The USB route: the key is already on the Device and passwordless login was
// never switched on.
func TestPairingWithKeyCopiedOverUSB(t *testing.T) {
	fake := fakedevice.New(t)
	f, store := newFlow(t)
	ctx := context.Background()

	s := scan(t, f, fake.Addr())
	c := s.Candidates[0]
	if c.Passwordless || c.Survey != nil {
		t.Fatalf("candidate = %+v", c)
	}
	if _, err := f.Confirm(ctx, c.Addr, c.Fingerprint); !errors.Is(err, ErrKeyNotAccepted) {
		t.Fatalf("before copying the key: err = %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, profile.ErrNotPaired) {
		t.Fatal("profile saved although pairing failed")
	}

	pub, err := f.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	fake.WriteFile(t, fakedevice.AuthorizedKeys, pub+"\n")
	p, err := f.Confirm(ctx, c.Addr, c.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Survey.LibraryExists {
		t.Fatalf("survey = %+v", p.Survey)
	}
	if got := fake.ReadFile(t, fakedevice.AuthorizedKeys); got != pub+"\n" {
		t.Fatalf("authorized_keys rewritten: %q", got)
	}
	if locked, err := f.CheckLock(ctx); err != nil || !locked {
		t.Fatalf("locked = %v, err = %v", locked, err)
	}
}

func TestLaterConnectWithWrongHostKeyIsAnError(t *testing.T) {
	fake := fakedevice.New(t)
	impostor := fakedevice.New(t, fakedevice.WithHostKeySeed(9))
	f, store := newFlow(t)
	host, port, _ := net.SplitHostPort(impostor.Addr())
	n, _ := strconv.Atoi(port)
	p := profile.New(host, n)
	p.HostKey = profile.AuthorizedKey(fake.HostKey())
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	_, err := f.CheckLock(context.Background())
	var mismatch *device.HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want HostKeyMismatchError", err)
	}
}

func TestScanRejectsBadAddresses(t *testing.T) {
	f, _ := newFlow(t)
	for _, a := range []string{"; rm -rf /", "host:99999", "host:abc", "-oProxyCommand=x", "a b"} {
		if err := f.Scan(a, 0); !errors.Is(err, ErrBadAddress) {
			t.Errorf("%q: err = %v", a, err)
		}
	}
	if err := f.Scan("", 70000); !errors.Is(err, ErrBadAddress) {
		t.Errorf("bad port: err = %v", err)
	}
}

func TestNormalizeAddress(t *testing.T) {
	cases := map[string]string{
		"192.168.0.42":    "192.168.0.42:2222",
		"192.168.0.42:22": "192.168.0.42:22",
		"kindle.lan":      "kindle.lan:2222",
		"[fe80::1]:2222":  "[fe80::1]:2222",
	}
	for in, want := range cases {
		if got, err := normalizeAddress(in, 0); err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
}
