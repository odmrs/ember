// Package device is ember's SSH and SFTP link to the Device. Every connection
// checks the pinned host key; a different key is a hard error.
package device

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// maxInFlight bounds the SFTP requests a transfer keeps outstanding: 512 KiB
// of 32 KiB packets. That still covers the round trip of a Kindle on Wi-Fi,
// and keeps the queue short enough that the Connector's keepalive, which
// waits behind it, is answered in time on a slow link.
const maxInFlight = 16

// passwordlessSecret is what ember types when KOReader's "Login without
// password" is on. The Device accepts any password in that mode, so this is
// not a secret.
const passwordlessSecret = "ember"

// ErrAuthRejected means the Device was reached and its host key matched, but
// it refused the login.
var ErrAuthRejected = errors.New("device: login rejected")

// HostKeyMismatchError means the host at the address is not the paired
// Device, or the Device's host key changed.
type HostKeyMismatchError struct {
	Want, Got string // SHA256 fingerprints
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("device: host key mismatch: got %s, pinned %s", e.Got, e.Want)
}

// Config says how to reach and log in to the Device.
type Config struct {
	Addr    string        // "host:port"
	User    string        // "root" on a Kindle
	HostKey ssh.PublicKey // pinned; required
	// Signer logs in with ember's key. When nil, ember logs in through
	// KOReader's "Login without password" instead.
	Signer  ssh.Signer
	Timeout time.Duration // connect and handshake; 10s when zero
}

// Device is an open SSH connection with an SFTP session on top.
type Device struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

// Dial connects, checks the host key and logs in.
func Dial(ctx context.Context, cfg Config) (*Device, error) {
	if cfg.HostKey == nil {
		return nil, errors.New("device: no pinned host key")
	}
	var mismatch *HostKeyMismatchError
	checked := false
	cc := &ssh.ClientConfig{
		User:              cfg.User,
		HostKeyAlgorithms: algorithmsFor(cfg.HostKey),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if !bytes.Equal(key.Marshal(), cfg.HostKey.Marshal()) {
				mismatch = &HostKeyMismatchError{Want: ssh.FingerprintSHA256(cfg.HostKey), Got: ssh.FingerprintSHA256(key)}
				return mismatch
			}
			checked = true
			return nil
		},
	}
	if cfg.Signer != nil {
		cc.Auth = []ssh.AuthMethod{ssh.PublicKeys(cfg.Signer)}
	} else {
		// Dropbear offers only password and publickey; the client tries
		// "none" on its own first.
		cc.Auth = []ssh.AuthMethod{ssh.Password(passwordlessSecret)}
	}

	client, err := handshake(ctx, cfg.Addr, cfg.Timeout, cc)
	switch {
	case mismatch != nil:
		return nil, mismatch
	case err != nil && checked && strings.Contains(err.Error(), "unable to authenticate"):
		return nil, ErrAuthRejected
	case err != nil:
		return nil, err
	}
	sc, err := sftp.NewClient(client, sftp.MaxConcurrentRequestsPerFile(maxInFlight))
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("device: sftp: %w", err)
	}
	return &Device{ssh: client, sftp: sc}, nil
}

// errProbed stops a handshake once the host key is known.
var errProbed = errors.New("probed")

// HostKey connects to addr and returns the host key it presents, without
// attempting to log in.
func HostKey(ctx context.Context, addr string, timeout time.Duration) (ssh.PublicKey, error) {
	var got ssh.PublicKey
	cc := &ssh.ClientConfig{
		HostKeyAlgorithms: preferredAlgorithms,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errProbed
		},
	}
	client, err := handshake(ctx, addr, timeout, cc)
	if client != nil {
		client.Close()
	}
	if got == nil {
		if err == nil {
			err = errors.New("device: no host key presented")
		}
		return nil, err
	}
	return got, nil
}

// preferredAlgorithms asks for ed25519 first. Dropbear holds ed25519, ECDSA
// and RSA host keys and presents whichever the client asks for.
var preferredAlgorithms = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

// algorithmsFor restricts negotiation to the pinned key's type, so the Device
// presents the pinned key instead of another one of its host keys.
func algorithmsFor(key ssh.PublicKey) []string {
	if key.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{key.Type()}
}

func handshake(ctx context.Context, addr string, timeout time.Duration, cc *ssh.ClientConfig) (*ssh.Client, error) {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cc)
	if !stop() || err != nil {
		conn.Close()
		if err == nil {
			err = ctx.Err()
		}
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// Close ends the SFTP session and the connection.
func (d *Device) Close() error {
	d.sftp.Close()
	return d.ssh.Close()
}

// Wait blocks until the connection is closed, from either end.
func (d *Device) Wait() error {
	return d.ssh.Wait()
}

// Ping asks the Device for a reply over the open connection. Any reply, even
// a refusal, proves the link is alive; Ping blocks until one arrives or the
// connection closes, so callers wanting a deadline close the Device.
func (d *Device) Ping() error {
	_, _, err := d.ssh.SendRequest("keepalive@openssh.com", true, nil)
	return err
}

// ErrTooLarge is returned by ReadFile when the file exceeds the given limit.
var ErrTooLarge = errors.New("device: file too large")

// ReadFile reads a whole file of at most max bytes.
func (d *Device) ReadFile(path string, max int64) ([]byte, error) {
	f, err := d.sftp.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, path)
	}
	return data, nil
}

// Stat returns a file's info; a missing file gives an error matching
// fs.ErrNotExist.
func (d *Device) Stat(path string) (fs.FileInfo, error) {
	return d.sftp.Stat(path)
}

