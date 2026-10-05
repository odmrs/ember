package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/profile"
)

func TestEventsStreamTheConnectorState(t *testing.T) {
	store, err := profile.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conn := connector.New(connector.Config{Store: store, Targets: func([]int) ([]string, error) { return nil, nil }})
	t.Cleanup(conn.Close)
	flow, _ := newFlow(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(New(ln.Addr().String(), "test", flow, conn))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	var got connector.State
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		if err := json.Unmarshal([]byte(data), &got); err != nil {
			t.Fatal(err)
		}
		if got.Phase == connector.PhaseUnpaired {
			break
		}
	}
	if got.Phase != connector.PhaseUnpaired {
		t.Fatalf("last state %+v, stream error %v", got, sc.Err())
	}

	res.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for conn.State().Phase != connector.PhaseIdle {
		if time.Now().After(deadline) {
			t.Fatal("the Session outlived its stream")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEventsRejectCrossSite(t *testing.T) {
	rec := do(t, "GET", "/api/events", self, "", "Sec-Fetch-Site", "cross-site")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// Shutting the server down must not wait on open streams.
func TestCloseEndsStreams(t *testing.T) {
	store, err := profile.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conn := connector.New(connector.Config{Store: store, Targets: func([]int) ([]string, error) { return nil, nil }})
	flow, _ := newFlow(t)
	h := New(self, "test", flow, conn)

	done := make(chan struct{})
	go func() {
		call(h, "GET", "/api/events", self, "", "")
		close(done)
	}()
	for conn.State().Phase == connector.PhaseIdle {
		time.Sleep(time.Millisecond)
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open after Close")
	}
}
