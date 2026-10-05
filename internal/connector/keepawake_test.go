package connector

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/profile"
)

const wantKeepAwake = "lipc-set-prop com.lab126.powerd preventScreenSaver 1 || exit $?; cat >/dev/null; lipc-set-prop com.lab126.powerd preventScreenSaver 0"

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *rig) update(change func(*profile.Profile)) {
	r.t.Helper()
	if _, err := r.store.Update(func(p *profile.Profile) error {
		change(p)
		return nil
	}); err != nil {
		r.t.Fatal(err)
	}
}

func awake(a Awake) func(State) bool {
	return func(s State) bool { return s.Phase == PhaseLinked && s.KeepAwake == a }
}

// releasedGracefully reports whether the keep-awake command has ended
// because ember closed its stdin, with the Device able to sleep again.
func releasedGracefully(fake *fakedevice.Device) func() bool {
	return func() bool {
		ex := fake.Exits()
		return len(ex) == 1 && ex[0].Delivered && ex[0].Status == 0 && fake.Running() == 0 && !fake.PreventScreenSaver()
	}
}

func TestKeepAwakeRunsWhileLinked(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	waitFor(t, session(t, c), "keep-awake on", awake(AwakeOn))
	eventually(t, "preventScreenSaver 1", fake.PreventScreenSaver)
	if got := fake.Execs(); len(got) != 1 || got[0] != wantKeepAwake {
		t.Fatalf("Execs = %q", got)
	}
	if fake.Running() != 1 {
		t.Fatalf("Running = %d, want the command held open", fake.Running())
	}
}

func TestKeepAwakeOffNeverRuns(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	r.update(func(p *profile.Profile) { p.KeepAwake = false })
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "linked with keep-awake off", awake(AwakeOff))
	// Long enough for several keepalives and a reload.
	c.Reload()
	time.Sleep(300 * time.Millisecond)
	if got := fake.Execs(); len(got) != 0 {
		t.Fatalf("ran %q with keep-awake off", got)
	}
	if fake.PreventScreenSaver() {
		t.Fatal("preventScreenSaver set with keep-awake off")
	}
}

func TestKeepAwakeIsReleasedWhenTheSessionEnds(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states, end := c.Session()
	waitFor(t, states, "keep-awake on", awake(AwakeOn))
	end()
	eventually(t, "keep-awake released", releasedGracefully(fake))
}

func TestKeepAwakeIsReleasedWhenEmberExits(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	waitFor(t, session(t, c), "keep-awake on", awake(AwakeOn))
	c.Close()
	// Close returns once the link is down, so the release is already done.
	if !releasedGracefully(fake)() {
		t.Fatalf("not released by Close: exits %+v, running %d, awake %v", fake.Exits(), fake.Running(), fake.PreventScreenSaver())
	}
}

func TestTurningKeepAwakeOffWhileLinkedStopsIt(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "keep-awake on", awake(AwakeOn))
	dials := r.dials.Load()

	r.update(func(p *profile.Profile) { p.KeepAwake = false })
	c.Reload()
	waitFor(t, states, "keep-awake off", awake(AwakeOff))
	eventually(t, "keep-awake released", releasedGracefully(fake))

	r.update(func(p *profile.Profile) { p.KeepAwake = true })
	c.Reload()
	waitFor(t, states, "keep-awake on again", awake(AwakeOn))
	eventually(t, "preventScreenSaver 1", fake.PreventScreenSaver)
	if r.dials.Load() != dials {
		t.Fatal("the link was redialed for a keep-awake change")
	}
}

// syncBuffer collects log output written from the Connector's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLog(t *testing.T) *syncBuffer {
	b := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(b)
	t.Cleanup(func() { log.SetOutput(prev) })
	return b
}