// ReadDir lists a directory. Each entry describes itself: a symbolic link is
// reported as a link, not as what it points to.
func (d *Device) ReadDir(path string) ([]fs.FileInfo, error) {
	return d.sftp.ReadDir(path)
}

// RealPath has the Device resolve path to its canonical absolute form, with
// "." and ".." removed and symbolic links followed. The last component does
// not have to exist.
func (d *Device) RealPath(path string) (string, error) {
	return d.sftp.RealPath(path)
}

// MkdirAll creates path and any missing parents. Directories it creates get
// mode perm.
func (d *Device) MkdirAll(path string, perm fs.FileMode) error {
	if fi, err := d.sftp.Stat(path); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("device: %s is not a directory", path)
		}
		return nil
	}
	if parent := parentDir(path); parent != path {
		if err := d.MkdirAll(parent, perm); err != nil {
			return err
		}
	}
	if err := d.sftp.Mkdir(path); err != nil {
		return err
	}
	d.chmod(path, perm)
	return nil
}

// Mkdir creates one directory and fails if path already exists.
func (d *Device) Mkdir(path string) error {
	return d.sftp.Mkdir(path)
}

// Rename uses the plain SFTP rename, never posix-rename, which replaces an
// existing target by design.
func (d *Device) Rename(from, to string) error {
	return d.sftp.Rename(from, to)
}

// Create creates a new file at path for writing and fails if path already
// exists, so it never truncates anything. Copying into it with io.Copy sends
// packets with several requests in flight, which is what makes SFTP fast
// over Wi-Fi; memory stays bounded by those packets whatever the size of the
// source.
func (d *Device) Create(path string) (io.WriteCloser, error) {
	f, err := d.sftp.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return nil, err
	}
	return newFile{f}, nil
}

// newFile is a file Create opened.
type newFile struct{ *sftp.File }

func (f newFile) ReadFrom(r io.Reader) (int64, error) {
	return f.File.ReadFromWithConcurrency(r, 0)
}

// Open opens a file for reading. Copying from it with io.Copy reads with
// several requests in flight.
func (d *Device) Open(path string) (io.ReadCloser, error) {
	return d.sftp.Open(path)
}

// Remove deletes a file, a symbolic link itself, or an empty directory.
func (d *Device) Remove(path string) error {
	return d.sftp.Remove(path)
}

// Append adds data to the end of path, creating the file with mode perm if it
// does not exist. Existing content is never truncated.
func (d *Device) Append(path string, data []byte, perm fs.FileMode) error {
	_, err := d.sftp.Stat(path)
	created := errors.Is(err, fs.ErrNotExist)
	if err != nil && !created {
		return err
	}
	f, err := d.sftp.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND)
	if err != nil {
		return err
	}
	// Write at the current end as well, for servers that ignore the append
	// flag; for those that honour it the offset makes no difference.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if created {
		d.chmod(path, perm)
	}
	return nil
}

// chmod applies perm where the file system supports it. The Kindle's user
// storage is FAT, which has no permission bits and may refuse the request, so
// a failure is not an error: whatever relies on the file is verified directly.
func (d *Device) chmod(path string, perm fs.FileMode) {
	_ = d.sftp.Chmod(path, perm)
}

// FreeSpace returns the free and total bytes of the file system holding path.
func (d *Device) FreeSpace(path string) (free, total uint64, err error) {
	st, err := d.sftp.StatVFS(path)
	if err != nil {
		return 0, 0, err
	}
	return st.FreeSpace(), st.TotalSpace(), nil
}

// Exec runs cmd on the Device and returns its combined output. cmd goes
// through the Device's shell, so it must be a fixed string and never include
// anything a user typed.
func (d *Device) Exec(cmd string) ([]byte, error) {
	s, err := d.ssh.NewSession()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.CombinedOutput(cmd)
}

// Process is a command running on the Device.
type Process struct {
	s     *ssh.Session
	stdin io.WriteCloser
	done  chan struct{}
	err   error
}

// Start runs cmd on the Device and returns while it runs. Like Exec, cmd
// goes through the Device's shell and must be a fixed string. Its output is
// discarded. The command lives in its own channel of the connection: it sees
// end of input when CloseStdin is called or the connection drops.
func (d *Device) Start(cmd string) (*Process, error) {
	s, err := d.ssh.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := s.StdinPipe()
	if err == nil {
		err = s.Start(cmd)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	p := &Process{s: s, stdin: stdin, done: make(chan struct{})}
	go func() {
		p.err = s.Wait()
		close(p.done)
	}()
	return p, nil
}

// CloseStdin sends end of input to the command.
func (p *Process) CloseStdin() error {
	return p.stdin.Close()
}

// Done is closed when the command has ended.
func (p *Process) Done() <-chan struct{} {
	return p.done
}

// ExitStatus is the command's exit status once Done is closed. ok is false
// when the command ended without one, as when the connection dropped.
func (p *Process) ExitStatus() (status int, ok bool) {
	var exit *ssh.ExitError
	switch {
	case p.err == nil:
		return 0, true
	case errors.As(p.err, &exit):
		return exit.ExitStatus(), true
	default:
		return -1, false
	}
}

// Close closes the command's channel; the Device's shell decides what
// happens to a command still running.
func (p *Process) Close() error {
	return p.s.Close()
}

func parentDir(p string) string {
	i := strings.LastIndex(strings.TrimSuffix(p, "/"), "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}
