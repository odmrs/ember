// Package connector keeps ember linked to the paired Device while at least one
// dashboard tab (a Session) is open: it dials every two seconds until the
// Device answers, finds it again on the local network if its address
// changed, and goes back to searching when the link drops.
package connector

import (
	"context"
	"errors"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/pairing"
	"github.com/odmrs/ember/internal/profile"
	"golang.org/x/crypto/ssh"
)

// Timings of the loop. RetryInterval is measured from the start of one
// attempt to the start of the next, without backoff.
const (
	RetryInterval = 2 * time.Second
	// AttemptsPerRound is how many attempts a round makes before the
	// Connector stops and waits for Retry.
	AttemptsPerRound = 5
	// KeepaliveInterval is how often a linked Device must answer a ping, and
	// how long it gets to answer.
	KeepaliveInterval = 5 * time.Second
	dialTimeout       = 5 * time.Second
	scanTimeout       = 30 * time.Second
	scanEveryAttempts = 8 // with RetryInterval, a network sweep at most every ~16s
)

// ErrNotLinked is returned by Device while no link is up.
var ErrNotLinked = errors.New("connector: device not linked")

// Phase is what the Connector is doing.
type Phase string

const (
	PhaseIdle      Phase = "idle"      // no Session open
	PhaseUnpaired  Phase = "unpaired"  // nothing to connect to yet
	PhaseSearching Phase = "searching" // dialing every RetryInterval
	PhaseWaiting   Phase = "waiting"   // a round of attempts failed; waiting for Retry
	PhaseLinked    Phase = "linked"
)

// Failure is why the last attempt at the saved address failed.
type Failure string

const (
	FailUnreachable     Failure = "unreachable"
	FailPortClosed      Failure = "port_closed"
	FailHostKeyMismatch Failure = "host_key_mismatch"
	FailAuthRejected    Failure = "auth_rejected"
)

// State is a snapshot for the dashboard.
type State struct {
	Phase Phase `json:"phase"`
	// Attempt counts dials of the saved address since the last link.
	Attempt int `json:"attempt,omitempty"`
	// Address is the "host:port" being tried, or the linked one.
	Address string  `json:"address,omitempty"`
	Failure Failure `json:"failure,omitempty"`
	// Scanning is set while the local network is searched for the Device.
	Scanning bool `json:"scanning,omitempty"`
	// KeepAwake is set while linked.
	KeepAwake Awake `json:"keepAwake,omitempty"`
}

// Config wires a Connector. Only Store is required.
type Config struct {
	Store *profile.Store
	// Dial opens a link; device.Dial when nil.
	Dial func(ctx context.Context, cfg device.Config) (*device.Device, error)
	// Targets lists the "host:port" addresses a network search probes;
	// pairing.LocalTargets when nil.
	Targets func(ports []int) ([]string, error)
	// After is the clock the loop waits on; time.After when nil.
	After func(time.Duration) <-chan time.Time
	// Attempts is the number of attempts per round; AttemptsPerRound when 0.
	Attempts int
	// Listening reports whether the Device still accepts new connections on
	// addr; a TCP connect when nil.
	Listening func(ctx context.Context, addr string) bool
}

// Connector is safe for concurrent use.
type Connector struct {
	cfg Config

	mu     sync.Mutex
	state  State
	dev    *device.Device
	subs   map[chan State]struct{}
	cancel context.CancelFunc // stops the running loop
	done   chan struct{}      // closed when the last loop started has exited
	closed bool
	// awakeFailed is set once the keep-awake command has failed on its own;
	// it is not tried again until keep-awake is switched off.
	awakeFailed bool
	onLink      []func(*device.Device)

	reload chan struct{}
	retry  chan struct{}
}

