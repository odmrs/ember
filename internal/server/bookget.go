package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type catalogBook struct {
	ID, Title, Author, Year, Source string
	Publisher, Details              string `json:",omitempty"`
	Formats                         map[string]string
	Token                           string `json:"token"`
}
type bookgetResult struct {
	Path     string   `json:"path"`
	Sent     bool     `json:"sent"`
	Messages []string `json:"messages"`
}
type bookgetRun func(context.Context, ...string) ([]byte, string, error)
type catalogChoice struct {
	id      string
	expires time.Time
}
type bookCatalog struct {
	mu      sync.Mutex
	choices map[string]catalogChoice
	busy    chan struct{}
	status  string
	run     bookgetRun
}

func newBookCatalog(hostport string) *bookCatalog {
	c := &bookCatalog{choices: map[string]catalogChoice{}, busy: make(chan struct{}, 1)}
	c.run = func(ctx context.Context, args ...string) ([]byte, string, error) {
		return runBookget(ctx, hostport, c.setStatus, args...)
	}
	return c
}
func (c *bookCatalog) setStatus(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(message) > 2000 {
		message = message[:2000]
	}
	c.status = message
}

// cappedOutput bounds memory even if a subprocess emits unexpected output.
type cappedOutput struct {
	bytes.Buffer
	limit int
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("bookget output limit exceeded")
	}
	return b.Buffer.Write(p)
}
func runBookget(ctx context.Context, hostport string, progress func(string), args ...string) ([]byte, string, error) {
	binary := os.Getenv("BOOKGET_BIN")
	if binary == "" {
		if exe, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), "bookget")
			if st, e := os.Stat(candidate); e == nil && !st.IsDir() {
				binary = candidate
			}
		}
	}
	if binary == "" {
		var err error
		binary, err = exec.LookPath("bookget")
		if err != nil {
			return nil, "", fmt.Errorf("bookget is missing: place it beside ember or set BOOKGET_BIN")
		}
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "BOOKGET_EMBER_URL=http://"+hostport)
	stdout := &cappedOutput{limit: 4 << 20}
	stderr := &cappedOutput{limit: 64 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = &progressOutput{output: stderr, update: progress}
	err := cmd.Run()
	return stdout.Bytes(), strings.TrimSpace(stderr.String()), err
}
func routeBookCatalog(mux *http.ServeMux, c *bookCatalog) {
	mux.HandleFunc("GET /api/books/status", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		message := c.status
		c.mu.Unlock()
		writeJSON(w, map[string]any{"busy": len(c.busy) > 0, "message": message})
	})
	mux.HandleFunc("GET /api/books/search", func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" || len(query) > 300 {
			writeErrorCode(w, 400, "bad_request", "enter a title (up to 300 bytes)")
			return
		}
		language := r.URL.Query().Get("language")
		if language != "" && (len(language) != 2 && len(language) != 3 || strings.Trim(language, "abcdefghijklmnopqrstuvwxyz") != "") {
			writeErrorCode(w, 400, "bad_request", "language must be a two or three letter lowercase code")
			return
		}
		// Bound external searches and downloads together; preserve selected tokens
		// across subsequent searches so another tab cannot change their meaning.
		select {
		case c.busy <- struct{}{}:
			defer func() { <-c.busy }()
		default:
			writeErrorCode(w, 409, "bookget_busy", "another book operation is running")
			return
		}
		c.setStatus("Iniciando a busca…")
		defer c.setStatus("")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		args := []string{"search", query, "--json"}
		if language != "" {
			args = append(args, "--language", language)
		}
		data, warnings, err := c.run(ctx, args...)
		if err != nil {
			writeErrorCode(w, 502, "bookget_failed", bookgetMessage(warnings, err))
			return
		}
		var books []catalogBook
		if err = json.Unmarshal(data, &books); err != nil {
			writeErrorCode(w, 502, "bookget_failed", "invalid bookget search response")
			return
		}
		if books == nil {
			books = []catalogBook{}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		now := time.Now()
		for token, choice := range c.choices {
			if now.After(choice.expires) {
				delete(c.choices, token)
			}
		}
		if len(c.choices)+len(books) > 2000 {
			writeErrorCode(w, 409, "bookget_busy", "too many pending selections; retry after 30 minutes")
			return
		}
		for i := range books {
			var random [24]byte
			if _, err = rand.Read(random[:]); err != nil {
				writeErrorCode(w, 500, "bookget_failed", "could not create selection")
				return
			}
			token := hex.EncodeToString(random[:])
			books[i].Token = token
			c.choices[token] = catalogChoice{books[i].ID, now.Add(30 * time.Minute)}
		}
		writeJSON(w, map[string]any{"books": books, "warnings": warnings})
	})
	mux.HandleFunc("POST /api/books/get", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token  string `json:"token"`
			Format string `json:"format"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if !decode(w, r, &req) {
			return
		}
		if req.Format != "epub" && req.Format != "azw3" && req.Format != "pdf" {
			writeErrorCode(w, 400, "bad_request", "format must be epub, azw3 or pdf")
			return
		}
		c.mu.Lock()
		choice, ok := c.choices[req.Token]
		c.mu.Unlock()
		if !ok || time.Now().After(choice.expires) {
			writeErrorCode(w, 400, "bad_request", "selection expired; search again")
			return
		}
		select {
		case c.busy <- struct{}{}:
			defer func() { <-c.busy }()
		default:
			writeErrorCode(w, 409, "bookget_busy", "another book operation is running")
			return
		}
		c.setStatus("Preparando o download…")
		defer c.setStatus("")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		data, warnings, err := c.run(ctx, "get", choice.id, "--format", req.Format, "--send", "--json")
		if err != nil {
			writeErrorCode(w, 502, "bookget_failed", bookgetMessage(warnings, err))
			return
		}
		var result bookgetResult
		if err = json.Unmarshal(data, &result); err != nil || result.Path == "" {
			writeErrorCode(w, 502, "bookget_failed", "invalid bookget download response")
			return
		}
		writeJSON(w, result)
	})
}
func bookgetMessage(stderr string, err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "book operation timed out"
	}
	if stderr != "" {
		return stderr
	}
	return err.Error()
}

type progressOutput struct {
	output  *cappedOutput
	pending string
	update  func(string)
}

func (p *progressOutput) Write(data []byte) (int, error) {
	n, err := p.output.Write(data)
	if err != nil {
		return n, err
	}
	p.pending += string(data)
	for {
		line, rest, ok := strings.Cut(p.pending, "\n")
		if !ok {
			break
		}
		p.pending = rest
		if p.update != nil && strings.TrimSpace(line) != "" {
			p.update(strings.TrimSpace(line))
		}
	}
	return n, nil
}
