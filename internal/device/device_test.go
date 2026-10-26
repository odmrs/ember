package device_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io/fs"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/fakedevice"
	"golang.org/x/crypto/ssh"
)

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func authorize(t *testing.T, fake *fakedevice.Device, s ssh.Signer) {
	fake.WriteFile(t, fakedevice.AuthorizedKeys, string(ssh.MarshalAuthorizedKey(s.PublicKey())))
}

func dial(t *testing.T, cfg device.Config) *device.Device {
	t.Helper()
	d, err := device.Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestHostKeyPrefersEd25519(t *testing.T) {
	fake := fakedevice.New(t)
	key, err := device.HostKey(context.Background(), fake.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(key) != ssh.FingerprintSHA256(fake.HostKey()) {
		t.Fatalf("got %s, want %s", ssh.FingerprintSHA256(key), ssh.FingerprintSHA256(fake.HostKey()))
	}
}

// The Device holds several host keys; dialing must ask for the pinned one
// instead of reporting a mismatch.
func TestDialPinnedToECDSA(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	ecdsa := fake.HostKeys()[1]
	dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: ecdsa})
}

func TestDialWithKey(t *testing.T) {
	fake := fakedevice.New(t)
	signer := newSigner(t)
	authorize(t, fake, signer)
	d := dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey(), Signer: signer})

	got, err := d.ReadFile(fakedevice.GitRev, 64)
	if err != nil || string(got) != "v2026.07.1\n" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	if _, err := d.ReadFile(fakedevice.GitRev, 4); !errors.Is(err, device.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if _, err := d.Stat("/mnt/us/nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat missing: %v", err)
	}
	free, total, err := d.FreeSpace(fakedevice.Documents)
	if err != nil || free == 0 || total < free {
		t.Fatalf("FreeSpace = %d, %d, %v", free, total, err)
	}
}

func TestDialPasswordless(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	d := dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	if _, err := d.Stat(fakedevice.Documents); err != nil {
		t.Fatal(err)
	}

	fake.SetPasswordless(false)
	_, err := device.Dial(context.Background(), device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	if !errors.Is(err, device.ErrAuthRejected) {
		t.Fatalf("err = %v, want ErrAuthRejected", err)
	}
}

func TestUnknownKeyIsRejected(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	_, err := device.Dial(context.Background(), device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey(), Signer: newSigner(t)})
	if !errors.Is(err, device.ErrAuthRejected) {
		t.Fatalf("err = %v, want ErrAuthRejected (no fallback to passwordless)", err)
	}
}

func TestWrongHostKeyIsAHardError(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	impostor := fakedevice.New(t, fakedevice.Passwordless(), fakedevice.WithHostKeySeed(9))
	_, err := device.Dial(context.Background(), device.Config{Addr: impostor.Addr(), User: "root", HostKey: fake.HostKey()})
	var mismatch *device.HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want HostKeyMismatchError", err)
	}
	if mismatch.Want != ssh.FingerprintSHA256(fake.HostKey()) || mismatch.Got != ssh.FingerprintSHA256(impostor.HostKey()) {
		t.Fatalf("mismatch = %+v", mismatch)
	}
}

func TestDialRequiresPinnedKey(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	if _, err := device.Dial(context.Background(), device.Config{Addr: fake.Addr(), User: "root"}); err == nil {
		t.Fatal("dial without a pinned host key succeeded")
	}
}