// New returns an idle Connector. Nothing is dialed until a Session opens.
func New(cfg Config) *Connector {
	if cfg.Dial == nil {
		cfg.Dial = device.Dial
	}
	if cfg.Targets == nil {
		cfg.Targets = pairing.LocalTargets
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.Attempts == 0 {
		cfg.Attempts = AttemptsPerRound
	}
	if cfg.Listening == nil {
		cfg.Listening = listening
	}
	return &Connector{cfg: cfg, state: State{Phase: PhaseIdle}, subs: map[chan State]struct{}{}, reload: make(chan struct{}, 1), retry: make(chan struct{}, 1)}
}

// State returns the current state.
func (c *Connector) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Device returns the linked Device, or ErrNotLinked. The Connector owns it:
// callers must not close it, and their calls fail with an error if the link
// drops underneath them.
func (c *Connector) Device() (*device.Device, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dev == nil {
		return nil, ErrNotLinked
	}
	return c.dev, nil
}

// Session registers an open dashboard tab. The channel delivers the current
// state at once and then every change; a reader that falls behind gets only
// the latest one. The loop runs while at least one Session is open. Calling
// end closes the Session; the channel is closed if the Connector is.
func (c *Connector) Session() (states <-chan State, end func()) {
	ch := make(chan State, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		close(ch)
		return ch, func() {}
	}
	ch <- c.state
	c.subs[ch] = struct{}{}
	if len(c.subs) == 1 {
		c.start()
	}
	var once sync.Once
	return ch, func() { once.Do(func() { c.leave(ch) }) }
}

func (c *Connector) leave(ch chan State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.subs[ch]; !ok {
		return
	}
	delete(c.subs, ch)
	if len(c.subs) == 0 {
		c.cancel()
	}
}

// start launches the loop; c.mu must be held. A loop still winding down from
// the previous Session finishes first, so two loops never overlap.
func (c *Connector) start() {
	ctx, cancel := context.WithCancel(context.Background())
	prev, done := c.done, make(chan struct{})
	c.cancel, c.done = cancel, done
	go func() {
		defer close(done)
		if prev != nil {
			<-prev
		}
		c.run(ctx)
		c.publish(State{Phase: PhaseIdle}, nil)
	}()
}

// Close stops the loop, closes the link and ends every Session.
func (c *Connector) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if c.cancel != nil {
		c.cancel()
	}
	for ch := range c.subs {
		close(ch)
	}
	c.subs = map[chan State]struct{}{}
	done := c.done
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Reload makes the Connector act on a changed Profile at once instead of at
// the next attempt: a link whose login settings changed, or to a Device that
// is no longer paired, is dropped and dialed again, and keep-awake follows
// its setting.
func (c *Connector) Reload() {
	select {
	case c.reload <- struct{}{}:
	default:
	}
}

// Retry starts a new round of attempts after one ended in PhaseWaiting.
func (c *Connector) Retry() {
	select {
	case c.retry <- struct{}{}:
	default:
	}
}

// OnLink registers f to be called with every new link, right after it is
// published. f runs on the Connector's loop, so it must not block.
func (c *Connector) OnLink(f func(*device.Device)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onLink = append(c.onLink, f)
}

// publish sets the state and the linked Device together and tells every
// Session about a change.
func (c *Connector) publish(s State, dev *device.Device) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dev = dev
	c.set(s)
}

// setAwake changes the keep-awake part of the state, if dev is still the
// linked Device.
func (c *Connector) setAwake(dev *device.Device, a Awake) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dev != dev {
		return
	}
	s := c.state
	s.KeepAwake = a
	c.set(s)
}

// set changes the state; c.mu must be held.
func (c *Connector) set(s State) {
	if s == c.state {
		return
	}
	c.state = s
	for ch := range c.subs {
		select {
		case <-ch:
		default:
		}
		ch <- s
	}
}

// found is a network search's result: a linked Device at a new address, or
// nothing.
type found struct {
	dev  *device.Device
	addr string
}

// search is a network search in progress, with the login it dials with.
type search struct {
	results chan found
	cancel  context.CancelFunc
	login   scanLogin
}

func (c *Connector) startSearch(ctx context.Context, cfg device.Config, p profile.Profile) *search {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	s := &search{results: make(chan found, 1), cancel: cancel, login: scanLoginOf(p)}
	go c.scan(ctx, cfg, p.Port, s.results)
	return s
}

// abandon stops the search and closes whatever it may still find.
func (s *search) abandon() {
	s.cancel()
	go func() {
		if f := <-s.results; f.dev != nil {
			f.dev.Close()
		}
	}()
}

