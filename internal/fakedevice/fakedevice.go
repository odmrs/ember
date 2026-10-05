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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// The fake's file system is 1 GiB, with DefaultFree of it free until
// SetFreeSpace says otherwise.
const (
	TotalSpace  = 1 << 30
	DefaultFree = 3 << 28
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
	t       testing.TB
	root    string
	addr    string
	keys    []ssh.Signer
	pwdless atomic.Bool
	mute    atomic.Bool
	noLipc  atomic.Bool
	noExec  atomic.Bool
	quick   atomic.Bool
	free    atomic.Uint64
	wg      sync.WaitGroup

	mu      sync.Mutex
	ln      net.Listener
	conns   map[net.Conn]bool
	execs   []string
	running int
	exits   []Exit
	awake   bool // powerd's preventScreenSaver
}

// Exit is how a command a client ran on the fake ended.
type Exit struct {
	Command string
	Status  int
	// Delivered is false when the channel was already gone, so the client
	// never saw the status: it dropped the connection rather than letting
	// the command finish.
	Delivered bool
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

// WithoutLipc starts the fake without lipc-set-prop, like a Device whose
// firmware lacks it.
func WithoutLipc() Option {
	return func(d *Device) { d.noLipc.Store(true) }
}

// RefusingExec starts the fake refusing every command, so starting one
// fails. Refused commands are still recorded in Execs.
func RefusingExec() Option {
	return func(d *Device) { d.noExec.Store(true) }
}

// QuickExit starts the fake ending every command at once with status 0,
// without reading its stdin.
func QuickExit() Option {
	return func(d *Device) { d.quick.Store(true) }
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
		t:     t,
		conns: map[net.Conn]bool{},
		root:  t.TempDir(),
		keys:  []ssh.Signer{mustSigner(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))), mustSigner(ecKey)},
	}
	d.free.Store(DefaultFree)
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

	d.listen("127.0.0.1:0")
	t.Cleanup(func() {
		d.Stop()
		d.wg.Wait()
	})
	return d
}

func (d *Device) listen(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		d.t.Fatal(err)
	}
	d.mu.Lock()
	d.ln = ln
	d.addr = ln.Addr().String()
	d.mu.Unlock()
	cfg := d.serverConfig()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			if d.ln != ln {
				// Stop ran between Accept and here.
				d.mu.Unlock()
				conn.Close()
				continue
			}
			d.conns[conn] = true
			d.mu.Unlock()
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.serve(conn, cfg)
				d.mu.Lock()
				delete(d.conns, conn)
				d.mu.Unlock()
			}()
		}
	}()
}

// Addr is the fake's "host:port". It stays the same across Stop and Start.
func (d *Device) Addr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.addr
}

// Stop shuts the SSH server down and drops every open connection, like the
// Kindle going to sleep.
func (d *Device) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ln != nil {
		d.ln.Close()
		d.ln = nil
	}
	for c := range d.conns {
		c.Close()
	}
}

// StopListening closes the listener but keeps open connections alive. That is
// what stopping KOReader's SSH server does: dropbear serves each connection in
// its own process, and only the one accepting new connections goes away.
func (d *Device) StopListening() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ln != nil {
		d.ln.Close()
		d.ln = nil
	}
}

// Listening reports whether the SSH server accepts new connections.
func (d *Device) Listening() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ln != nil
}

// Start brings the SSH server back on the same address after Stop.
func (d *Device) Start() {
	d.listen(d.Addr())
}

// SetUnresponsive makes open connections stop answering requests while
// staying open, like a Device that dropped off Wi-Fi without closing them.
// New connections still log in.
func (d *Device) SetUnresponsive(on bool) { d.mute.Store(on) }

// SetFreeSpace changes the free bytes the fake reports, rounded down to
// whole 4 KiB blocks as a real file system reports them.
func (d *Device) SetFreeSpace(free uint64) { d.free.Store(free) }

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

// Running is how many commands are running on the fake right now.
func (d *Device) Running() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

