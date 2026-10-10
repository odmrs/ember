package main

import (
	"testing"
	"time"
)

const tick = 40 * time.Millisecond

func quitSignal() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	return ch, func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func expectQuit(t *testing.T, ch chan struct{}, want bool, within time.Duration) {
	t.Helper()
	select {
	case <-ch:
		if !want {
			t.Fatal("quit although a tab is open or came back")
		}
	case <-time.After(within):
		if want {
			t.Fatal("did not quit")
		}
	}
}

func TestQuitsAfterTheLastTabCloses(t *testing.T) {
	ch, quit := quitSignal()
	w := watchTabs(time.Hour, tick, quit)
	w.sessions(1)
	w.sessions(0)
	expectQuit(t, ch, true, 10*tick)
}

func TestAReloadDoesNotQuit(t *testing.T) {
	ch, quit := quitSignal()
	w := watchTabs(time.Hour, 3*tick, quit)
	w.sessions(1)
	w.sessions(0)
	w.sessions(1)
	expectQuit(t, ch, false, 8*tick)
}

func TestStaysWhileAnotherTabIsOpen(t *testing.T) {
	ch, quit := quitSignal()
	w := watchTabs(time.Hour, tick, quit)
	w.sessions(1)
	w.sessions(2)
	w.sessions(1)
	expectQuit(t, ch, false, 5*tick)
}

func TestQuitsWhenNoTabEverOpens(t *testing.T) {
	ch, quit := quitSignal()
	watchTabs(tick, time.Hour, quit)
	expectQuit(t, ch, true, 10*tick)
}

func TestAFirstTabCancelsTheStartupCountdown(t *testing.T) {
	ch, quit := quitSignal()
	w := watchTabs(2*tick, time.Hour, quit)
	w.sessions(1)
	expectQuit(t, ch, false, 6*tick)
}