func (c *Connector) run(ctx context.Context) {
	var scan *search // non-nil while the network is being searched
	stopScan := func() {
		if scan != nil {
			scan.abandon()
			scan = nil
		}
	}
	defer stopScan()
	results := func() <-chan found {
		if scan == nil {
			return nil
		}
		return scan.results
	}

	attempt, lastScan := 0, 0
	var failure Failure
	var profileErr string
	for ctx.Err() == nil {
		// Measured from the start of the attempt; a link that drops at once
		// is retried on the same cadence, never in a tight loop.
		next := c.cfg.After(RetryInterval)
		p, cfg, err := c.target()
		if err != nil {
			if !errors.Is(err, profile.ErrNotPaired) && err.Error() != profileErr {
				log.Printf("connector: %v", err)
			}
			profileErr = err.Error()
			stopScan()
			attempt, lastScan, failure = 0, 0, ""
			c.publish(State{Phase: PhaseUnpaired}, nil)
		} else {
			profileErr = ""
			attempt++
			c.publish(State{Phase: PhaseSearching, Attempt: attempt, Address: cfg.Addr, Failure: failure, Scanning: scan != nil}, nil)
			d, err := c.cfg.Dial(ctx, cfg)
			switch {
			case err == nil:
				stopScan()
				c.link(ctx, d, cfg.Addr, p)
				attempt, lastScan, failure = 0, 0, ""
			case ctx.Err() != nil:
				return
			default:
				failure = classify(err)
				// A refused login means the Device is right there; searching
				// elsewhere cannot help.
				// The first failure searches at once; after that the saved
				// address keeps its 2s cadence while full sweeps of the network
				// are spaced out so the LAN is not flooded.
				if scan == nil && failure != FailAuthRejected && (lastScan == 0 || attempt-lastScan >= scanEveryAttempts) {
					scan = c.startSearch(ctx, cfg, p)
					lastScan = attempt
				}
				c.publish(State{Phase: PhaseSearching, Attempt: attempt, Address: cfg.Addr, Failure: failure, Scanning: scan != nil}, nil)
			}
		}

	wait:
		for {
			select {
			case <-ctx.Done():
				return
			case <-next:
				// A round is a few attempts, not an endless loop: the user
				// may simply not have started the SSH server yet, and asks
				// for the next round once they have. A network search
				// already under way can still finish and link.
				if attempt >= c.cfg.Attempts {
					next = nil
					c.publish(State{Phase: PhaseWaiting, Attempt: attempt, Address: cfg.Addr, Failure: failure, Scanning: scan != nil}, nil)
					continue
				}
				break wait
			case <-c.retry:
				attempt, lastScan, failure = 0, 0, ""
				break wait
			case <-c.reload:
				// The search dials with the old login; the next attempt
				// starts one with the new.
				stopScan()
				if next == nil {
					attempt, lastScan, failure = 0, 0, ""
				}
				break wait
			case f := <-results():
				made := scan.login
				scan.cancel()
				scan = nil
				if f.dev == nil {
					c.publish(State{Phase: roundPhase(next), Attempt: attempt, Address: cfg.Addr, Failure: failure}, nil)
					continue
				}
				now, ok := c.moveTo(f.addr, made)
				if !ok {
					f.dev.Close()
					c.publish(State{Phase: roundPhase(next), Attempt: attempt, Address: cfg.Addr, Failure: failure}, nil)
					continue
				}
				c.link(ctx, f.dev, f.addr, now)
				attempt, lastScan, failure = 0, 0, ""
			}
		}
	}
}

// roundPhase is the phase to report between attempts: waiting once the round
// is over (no next attempt scheduled), searching otherwise.
func roundPhase(next <-chan time.Time) Phase {
	if next == nil {
		return PhaseWaiting
	}
	return PhaseSearching
}

// target reads the Profile on every attempt, so a new pairing, address or
// passwordless setting applies without a restart.
func (c *Connector) target() (profile.Profile, device.Config, error) {
	p, err := c.cfg.Store.Load()
	if err != nil {
		return p, device.Config{}, err
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p.HostKey))
	if err != nil {
		return p, device.Config{}, err
	}
	cfg := device.Config{Addr: p.HostPort(), User: p.User, HostKey: hostKey, Timeout: dialTimeout}
	if !p.Passwordless {
		if cfg.Signer, err = c.cfg.Store.Signer(); err != nil {
			return p, device.Config{}, err
		}
	}
	return p, cfg, nil
}

