package pairing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/profile"
	"golang.org/x/crypto/ssh"
)

const scanTimeout = time.Minute

// Errors a pairing step can end with, besides device.HostKeyMismatchError.
var (
	ErrBadAddress     = errors.New("pairing: invalid address")
	ErrNotLocal       = errors.New("pairing: not an address on the local network")
	ErrAlreadyPaired  = errors.New("pairing: a device is already paired")
	ErrUnknownDevice  = errors.New("pairing: not a device found by the last search")
	ErrKeyNotAccepted = errors.New("pairing: the device accepts neither ember's key nor a passwordless login")
	ErrVerifyFailed   = errors.New("pairing: key installed but a key-only login failed")
	ErrNotConfirmed   = errors.New("pairing: no device confirmed yet")
)

// Phase is where the user is in the pairing flow.
type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseScanning Phase = "scanning"
	PhaseFound    Phase = "found"
	PhaseLock     Phase = "lock" // key installed, waiting for passwordless login to be switched off
	// PhaseAwaitKey: the user confirmed a Device and is copying ember's key
	// to it over USB; ember tries a key-only login until it works.
	PhaseAwaitKey Phase = "awaiting_key"
	PhaseDone     Phase = "done"
)

// State is a snapshot of the flow for the dashboard.
type State struct {
	Phase      Phase       `json:"phase"`
	Candidates []Candidate `json:"candidates"`
	// Address is the Device being waited for in PhaseAwaitKey.
	Address string `json:"address,omitempty"`
	// Error says why a wait for the key ended without pairing:
	// "key_wait_timeout" or "host_key_mismatch".
	Error string `json:"error,omitempty"`
}

// Why a wait for the key ended.
const (
	KeyWaitTimeout  = "key_wait_timeout"
	KeyWaitMismatch = "host_key_mismatch"
)

// Config wires a Flow.
type Config struct {
	Store *profile.Store
	// Targets lists the "host:port" addresses a LAN search probes. Nil means
	// LocalTargets.
	Targets func(ports []int) ([]string, error)
	// LookupHost resolves a host name typed by the user. Nil means the
	// system resolver.
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// Probe inspects the "host:port" targets of a search. Nil means Find.
	Probe func(ctx context.Context, targets []string) []Candidate
	// KeyPoll is how often a wait for the key tries a key-only login, and
	// KeyWait how long it keeps trying; 2s and 15 minutes when zero.
	KeyPoll, KeyWait time.Duration
}

// Flow runs pairing for the dashboard: search, confirm, lock. It is safe for
// concurrent use.
type Flow struct {
	cfg Config

	mu         sync.Mutex
	state      State
	scanID     int
	cancelScan context.CancelFunc
}

// NewFlow returns an idle Flow.
func NewFlow(cfg Config) *Flow {
	if cfg.Targets == nil {
		cfg.Targets = LocalTargets
	}
	if cfg.LookupHost == nil {
		cfg.LookupHost = net.DefaultResolver.LookupHost
	}
	if cfg.Probe == nil {
		cfg.Probe = Find
	}
	if cfg.KeyPoll == 0 {
		cfg.KeyPoll = 2 * time.Second
	}
	if cfg.KeyWait == 0 {
		cfg.KeyWait = 15 * time.Minute
	}
	return &Flow{cfg: cfg, state: State{Phase: PhaseIdle, Candidates: []Candidate{}}}
}

// State returns the current state.
func (f *Flow) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state
	s.Candidates = append([]Candidate{}, s.Candidates...)
	return s
}

// Profile returns the paired Profile, or profile.ErrNotPaired.
func (f *Flow) Profile() (profile.Profile, error) {
	return f.cfg.Store.Load()
}

// UpdateProfile changes the paired Profile; see profile.Store.Update.
func (f *Flow) UpdateProfile(change func(*profile.Profile) error) (profile.Profile, error) {
	return f.cfg.Store.Update(change)
}

// PublicKey returns ember's public key as an authorized_keys line, creating
// the key pair on first use.
func (f *Flow) PublicKey() (string, error) {
	return f.cfg.Store.PublicKey()
}

