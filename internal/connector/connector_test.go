package connector

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/profile"
)

// fast runs every wait 50 times quicker: a 2s retry takes 40ms.
func fast(d time.Duration) <-chan time.Time { return time.After(d / 50) }

type rig struct {
	t     *testing.T
	store *profile.Store
	dials atomic.Int32
	scans atomic.Int32
}

// newRig returns a Profile paired with fake, saved with address addr. ember's
// key is installed on every fake in authorized, so only the host key tells
// them apart.
func newRig(t *testing.T, fake *fakedevice.Device, addr string, authorized ...*fakedevice.Device) *rig {
	t.Helper()
	store, err := profile.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pub, err := store.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range authorized {
		f.WriteFile(t, fakedevice.AuthorizedKeys, pub+"\n")
	}
	host, port := splitAddr(t, addr)
	p := profile.New(host, port)
	p.HostKey = profile.AuthorizedKey(fake.HostKey())
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, store: store}
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := net.LookupPort("tcp", port)
	if err != nil {
		t.Fatal(err)
	}
	return host, n
}

func (r *rig) connector(targets []string, after func(time.Duration) <-chan time.Time) *Connector {
	c := New(Config{
		Store: r.store,
		Dial: func(ctx context.Context, cfg device.Config) (*device.Device, error) {
			r.dials.Add(1)
			return device.Dial(ctx, cfg)
		},
		Targets: func([]int) ([]string, error) {
			r.scans.Add(1)
			return targets, nil
		},
		After: after,
	})
	r.t.Cleanup(c.Close)
	return c
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

// session opens a Session that ends with the test.
func session(t *testing.T, c *Connector) <-chan State {
	states, end := c.Session()
	t.Cleanup(end)
	return states
}

func waitFor(t *testing.T, states <-chan State, what string, ok func(State) bool) State {
	t.Helper()
	timeout := time.After(10 * time.Second)
	var last State
	for {
		select {
		case s, open := <-states:
			if !open {
				t.Fatalf("waiting for %s: stream closed (last %+v)", what, last)
			}
			last = s
			if ok(s) {
				return s
			}
		case <-timeout:
			t.Fatalf("waiting for %s: last state %+v", what, last)
		}
	}
}

func linkedAt(addr string) func(State) bool {
	return func(s State) bool { return s.Phase == PhaseLinked && s.Address == addr }
}

func TestLinksToThePairedDevice(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)

	waitFor(t, session(t, c), "link", linkedAt(fake.Addr()))
	d, err := c.Device()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Stat(fakedevice.Documents); err != nil {
		t.Fatalf("linked Device unusable: %v", err)
	}
	if r.scans.Load() != 0 {
		t.Error("scanned although the saved address worked")
	}
}

func TestDropThenRelink(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "link", linkedAt(fake.Addr()))

	fake.Stop()
	s := waitFor(t, states, "searching after drop", func(s State) bool { return s.Phase == PhaseSearching && s.Failure != "" })
	if s.Failure != FailPortClosed || s.Address != fake.Addr() {
		t.Fatalf("state = %+v, want port_closed at %s", s, fake.Addr())
	}
	if _, err := c.Device(); err != ErrNotLinked {
		t.Fatalf("Device() while searching: %v", err)
	}

	fake.Start()
	waitFor(t, states, "relink", linkedAt(fake.Addr()))
}

// A Device that drops off Wi-Fi leaves the TCP connection open; only the
// keepalive notices.
func TestSilentLinkIsDropped(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	waitFor(t, session(t, c), "link", linkedAt(fake.Addr()))
	first, _ := c.Device()

	fake.SetUnresponsive(true)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if d, _ := c.Device(); d != nil && d != first {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("silent link was never replaced")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := first.Ping(); err == nil {
		t.Fatal("the silent connection was left open")
	}
}

func TestScanFindsTheDeviceAtANewAddress(t *testing.T) {
	fake := fakedevice.New(t)
	impostor := fakedevice.New(t, fakedevice.WithHostKeySeed(9), fakedevice.Passwordless())
	r := newRig(t, fake, closedPort(t), fake, impostor)
	c := r.connector([]string{closedPort(t), impostor.Addr(), fake.Addr()}, fast)

	waitFor(t, session(t, c), "link at the new address", linkedAt(fake.Addr()))
	p, err := r.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.HostPort() != fake.Addr() {
		t.Fatalf("saved address = %s, want %s", p.HostPort(), fake.Addr())
	}
	if p.HostKey != profile.AuthorizedKey(fake.HostKey()) {
		t.Fatal("pinned host key changed")
	}
}

func TestScanIgnoresAnImpostor(t *testing.T) {
	fake := fakedevice.New(t)
	impostor := fakedevice.New(t, fakedevice.WithHostKeySeed(9), fakedevice.Passwordless())
	saved := closedPort(t)
	r := newRig(t, fake, saved, impostor)
	c := r.connector([]string{impostor.Addr()}, fast)

	states := session(t, c)
	waitFor(t, states, "several scans", func(State) bool { return r.scans.Load() >= 3 })
	s := waitFor(t, states, "next attempt", func(s State) bool { return s.Phase == PhaseSearching })
	if s.Failure != FailPortClosed || s.Address != saved {
		t.Fatalf("state = %+v", s)
	}
	if _, err := c.Device(); err != ErrNotLinked {
		t.Fatalf("linked to the impostor: %v", err)
	}
	if p, _ := r.store.Load(); p.HostPort() != saved {
		t.Fatalf("saved address moved to %s", p.HostPort())
	}
}

func TestNetworkSweepsAreSpacedOut(t *testing.T) {
	fake := fakedevice.New(t)
	saved := closedPort(t)
	r := newRig(t, fake, saved)
	c := r.connector(nil, fast)

	states := session(t, c)
	waitFor(t, states, "17 attempts", func(State) bool { return r.dials.Load() >= 17 })
	if n := r.scans.Load(); n < 2 || n > 3 {
		t.Fatalf("%d sweeps over 17 attempts, want one every %d attempts", n, scanEveryAttempts)
	}
}

func TestHostKeyMismatchIsReported(t *testing.T) {
	fake := fakedevice.New(t)
	impostor := fakedevice.New(t, fakedevice.WithHostKeySeed(9), fakedevice.Passwordless())
	r := newRig(t, fake, impostor.Addr(), impostor)
	c := r.connector(nil, fast)

	s := waitFor(t, session(t, c), "mismatch", func(s State) bool { return s.Failure != "" })
	if s.Phase != PhaseSearching || s.Failure != FailHostKeyMismatch || s.Address != impostor.Addr() {
		t.Fatalf("state = %+v, want host_key_mismatch", s)
	}
}

func TestAuthRejectedIsReportedWithoutScanning(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr()) // ember's key not installed
	c := r.connector([]string{fake.Addr()}, fast)

	states := session(t, c)
	s := waitFor(t, states, "auth failure", func(s State) bool { return s.Failure != "" })
	if s.Failure != FailAuthRejected {
		t.Fatalf("state = %+v, want auth_rejected", s)
	}
	waitFor(t, states, "a few more attempts", func(s State) bool { return s.Attempt >= 3 })
	if n := r.scans.Load(); n != 0 {
		t.Fatalf("scanned %d times; the Device was found, it refused the login", n)
	}
}

