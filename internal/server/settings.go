package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/odmrs/ember/internal/library"
	"github.com/odmrs/ember/internal/profile"
)

// confirmTTL is how long the user has to confirm turning passwordless mode
// on after asking for it.
const confirmTTL = 2 * time.Minute

var (
	errBadPort       = errors.New("settings: port must be between 1 and 65535")
	errBadUser       = errors.New("settings: user must be a plain Unix user name")
	errNotConfirmed  = errors.New("settings: passwordless mode needs a confirmation token from a request just made")
	errForgetUnasked = errors.New("settings: forgetting the Kindle needs confirm set")
	userName         = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// settingsRequest changes the fields it carries and keeps the others.
// Passwordless mode is deliberately absent: it has its own two-step route.
type settingsRequest struct {
	LibraryRoot *string `json:"libraryRoot"`
	Port        *int    `json:"port"`
	User        *string `json:"user"`
	KeepAwake   *bool   `json:"keepAwake"`
}

// routeSettings serves the Settings screen. Every change is saved to the
// Profile and handed to the Connector, which applies it to the link at once.
func routeSettings(mux *http.ServeMux, a *access, gate *confirmGate) {
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var req settingsRequest
		if !decode(w, r, &req) {
			return
		}
		p, err := a.applySettings(req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"device": viewOf(p)})
	})

	// Turning passwordless mode on takes two requests: this one returns a
	// token, and only a confirm carrying it, sent after the user agreed in
	// the dialog, changes the Profile.
	mux.HandleFunc("POST /api/settings/passwordless/request", func(w http.ResponseWriter, r *http.Request) {
		if !decode(w, r, &struct{}{}) {
			return
		}
		if _, err := a.flow.Profile(); err != nil {
			writeError(w, err)
			return
		}
		token, expires, err := gate.issue()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"token": token, "expiresAt": expires})
	})

	mux.HandleFunc("POST /api/settings/passwordless/confirm", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &req) {
			return
		}
		if !gate.redeem(req.Token) {
			writeError(w, errNotConfirmed)
			return
		}
		a.setPasswordless(w, true)
	})

	mux.HandleFunc("POST /api/settings/passwordless/off", func(w http.ResponseWriter, r *http.Request) {
		if !decode(w, r, &struct{}{}) {
			return
		}
		gate.clear()
		a.setPasswordless(w, false)
	})

	mux.HandleFunc("POST /api/settings/forget", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Confirm bool `json:"confirm"`
		}
		if !decode(w, r, &req) {
			return
		}
		if !req.Confirm {
			writeError(w, errForgetUnasked)
			return
		}
		gate.clear()
		// A write in progress finishes on the Device before the link goes.
		a.mu.Lock()
		err := a.flow.Forget()
		a.mu.Unlock()
		a.conn.Reload()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]string{"state": "unpaired"})
	})
}

// applySettings validates and saves req. A new Library root must exist on the
// linked Device as a folder inside user storage; it is checked under the
// write lock, so no Library write runs across the change.
func (a *access) applySettings(req settingsRequest) (profile.Profile, error) {
	if req.Port != nil && (*req.Port < 1 || *req.Port > 65535) {
		return profile.Profile{}, errBadPort
	}
	if req.User != nil && !userName.MatchString(*req.User) {
		return profile.Profile{}, errBadUser
	}
	current, err := a.flow.Profile()
	if err != nil {
		return profile.Profile{}, err
	}
	var root string
	if req.LibraryRoot != nil && *req.LibraryRoot != current.LibraryRoot {
		d, err := a.conn.Device()
		if err != nil {
			return profile.Profile{}, err
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if root, err = library.CheckRoot(d, *req.LibraryRoot); err != nil {
			return profile.Profile{}, err
		}
	}
	p, err := a.flow.UpdateProfile(func(p *profile.Profile) error {
		if root != "" {
			p.LibraryRoot = root
			p.Survey.LibraryExists = true
		}
		if req.Port != nil {
			p.Port = *req.Port
		}
		if req.User != nil {
			p.User = *req.User
		}
		if req.KeepAwake != nil {
			p.KeepAwake = *req.KeepAwake
		}
		return nil
	})
	if err != nil {
		return profile.Profile{}, err
	}
	a.conn.Reload()
	return p, nil
}

func (a *access) setPasswordless(w http.ResponseWriter, on bool) {
	p, err := a.flow.UpdateProfile(func(p *profile.Profile) error {
		p.Passwordless = on
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	a.conn.Reload()
	writeJSON(w, map[string]any{"device": viewOf(p)})
}

// confirmGate holds the one passwordless-mode token in flight. A token is
// single use and short lived, and any attempt to redeem one spends it, right
// or wrong, so it cannot be guessed.
type confirmGate struct {
	now func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// issue makes a new token, replacing any pending one.
func (g *confirmGate) issue() (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.token = base64.RawURLEncoding.EncodeToString(raw)
	g.expires = g.now().Add(confirmTTL)
	return g.token, g.expires, nil
}

// redeem reports whether token is the pending one and still valid, and
// spends the pending token either way.
func (g *confirmGate) redeem(token string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	pending, expires := g.token, g.expires
	g.token = ""
	return pending != "" &&
		subtle.ConstantTimeCompare([]byte(token), []byte(pending)) == 1 &&
		g.now().Before(expires)
}

func (g *confirmGate) clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.token = ""
}