func TestMissingLipcIsReportedOnceAndNotRetried(t *testing.T) {
	logs := captureLog(t)
	fake := fakedevice.New(t, fakedevice.WithoutLipc())
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "keep-awake unavailable", awake(AwakeUnavailable))

	// Neither a reload nor a new link tries again.
	c.Reload()
	fake.Stop()
	waitFor(t, states, "link lost", func(s State) bool { return s.Phase == PhaseSearching })
	fake.Start()
	waitFor(t, states, "relinked, still unavailable", awake(AwakeUnavailable))
	time.Sleep(100 * time.Millisecond)
	if n := len(fake.Execs()); n != 1 {
		t.Fatalf("keep-awake tried %d times, want once", n)
	}
	if n := strings.Count(logs.String(), "lipc-set-prop"); n != 1 {
		t.Fatalf("logged %d times, want once:\n%s", n, logs)
	}

	// Switching it off and on is the user asking to try again.
	r.update(func(p *profile.Profile) { p.KeepAwake = false })
	c.Reload()
	waitFor(t, states, "keep-awake off", awake(AwakeOff))
	r.update(func(p *profile.Profile) { p.KeepAwake = true })
	c.Reload()
	waitFor(t, states, "unavailable again", awake(AwakeUnavailable))
	if n := len(fake.Execs()); n != 2 {
		t.Fatalf("keep-awake tried %d times, want twice", n)
	}
}

func TestReloadRedialsWhenTheLoginChanges(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "link", linkedAt(fake.Addr()))
	dials := r.dials.Load()

	r.update(func(p *profile.Profile) { p.Passwordless = true })
	c.Reload()
	waitFor(t, states, "link dropped", func(s State) bool { return s.Phase != PhaseLinked })
	waitFor(t, states, "relinked", linkedAt(fake.Addr()))
	if r.dials.Load() == dials {
		t.Fatal("not redialed")
	}
}

func TestReloadAfterForgetUnlinks(t *testing.T) {
	fake := fakedevice.New(t)
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "keep-awake on", awake(AwakeOn))
	if err := r.store.Forget(); err != nil {
		t.Fatal(err)
	}
	c.Reload()
	waitFor(t, states, "unpaired", func(s State) bool { return s.Phase == PhaseUnpaired })
	eventually(t, "keep-awake released", releasedGracefully(fake))
	if _, err := c.Device(); err == nil {
		t.Fatal("still linked to a forgotten Device")
	}
}

// A command the Device refuses to start counts as unavailable just like
// one that exits with an error: logged once, not tried again on a reload or
// the next link, until keep-awake is switched off and on.
func TestKeepAwakeThatCannotStartIsNotRetried(t *testing.T) {
	logs := captureLog(t)
	fake := fakedevice.New(t, fakedevice.RefusingExec())
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "keep-awake unavailable", awake(AwakeUnavailable))

	c.Reload()
	fake.Stop()
	waitFor(t, states, "link lost", func(s State) bool { return s.Phase == PhaseSearching })
	fake.Start()
	waitFor(t, states, "relinked, still unavailable", awake(AwakeUnavailable))
	time.Sleep(100 * time.Millisecond)
	if n := len(fake.Execs()); n != 1 {
		t.Fatalf("keep-awake tried %d times, want once", n)
	}
	if n := strings.Count(logs.String(), "could not start"); n != 1 {
		t.Fatalf("logged %d times, want once:\n%s", n, logs)
	}

	r.update(func(p *profile.Profile) { p.KeepAwake = false })
	c.Reload()
	waitFor(t, states, "keep-awake off", awake(AwakeOff))
	r.update(func(p *profile.Profile) { p.KeepAwake = true })
	c.Reload()
	waitFor(t, states, "unavailable again", awake(AwakeUnavailable))
	if n := len(fake.Execs()); n != 2 {
		t.Fatalf("keep-awake tried %d times, want twice", n)
	}
}

// A command that ends with status 0 ran to its end; the Device is not
// unavailable, and the next link starts it again.
func TestKeepAwakeThatEndsCleanlyIsNotUnavailable(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.QuickExit())
	r := newRig(t, fake, fake.Addr(), fake)
	c := r.connector(nil, fast)
	states := session(t, c)
	waitFor(t, states, "keep-awake ended", awake(AwakeOff))

	fake.Stop()
	waitFor(t, states, "link lost", func(s State) bool { return s.Phase == PhaseSearching })
	fake.Start()
	waitFor(t, states, "relinked", linkedAt(fake.Addr()))
	eventually(t, "keep-awake started again", func() bool { return len(fake.Execs()) == 2 })
}