// Scan starts a search in the background and returns at once; State reports
// progress. An empty address searches the local network on the default port
// and on port, if given; otherwise only address ("host" or "host:port") is
// probed, and only if it is on the local network (see localAddrs). A new Scan
// cancels the previous one.
func (f *Flow) Scan(address string, port int) error {
	if _, err := f.cfg.Store.Load(); err == nil {
		return ErrAlreadyPaired
	}
	if port != 0 && (port < 1 || port > 65535) {
		return ErrBadAddress
	}
	var targets func() ([]string, error)
	if address != "" {
		hostport, err := normalizeAddress(address, port)
		if err != nil {
			return err
		}
		addrs, err := f.localAddrs(hostport)
		if err != nil {
			return err
		}
		targets = func() ([]string, error) { return addrs, nil }
	} else {
		ports := []int{profile.DefaultPort}
		if port != 0 && port != profile.DefaultPort {
			ports = append(ports, port)
		}
		targets = func() ([]string, error) { return f.cfg.Targets(ports) }
	}

	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	f.mu.Lock()
	if f.cancelScan != nil {
		f.cancelScan()
	}
	f.scanID++
	id := f.scanID
	f.cancelScan = cancel
	f.state = State{Phase: PhaseScanning, Candidates: []Candidate{}}
	f.mu.Unlock()

	go func() {
		defer cancel()
		var found []Candidate
		if list, err := targets(); err == nil {
			found = f.cfg.Probe(ctx, list)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if id != f.scanID {
			return
		}
		if found == nil {
			found = []Candidate{}
		}
		f.state = State{Phase: PhaseFound, Candidates: found}
	}()
	return nil
}

var hostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252})$`)

func normalizeAddress(address string, port int) (string, error) {
	host, p, err := net.SplitHostPort(address)
	if err != nil {
		host, p = address, ""
	}
	n := profile.DefaultPort
	if port != 0 {
		n = port
	}
	if p != "" {
		if n, err = strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", ErrBadAddress
		}
	}
	if net.ParseIP(host) == nil && !hostname.MatchString(host) {
		return "", ErrBadAddress
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}

// lookupWait bounds resolving a host name the user typed.
const lookupWait = 5 * time.Second

// localAddrs turns a "host:port" the user typed into the addresses to probe,
// refusing any that is not on the local network: a private (RFC 1918 or
// fc00::/7), link-local or loopback address. A host name is resolved here,
// once, and must resolve only to such addresses; the probe then dials those
// addresses, so a second lookup cannot point it elsewhere.
func (f *Flow) localAddrs(hostport string) ([]string, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil, ErrBadAddress
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isLocal(ip) {
			return nil, fmt.Errorf("%w: %s", ErrNotLocal, host)
		}
		return []string{hostport}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), lookupWait)
	defer cancel()
	names, err := f.cfg.LookupHost(ctx, host)
	if err != nil || len(names) == 0 {
		return nil, fmt.Errorf("%w: %s does not resolve", ErrBadAddress, host)
	}
	var out []string
	for _, n := range names {
		ip, err := netip.ParseAddr(n)
		if err != nil || !isLocal(ip) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrNotLocal, host, n)
		}
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	return out, nil
}

// isLocal also takes IPv4-mapped IPv6 addresses by their IPv4 meaning,
// as netip's predicates do.
func isLocal(ip netip.Addr) bool {
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback()
}

// Confirm pairs with the candidate the user picked, identified by address and
// the fingerprint the user saw. It is the first step that writes anything to
// the Device: it surveys it, installs ember's key if the Device does not
// already accept it, proves a key-only login, and saves the Profile.
func (f *Flow) Confirm(ctx context.Context, address, fingerprint string) (profile.Profile, error) {
	if _, err := f.cfg.Store.Load(); err == nil {
		return profile.Profile{}, ErrAlreadyPaired
	}
	c, ok := f.candidate(address, fingerprint)
	if !ok {
		return profile.Profile{}, ErrUnknownDevice
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.HostKey))
	if err != nil {
		return profile.Profile{}, ErrUnknownDevice
	}
	signer, err := f.cfg.Store.Signer()
	if err != nil {
		return profile.Profile{}, err
	}
	host, portStr, _ := net.SplitHostPort(c.Addr)
	port, _ := strconv.Atoi(portStr)
	p := profile.New(host, port)
	p.HostKey = c.HostKey

	keyLogin := device.Config{Addr: c.Addr, User: p.User, HostKey: hostKey, Signer: signer, Timeout: handshakeWait}
	d, err := device.Dial(ctx, keyLogin)
	if errors.Is(err, device.ErrAuthRejected) {
		p.Survey, err = installOverPasswordless(ctx, keyLogin, p.LibraryRoot)
		if err != nil {
			return profile.Profile{}, err
		}
		d, err = device.Dial(ctx, keyLogin)
		if errors.Is(err, device.ErrAuthRejected) {
			return profile.Profile{}, ErrVerifyFailed
		}
		if err != nil {
			return profile.Profile{}, err
		}
		d.Close()
	} else if err != nil {
		return profile.Profile{}, err
	} else {
		p.Survey, err = Survey(d, p.LibraryRoot)
		d.Close()
		if err != nil {
			return profile.Profile{}, err
		}
	}

	p.PairedAt = time.Now().UTC().Truncate(time.Second)
	if err := f.cfg.Store.Save(p); err != nil {
		return profile.Profile{}, err
	}
	f.mu.Lock()
	if f.cancelScan != nil {
		f.cancelScan()
	}
	f.scanID++
	f.state = State{Phase: PhaseLock, Candidates: []Candidate{}}
	f.mu.Unlock()
	return p, nil
}

// ConfirmKeyOnly pairs with the candidate the user picked, like Confirm, but
// for a user who copies ember's key over USB and never opens the
// passwordless window. It returns at once; in the background ember tries a
// key-only login to that address alone, every KeyPoll, until the Device
// accepts the key. Then it surveys the Device over that login, saves the
// Profile and moves to PhaseDone. A new Scan or Forget stops the wait; after
// KeyWait, or if another host answers at the address, the flow goes back to
// PhaseFound with Error set. No password is ever sent.
func (f *Flow) ConfirmKeyOnly(address, fingerprint string) error {
	if _, err := f.cfg.Store.Load(); err == nil {
		return ErrAlreadyPaired
	}
	c, ok := f.candidate(address, fingerprint)
	if !ok {
		return ErrUnknownDevice
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.HostKey))
	if err != nil {
		return ErrUnknownDevice
	}
	signer, err := f.cfg.Store.Signer()
	if err != nil {
		return err
	}
	host, portStr, _ := net.SplitHostPort(c.Addr)
	port, _ := strconv.Atoi(portStr)
	p := profile.New(host, port)
	p.HostKey = c.HostKey
	keyLogin := device.Config{Addr: c.Addr, User: p.User, HostKey: hostKey, Signer: signer, Timeout: handshakeWait}

	ctx, cancel := context.WithTimeout(context.Background(), f.cfg.KeyWait)
	f.mu.Lock()
	if f.cancelScan != nil {
		f.cancelScan()
	}
	f.scanID++
	id := f.scanID
	f.cancelScan = cancel
	candidates := f.state.Candidates
	f.state = State{Phase: PhaseAwaitKey, Candidates: candidates, Address: c.Addr}
	f.mu.Unlock()

	go func() {
		defer cancel()
		var failure string
		for {
			d, err := device.Dial(ctx, keyLogin)
			if err == nil {
				err = f.finishKeyOnly(ctx, d, p, id)
				if err == nil {
					return
				}
			}
			var mismatch *device.HostKeyMismatchError
			if errors.As(err, &mismatch) {
				failure = KeyWaitMismatch
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(f.cfg.KeyPoll):
				continue
			}
			failure = KeyWaitTimeout
			break
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if id == f.scanID {
			f.state = State{Phase: PhaseFound, Candidates: candidates, Error: failure}
		}
	}()
	return nil
}

// finishKeyOnly surveys the Device over the key login that just worked,
// saves the Profile and ends the flow, unless the wait was called off
// meanwhile.
func (f *Flow) finishKeyOnly(ctx context.Context, d *device.Device, p profile.Profile, id int) error {
	s, err := Survey(d, p.LibraryRoot)
	d.Close()
	if err != nil {
		return err
	}
	p.Survey = s
	p.PairedAt = time.Now().UTC().Truncate(time.Second)
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.scanID || ctx.Err() != nil {
		return nil
	}
	if err := f.cfg.Store.Save(p); err != nil {
		return err
	}
	f.scanID++
	f.cancelScan = nil
	f.state = State{Phase: PhaseDone, Candidates: []Candidate{}}
	return nil
}

func installOverPasswordless(ctx context.Context, keyLogin device.Config, libraryRoot string) (profile.Survey, error) {
	open := keyLogin
	open.Signer = nil
	d, err := device.Dial(ctx, open)
	if errors.Is(err, device.ErrAuthRejected) {
		return profile.Survey{}, ErrKeyNotAccepted
	}
	if err != nil {
		return profile.Survey{}, err
	}
	defer d.Close()
	s, err := Survey(d, libraryRoot)
	if err != nil {
		return s, err
	}
	_, err = InstallKey(d, keyLogin.Signer.PublicKey(), "ember")
	return s, err
}

func (f *Flow) candidate(address, fingerprint string) (Candidate, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.state.Candidates {
		if c.Addr == address && c.Fingerprint == fingerprint && c.HostKey != "" {
			return c, true
		}
	}
	return Candidate{}, false
}

// CheckLock probes the paired Device once and reports whether passwordless
// login is now refused while ember's key still works. When it is, the flow
// moves to PhaseDone.
func (f *Flow) CheckLock(ctx context.Context) (bool, error) {
	p, err := f.cfg.Store.Load()
	if errors.Is(err, profile.ErrNotPaired) {
		return false, ErrNotConfirmed
	}
	if err != nil {
		return false, err
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p.HostKey))
	if err != nil {
		return false, err
	}
	signer, err := f.cfg.Store.Signer()
	if err != nil {
		return false, err
	}
	cfg := device.Config{Addr: p.HostPort(), User: p.User, HostKey: hostKey, Timeout: handshakeWait}

	d, err := device.Dial(ctx, cfg)
	if err == nil {
		d.Close()
		return false, nil
	}
	if !errors.Is(err, device.ErrAuthRejected) {
		return false, err
	}
	cfg.Signer = signer
	d, err = device.Dial(ctx, cfg)
	if err != nil {
		return false, err
	}
	d.Close()

	f.mu.Lock()
	f.state = State{Phase: PhaseDone, Candidates: []Candidate{}}
	f.mu.Unlock()
	return true, nil
}

// Forget unpairs: it stops any search, deletes the Profile and ember's key,
// and leaves the flow ready to pair from the start.
func (f *Flow) Forget() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancelScan != nil {
		f.cancelScan()
	}
	f.scanID++
	f.state = State{Phase: PhaseIdle, Candidates: []Candidate{}}
	return f.cfg.Store.Forget()
}