// scan looks for the Device on the local network: every host answering like
// KOReader's SSH server is dialed with the pinned host key, so any other
// device is dropped at the handshake, before ember logs in.
func (c *Connector) scan(ctx context.Context, cfg device.Config, port int, out chan<- found) {
	targets, err := c.cfg.Targets([]int{port})
	if err != nil {
		log.Printf("connector: listing local networks: %v", err)
		out <- found{}
		return
	}
	for _, h := range pairing.FindDropbear(ctx, targets) {
		at := cfg
		at.Addr = h.Addr
		if d, err := c.cfg.Dial(ctx, at); err == nil {
			out <- found{dev: d, addr: h.Addr}
			return
		}
	}
	out <- found{}
}

// scanLogin is what a network search dials with, apart from the address
// it is looking for.
type scanLogin struct {
	user, hostKey string
	port          int
	passwordless  bool
}

func scanLoginOf(p profile.Profile) scanLogin {
	return scanLogin{p.User, p.HostKey, p.Port, p.Passwordless}
}

// moveTo saves the new address a search found the Device at and returns the
// Profile with it. If the Profile's login changed while the search ran, the
// link it made is not the one the user asked for: nothing is saved and ok is
// false, so a port the user has since changed is never written back.
func (c *Connector) moveTo(addr string, made scanLogin) (p profile.Profile, ok bool) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return p, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return p, false
	}
	errChanged := errors.New("connector: login changed during the search")
	saved, err := c.cfg.Store.Update(func(p *profile.Profile) error {
		if scanLoginOf(*p) != made {
			return errChanged
		}
		p.Address, p.Port = host, port
		return nil
	})
	switch {
	case errors.Is(err, errChanged):
		return p, false
	case err != nil:
		log.Printf("connector: saving the new address %s: %v", addr, err)
		cur, lerr := c.cfg.Store.Load()
		if lerr != nil || scanLoginOf(cur) != made {
			return p, false
		}
		cur.Address, cur.Port = host, port
		return cur, true
	}
	return saved, true
}

// login is what a link was made with. A Profile with a different one needs a
// new link.
type login struct {
	addr, user, hostKey string
	passwordless        bool
}

func loginOf(p profile.Profile) login {
	return login{p.HostPort(), p.User, p.HostKey, p.Passwordless}
}

// link holds the link made with Profile p until it drops, ctx ends or a
// reload finds the login changed, then closes it. A Device that leaves Wi-Fi
// does not close the TCP connection, so a ping every KeepaliveInterval must
// be answered within the next one. Keep-awake runs while the link is held
// and is released before it closes.
func (c *Connector) link(ctx context.Context, d *device.Device, addr string, p profile.Profile) {
	made := loginOf(p)
	k := &keeper{c: c, d: d}
	c.publish(State{Phase: PhaseLinked, Address: addr, KeepAwake: k.set(p.KeepAwake)}, d)
	c.mu.Lock()
	hooks := c.onLink
	c.mu.Unlock()
	for _, f := range hooks {
		f(d)
	}
	defer d.Close()
	defer c.publish(State{Phase: PhaseSearching, Address: addr}, nil)
	defer k.stop()

	gone := make(chan struct{})
	go func() {
		d.Wait()
		close(gone)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-gone:
			return
		case <-c.reload:
			p, err := c.cfg.Store.Load()
			if err != nil || loginOf(p) != made {
				return
			}
			c.setAwake(d, k.set(p.KeepAwake))
		case <-k.done():
			c.setAwake(d, k.ended())
		case <-c.cfg.After(KeepaliveInterval):
			replied := make(chan error, 1)
			go func() { replied <- d.Ping() }()
			select {
			case err := <-replied:
				if err != nil {
					return
				}
				// Stopping the SSH server on the Kindle leaves established
				// connections running, so a live link alone would ignore the
				// user switching it off. A closed listener ends the link.
				if !c.cfg.Listening(ctx, addr) {
					return
				}
			case <-gone:
				return
			case <-ctx.Done():
				return
			case <-c.cfg.After(KeepaliveInterval):
				return
			}
		}
	}
}

// listening reports false only when the Device refuses the connection. A
// timeout is left to the keepalive to judge, so a Wi-Fi hiccup does not end a
// working link.
func listening(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return !refused(err)
	}
	conn.Close()
	return true
}

func classify(err error) Failure {
	var mismatch *device.HostKeyMismatchError
	switch {
	case errors.As(err, &mismatch):
		return FailHostKeyMismatch
	case errors.Is(err, device.ErrAuthRejected):
		return FailAuthRejected
	case refused(err):
		return FailPortClosed
	default:
		return FailUnreachable
	}
}
