package connector

import (
	"testing"
	"time"

	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/profile"
)

func disconnected(s State) bool {
	return s.Phase == PhaseWaiting && s.Reason == ReasonDisconnected
}

func TestStopServerCommand(t *testing.T) {
	for port, want := range map[int]string{
		2222: `nohup sh -c 'sleep 1; pid=$(cat /tmp/dropbear_koreader.pid); kill -TERM "$pid"; sleep 1; ` +
			`iptables -D INPUT -p tcp --dport 2222 -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT; ` +
			`iptables -D OUTPUT -p tcp --sport 2222 -m conntrack --ctstate ESTABLISHED -j ACCEPT; ` +
			`[ -d "/proc/$pid" ] || rm -f /tmp/dropbear_koreader.pid' >/dev/null 2>&1 &`,
		22: `nohup sh -c 'sleep 1; pid=$(cat /tmp/dropbear_koreader.pid); kill -TERM "$pid"; sleep 1; ` +
			`iptables -D INPUT -p tcp --dport 22 -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT; ` +
			`iptables -D OUTPUT -p tcp --sport 22 -m conntrack --ctstate ESTABLISHED -j ACCEPT; ` +
			`[ -d "/proc/$pid" ] || rm -f /tmp/dropbear_koreader.pid' >/dev/null 2>&1 &`,
	} {
		if got := stopServerCommand(port); got != want {
			t.Errorf("stopServerCommand(%d) =\n%s\nwant\n%s", port, got, want)
		}
	}
}

func TestDisconnectNeedsALink(t *testing.T) {
	fake := fakedevice.New(t)
	fake.Stop()
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	if err := c.Disconnect(false); err != ErrNotLinked {
		t.Fatalf("Disconnect without a Session: %v", err)
	}
	waitFor(t, session(t, c), "searching", func(s State) bool { return s.Phase == PhaseSearching })
	if err := c.Disconnect(true); err != ErrNotLinked {
		t.Fatalf("Disconnect while searching: %v", err)
	}
	if got := fake.Execs(); len(got) != 0 {
		t.Fatalf("ran %q without a link", got)
	}
}

// Disconnecting only closes the link: the server keeps listening, and the
// Connector must not dial it again on its own.
func TestDisconnectOnlyWaitsForRetry(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "keep-awake on", awake(AwakeOn))
	dials := r.dials.Load()

	if err := c.Disconnect(false); err != nil {
		t.Fatal(err)
	}
	s := c.State()
	if !disconnected(s) {
		t.Fatalf("state after Disconnect = %+v", s)
	}
	if _, err := c.Device(); err != ErrNotLinked {
		t.Fatalf("Device() after Disconnect: %v", err)
	}
	if !releasedGracefully(fake)() {
		t.Fatalf("keep-awake not released: exits %+v", fake.Exits())
	}
	if got := fake.Execs(); len(got) != 1 || got[0] != wantKeepAwake {
		t.Fatalf("Execs = %q, want only keep-awake", got)
	}

	// Many retry intervals; a dial here would link at once.
	time.Sleep(20 * RetryInterval / 50)
	c.Reload()
	time.Sleep(5 * RetryInterval / 50)
	if n := r.dials.Load(); n != dials {
		t.Fatalf("%d dials after Disconnect", n-dials)
	}
	if s := c.State(); !disconnected(s) {
		t.Fatalf("state = %+v, want still disconnected", s)
	}

	c.Retry()
	waitFor(t, states, "linked after retry", linkedAt(fake.Addr()))
}

func TestDisconnectStopsTheServer(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "keep-awake on", awake(AwakeOn))

	if err := c.Disconnect(true); err != nil {
		t.Fatal(err)
	}
	if s := c.State(); !disconnected(s) {
		t.Fatalf("state after Disconnect = %+v", s)
	}
	_, port := splitAddr(t, fake.Addr())
	want := stopServerCommand(port)
	if got := fake.Execs(); len(got) != 2 || got[0] != wantKeepAwake || got[1] != want {
		t.Fatalf("Execs = %q, want keep-awake then %q", got, want)
	}
	// Keep-awake ended before the stop command was sent.
	eventually(t, "both commands ended", func() bool { return len(fake.Exits()) == 2 })
	ex := fake.Exits()
	if ex[0].Command != wantKeepAwake || !ex[0].Delivered || ex[1].Command != want {
		t.Fatalf("Exits = %+v, want keep-awake released first", ex)
	}
	if fake.PreventScreenSaver() {
		t.Fatal("Device left awake")
	}
	if fake.Listening() {
		t.Fatal("the Device's SSH server still listens")
	}

	dials := r.dials.Load()
	time.Sleep(20 * RetryInterval / 50)
	if n := r.dials.Load(); n != dials {
		t.Fatalf("%d dials after Disconnect", n-dials)
	}

	fake.Start()
	c.Retry()
	waitFor(t, states, "linked after retry", linkedAt(fake.Addr()))
}

// The stop command carries the port the Profile linked with.
func TestDisconnectUsesTheProfilePort(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	r.update(func(p *profile.Profile) { p.KeepAwake = false })
	c := r.connector(nil, fast)
	waitFor(t, session(t, c), "linked", linkedAt(fake.Addr()))
	if err := c.Disconnect(true); err != nil {
		t.Fatal(err)
	}
	p, err := r.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := fake.Execs(); len(got) != 1 || got[0] != stopServerCommand(p.Port) {
		t.Fatalf("Execs = %q, want the stop command for port %d", got, p.Port)
	}
}

// A tab reopened after a Disconnect finds it still disconnected.
func TestDisconnectOutlivesTheSession(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states, end := c.Session()
	waitFor(t, states, "linked", linkedAt(fake.Addr()))
	if err := c.Disconnect(false); err != nil {
		t.Fatal(err)
	}
	end()
	eventually(t, "idle", func() bool { return c.State().Phase == PhaseIdle })
	dials := r.dials.Load()

	states = session(t, c)
	waitFor(t, states, "disconnected", disconnected)
	time.Sleep(10 * RetryInterval / 50)
	if n := r.dials.Load(); n != dials {
		t.Fatalf("%d dials in the new Session", n-dials)
	}
	c.Retry()
	waitFor(t, states, "linked after retry", linkedAt(fake.Addr()))
}

// Forgetting the Kindle ends the disconnected wait, so the next Pairing
// links on its own.
func TestForgetClearsDisconnect(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "linked", linkedAt(fake.Addr()))
	if err := c.Disconnect(false); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Forget(); err != nil {
		t.Fatal(err)
	}
	c.Reload()
	waitFor(t, states, "unpaired", func(s State) bool { return s.Phase == PhaseUnpaired })
}
