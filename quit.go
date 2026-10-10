package main

import (
	"sync"
	"time"
)

// How long ember outlives its dashboard. A reload closes the tab's Session
// and opens a new one a moment later, so the last tab closing only starts a
// countdown; a tab that never shows up gets longer, for a slow browser start.
const (
	quitAfterLastTab = 6 * time.Second
	quitIfNoTabOpens = 90 * time.Second
)

// tabWatch calls quit once no dashboard tab has been open for a while.
type tabWatch struct {
	mu    sync.Mutex
	grace time.Duration
	quit  func()
	timer *time.Timer
}

// watchTabs starts the countdown for a first tab that never opens.
func watchTabs(startup, grace time.Duration, quit func()) *tabWatch {
	return &tabWatch{grace: grace, quit: quit, timer: time.AfterFunc(startup, quit)}
}

// sessions is told how many tabs are open each time that number changes.
func (w *tabWatch) sessions(open int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timer.Stop()
	if open == 0 {
		w.timer = time.AfterFunc(w.grace, w.quit)
	}
}
