package annasarchive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestManualVerificationThenAutomaticResume(t *testing.T) {
	states := []string{`{"manual":true}`, `{"manual":true,"url":"https://provider.example/not-ready"}`, `{"manual":false,"url":"https://provider.example/book.epub"}`}
	i, shown := 0, 0
	var messages []string
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := waitForDownload(ctx, func(context.Context) (string, error) { v := states[i]; i++; return v, nil }, func(s string) { messages = append(messages, s) }, func() { shown++ })
	if err != nil || got != "https://provider.example/book.epub" || shown != 1 || len(messages) != 2 {
		t.Fatal(got, err, shown, messages)
	}
}
func TestWaitingRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitForDownload(ctx, func(context.Context) (string, error) { return `{"manual":true}`, nil }, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestWaitingDoesNotAnnounceAFilePrematurely(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := waitForDownload(ctx, func(context.Context) (string, error) { return `{"manual":false}`, nil }, func(s string) {
		if strings.Contains(s, "disponível") {
			t.Error("no URL was available")
		}
	}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