func TestPasswordlessModeLogsInWithoutTheKey(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	r := newRig(t, fake, fake.Addr())
	p, _ := r.store.Load()
	p.Passwordless = true
	if err := r.store.Save(p); err != nil {
		t.Fatal(err)
	}
	c := r.connector(nil, fast)
	waitFor(t, session(t, c), "link", linkedAt(fake.Addr()))
}

func TestNoDialingWithoutASession(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)

	time.Sleep(200 * time.Millisecond) // five retry periods
	if n := r.dials.Load(); n != 0 {
		t.Fatalf("dialed %d times with no Session", n)
	}

	states, end := c.Session()
	waitFor(t, states, "link", linkedAt(fake.Addr()))
	d, _ := c.Device()
	end()

	deadline := time.Now().Add(5 * time.Second)
	for d.Ping() == nil || c.State().Phase != PhaseIdle {
		if time.Now().After(deadline) {
			t.Fatalf("connection still open after the last Session ended (phase %s)", c.State().Phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
	n := r.dials.Load()
	time.Sleep(200 * time.Millisecond)
	if got := r.dials.Load(); got != n {
		t.Fatalf("kept dialing after the last Session ended: %d -> %d", n, got)
	}
}

func TestLoopRunsWhileAnySessionIsOpen(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)

	a, endA := c.Session()
	b, endB := c.Session()
	waitFor(t, a, "link", linkedAt(fake.Addr()))
	endA()
	waitFor(t, b, "still linked", linkedAt(fake.Addr()))
	if _, err := c.Device(); err != nil {
		t.Fatalf("one tab closing dropped the link for the other: %v", err)
	}
	endB()
}

func TestUnpairedDoesNotDial(t *testing.T) {
	store, err := profile.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, store: store}
	c := r.connector(nil, fast)
	waitFor(t, session(t, c), "unpaired", func(s State) bool { return s.Phase == PhaseUnpaired })
	time.Sleep(100 * time.Millisecond)
	if r.dials.Load() != 0 || r.scans.Load() != 0 {
		t.Fatal("dialed or scanned without a paired Device")
	}
}

// manualClock fires timers only when the test says so.
type manualClock struct {
	mu      sync.Mutex
	pending map[time.Duration][]chan time.Time
}

func (m *manualClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = map[time.Duration][]chan time.Time{}
	}
	m.pending[d] = append(m.pending[d], ch)
	return ch
}

// fire waits until a timer of d is pending and fires every such timer.
func (m *manualClock) fire(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		chans := m.pending[d]
		delete(m.pending, d)
		m.mu.Unlock()
		if len(chans) > 0 {
			for _, ch := range chans {
				ch <- time.Now()
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s timer pending", d)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRetriesEveryTwoSecondsWithoutBackoff(t *testing.T) {
	fake := fakedevice.New(t)
	saved := closedPort(t)
	r := newRig(t, fake, saved, fake)
	clock := &manualClock{}
	c := r.connector(nil, clock.After)

	states := session(t, c)
	for attempt := 1; attempt <= 4; attempt++ {
		s := waitFor(t, states, "attempt", func(s State) bool { return s.Attempt == attempt && s.Failure != "" })
		if s.Failure != FailPortClosed || s.Address != saved {
			t.Fatalf("attempt %d: %+v", attempt, s)
		}
		if n := r.dials.Load(); n != int32(attempt) {
			t.Fatalf("attempt %d: %d dials of the saved address", attempt, n)
		}
		clock.fire(t, RetryInterval)
	}
}

// A link that drops right away is retried on the 2s cadence, not at once.
func TestRedialAfterADropWaitsForTheNextTick(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	clock := &manualClock{}
	c := r.connector(nil, clock.After)
	states := session(t, c)
	waitFor(t, states, "link", linkedAt(fake.Addr()))

	fake.Stop()
	fake.Start()
	waitFor(t, states, "drop", func(s State) bool { return s.Phase == PhaseSearching })
	time.Sleep(50 * time.Millisecond)
	if n := r.dials.Load(); n != 1 {
		t.Fatalf("redialed %d times before the retry period", n-1)
	}
	clock.fire(t, RetryInterval)
	waitFor(t, states, "relink", linkedAt(fake.Addr()))
}