func TestAppendCreatesThenAppends(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	d := dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})

	if err := d.MkdirAll("/mnt/us/koreader/settings/SSH", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := d.Append(fakedevice.AuthorizedKeys, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.Append(fakedevice.AuthorizedKeys, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := fake.ReadFile(t, fakedevice.AuthorizedKeys); got != "one\ntwo\n" {
		t.Fatalf("content = %q", got)
	}
	if runtime.GOOS != "windows" {
		for p, want := range map[string]os.FileMode{fakedevice.AuthorizedKeys: 0o600, "/mnt/us/koreader/settings/SSH": 0o700} {
			fi, err := os.Stat(fake.Path(p))
			if err != nil || fi.Mode().Perm() != want {
				t.Errorf("%s: mode %v, err %v, want %o", p, fi.Mode().Perm(), err, want)
			}
		}
	}
}

func TestExecIsRecorded(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	d := dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	if _, err := d.Exec("true"); err != nil {
		t.Fatal(err)
	}
	if got := fake.Execs(); len(got) != 1 || got[0] != "true" {
		t.Fatalf("Execs = %q", got)
	}
}

func TestSFTPCannotEscapeTheFakeRoot(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	d := dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	fake.WriteFile(t, "/etc/passwd", "fake")
	got, err := d.ReadFile("/../../../../etc/passwd", 64)
	if err != nil || string(got) != "fake" {
		t.Fatalf("got %q, %v; want the fake's own /etc/passwd", got, err)
	}
}

func TestDialTimesOut(t *testing.T) {
	// A listener that accepts and never speaks stalls the handshake.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	start := time.Now()
	_, err = device.HostKey(context.Background(), ln.Addr().String(), 100*time.Millisecond)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestPingAndWaitFollowTheConnection(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	d := dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	if err := d.Ping(); err != nil {
		t.Fatalf("Ping on a live link: %v", err)
	}

	waited := make(chan struct{})
	go func() {
		d.Wait()
		close(waited)
	}()
	fake.Stop()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the Device went away")
	}
	if err := d.Ping(); err == nil {
		t.Fatal("Ping succeeded on a closed link")
	}

	fake.Start()
	dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
}

// A Device that dropped off Wi-Fi answers nothing, yet closing the link to it
// must not wait for an answer: the Connector cannot search again until it
// returns.
func TestCloseDoesNotWaitForASilentDevice(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	d, err := device.Dial(context.Background(), device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	if err != nil {
		t.Fatal(err)
	}
	fake.SetUnresponsive(true)

	closed := make(chan struct{})
	go func() {
		d.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		fake.Stop() // lets the stuck Close return so the test can end
		t.Fatal("Close waited on a Device that does not answer")
	}
}

const holdAwake = "lipc-set-prop com.lab126.powerd preventScreenSaver 1 || exit $?; cat >/dev/null; lipc-set-prop com.lab126.powerd preventScreenSaver 0"

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartRunsUntilStdinCloses(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	d := dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	p, err := d.Start(holdAwake)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "preventScreenSaver 1", fake.PreventScreenSaver)
	select {
	case <-p.Done():
		t.Fatal("ended before stdin closed")
	case <-time.After(50 * time.Millisecond):
	}
	if err := p.CloseStdin(); err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	if status, ok := p.ExitStatus(); !ok || status != 0 {
		t.Fatalf("ExitStatus = %d, %v", status, ok)
	}
	if fake.PreventScreenSaver() {
		t.Fatal("preventScreenSaver still 1")
	}
	if ex := fake.Exits(); len(ex) != 1 || !ex[0].Delivered {
		t.Fatalf("Exits = %+v", ex)
	}
}

func TestStartReportsAMissingCommand(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless(), fakedevice.WithoutLipc())
	d := dial(t, device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	p, err := d.Start(holdAwake)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("still running without lipc-set-prop")
	}
	if status, ok := p.ExitStatus(); !ok || status != 127 {
		t.Fatalf("ExitStatus = %d, %v; want 127", status, ok)
	}
}

// A connection that drops ends the command on the Device without ember
// closing stdin, and the client learns no exit status.
func TestStartEndsWithTheConnection(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	d, err := device.Dial(context.Background(), device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.Start(holdAwake)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "preventScreenSaver 1", fake.PreventScreenSaver)
	d.Close()
	<-p.Done()
	if _, ok := p.ExitStatus(); ok {
		t.Fatal("exit status reported after the connection dropped")
	}
	waitUntil(t, "preventScreenSaver 0", func() bool { return !fake.PreventScreenSaver() })
	waitUntil(t, "the command to end", func() bool { return len(fake.Exits()) == 1 })
	if fake.Exits()[0].Delivered {
		t.Fatal("exit status delivered over a dropped connection")
	}
}
