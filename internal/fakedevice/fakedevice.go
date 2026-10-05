// Package fakedevice runs an in-process stand-in for a Kindle with KOReader's
// SSH server: a dropbear-like SSH server with SFTP over a temp directory laid
// out like the Kindle's file system. Tests dial it on 127.0.0.1.
package fakedevice

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Banner is the version string the fake announces.
const Banner = "SSH-2.0-dropbear_2022.83"

// Paths on the Device, as KOReader lays them out on a Kindle.
const (
	Documents      = "/mnt/us/documents"
	KOReaderDir    = "/mnt/us/koreader"
	Settings       = "/mnt/us/koreader/settings.reader.lua"
	GitRev         = "/mnt/us/koreader/git-rev"
	AuthorizedKeys = "/mnt/us/koreader/settings/SSH/authorized_keys"
)

// Device is a running fake.
type Device struct {
	root    string
	ln      net.Listener
	keys    []ssh.Signer
	pwdless atomic.Bool

	mu    sync.Mutex
	execs []string
}

// Option configures a Device.
type Option func(*Device)

// WithHostKeySeed derives the ed25519 host key from seed, so two fakes with
// the same seed look like the same Device and different seeds like an
// impostor.
func WithHostKeySeed(seed byte) Option {
	return func(d *Device) {
		d.keys[0] = mustSigner(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize)))
	}
}

// Passwordless starts the fake with "Login without password" on.
func Passwordless() Option {
	return func(d *Device) { d.pwdless.Store(true) }
}

// New starts a fake Device and stops it when the test ends. Like dropbear
// with -R, it offers an ed25519 and an ECDSA host key.
func New(t testing.TB, opts ...Option) *Device {
	t.Helper()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := &Device{
		root: t.TempDir(),
		keys: []ssh.Signer{mustSigner(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))), mustSigner(ecKey)},
	}
	for _, o := range opts {
		o(d)
	}
	for _, dir := range []string{Documents, KOReaderDir} {
		if err := os.MkdirAll(d.Path(dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d.WriteFile(t, GitRev, "v2026.07.1\n")
	d.WriteFile(t, Settings, "-- we can read Lua syntax here!\nreturn {\n    [\"document_metadata_folder\"] = \"doc\",\n    [\"home_dir\"] = \"/mnt/us/documents\",\n}\n")

	d.ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		d.ln.Close()
		wg.Wait()
	})
	cfg := d.serverConfig()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := d.ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				d.serve(conn, cfg)
			}()
		}
	}()
	return d
}

// Addr is the fake's "host:port".
func (d *Device) Addr() string { return d.ln.Addr().String() }

// HostKey is the ed25519 host key the fake presents.
func (d *Device) HostKey() ssh.PublicKey { return d.keys[0].PublicKey() }

// HostKeys is every host key the fake holds, ed25519 first.
func (d *Device) HostKeys() []ssh.PublicKey {
	return []ssh.PublicKey{d.keys[0].PublicKey(), d.keys[1].PublicKey()}
}

// SetPasswordless switches "Login without password" on or off. It applies to
// the next login, like restarting KOReader's SSH server.
func (d *Device) SetPasswordless(on bool) { d.pwdless.Store(on) }

// Path maps a Device path to where the fake keeps it on the host.
func (d *Device) Path(devicePath string) string {
	return filepath.Join(d.root, filepath.FromSlash(path.Clean("/"+devicePath)))
}

// WriteFile creates a file on the Device, with its parent directories.
func (d *Device) WriteFile(t testing.TB, devicePath, content string) {
	t.Helper()
	p := d.Path(devicePath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ReadFile returns a file's content on the Device, or "" if it is missing.
func (d *Device) ReadFile(t testing.TB, devicePath string) string {
	t.Helper()
	raw, err := os.ReadFile(d.Path(devicePath))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(raw)
}

// Execs returns the commands clients asked the fake to run, in order.
func (d *Device) Execs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.execs...)
}

func (d *Device) serverConfig() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		ServerVersion: Banner,
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return d.passwordless()
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if d.authorized(key) {
				return nil, nil
			}
			return nil, errors.New("key not authorized")
		},
	}
	for _, k := range d.keys {
		cfg.AddHostKey(k)
	}
	return cfg
}

func (d *Device) passwordless() (*ssh.Permissions, error) {
	if d.pwdless.Load() {
		return nil, nil
	}
	return nil, errors.New("password login disabled")
}

// authorized reads authorized_keys on every login, as dropbear does.
func (d *Device) authorized(key ssh.PublicKey) bool {
	rest, err := os.ReadFile(d.Path(AuthorizedKeys))
	if err != nil {
		return false
	}
	for len(rest) > 0 {
		var k ssh.PublicKey
		k, _, _, rest, err = ssh.ParseAuthorizedKey(rest)
		if err != nil {
			return false
		}
		if bytes.Equal(k.Marshal(), key.Marshal()) {
			return true
		}
	}
	return false
}

func (d *Device) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go d.session(ch, chReqs)
	}
}

func (d *Device) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			if string(payloadString(req.Payload)) != "sftp" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			srv := sftp.NewRequestServer(ch, handlers(&files{root: d.root}))
			_ = srv.Serve()
			srv.Close()
			return
		case "exec":
			_ = req.Reply(true, nil)
			d.mu.Lock()
			d.execs = append(d.execs, string(payloadString(req.Payload)))
			d.mu.Unlock()
			_, _ = io.Copy(io.Discard, ch)
			_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

// payloadString decodes the SSH string at the start of an exec or subsystem
// request payload.
func payloadString(p []byte) []byte {
	if len(p) < 4 {
		return nil
	}
	n := binary.BigEndian.Uint32(p)
	if uint64(n) > uint64(len(p)-4) {
		return nil
	}
	return p[4 : 4+n]
}

func mustSigner(key any) ssh.Signer {
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		panic(err)
	}
	return s
}