// Exits lists the commands that have finished, in order.
func (d *Device) Exits() []Exit {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Exit(nil), d.exits...)
}

// PreventScreenSaver is powerd's preventScreenSaver property, as
// lipc-set-prop last set it.
func (d *Device) PreventScreenSaver() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.awake
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
	go func() {
		for req := range reqs {
			if req.WantReply && !d.mute.Load() {
				_ = req.Reply(false, nil)
			}
		}
	}()
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
			srv := sftp.NewRequestServer(ch, handlers(&files{root: d.root, free: &d.free}))
			_ = srv.Serve()
			srv.Close()
			return
		case "exec":
			cmd := string(payloadString(req.Payload))
			d.mu.Lock()
			d.execs = append(d.execs, cmd)
			d.mu.Unlock()
			if d.noExec.Load() {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			d.mu.Lock()
			d.running++
			d.mu.Unlock()
			status := 0
			if !d.quick.Load() {
				status = d.run(cmd, ch)
			}
			_, err := ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, uint32(status)))
			d.mu.Lock()
			d.running--
			d.exits = append(d.exits, Exit{Command: cmd, Status: status, Delivered: err == nil})
			d.mu.Unlock()
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

const lipcPrefix = "lipc-set-prop com.lab126.powerd preventScreenSaver "

// stopServer is KOReader's own SSH stop, run detached: TERM the listener
// whose pid KOReader keeps, plug the two firewall holes for the port, and
// drop the pid file once the listener is gone.
var stopServer = regexp.MustCompile(`^nohup sh -c 'sleep 1; pid=\$\(cat /tmp/dropbear_koreader\.pid\); kill -TERM "\$pid"; sleep 1; ` +
	`iptables -D INPUT -p tcp --dport (\d+) -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT; ` +
	`iptables -D OUTPUT -p tcp --sport (\d+) -m conntrack --ctstate ESTABLISHED -j ACCEPT; ` +
	`\[ -d "/proc/\$pid" \] \|\| rm -f /tmp/dropbear_koreader\.pid' >/dev/null 2>&1 &$`)

// stopsThisServer reports whether cmd is the stop command for the port the
// fake listens on.
func (d *Device) stopsThisServer(cmd string) bool {
	m := stopServer.FindStringSubmatch(cmd)
	if m == nil || m[1] != m[2] {
		return false
	}
	_, port, err := net.SplitHostPort(d.Addr())
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && strconv.Itoa(n) == port
}

// run plays the part of the Device's shell for the commands ember sends: a
// "; "-separated list of steps, each of which may end in "|| exit $?". A
// step is "cat >/dev/null", which reads stdin until it closes, or
// lipc-set-prop setting powerd's preventScreenSaver. KOReader's SSH stop for
// the fake's own port closes the listener and leaves open connections, as
// dropbear does. Any other command reads stdin to the end and succeeds.
func (d *Device) run(cmd string, stdin io.Reader) int {
	if d.stopsThisServer(cmd) {
		d.StopListening()
		_, _ = io.Copy(io.Discard, stdin)
		return 0
	}
	steps := strings.Split(cmd, "; ")
	for _, step := range steps {
		step, _ = strings.CutSuffix(step, " || exit $?")
		if v, ok := strings.CutPrefix(step, lipcPrefix); !(step == "cat >/dev/null" || ok && (v == "0" || v == "1")) {
			_, _ = io.Copy(io.Discard, stdin)
			return 0
		}
	}
	status := 0
	for _, step := range steps {
		step, exitOnFail := strings.CutSuffix(step, " || exit $?")
		if v, ok := strings.CutPrefix(step, lipcPrefix); ok {
			status = 0
			if d.noLipc.Load() {
				status = 127
			} else {
				d.mu.Lock()
				d.awake = v == "1"
				d.mu.Unlock()
			}
		} else {
			_, _ = io.Copy(io.Discard, stdin)
			status = 0
		}
		if status != 0 && exitOnFail {
			return status
		}
	}
	return status
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
