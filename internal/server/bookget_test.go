package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func catalogHandler(c *bookCatalog) http.Handler {
	mux := http.NewServeMux()
	routeBookCatalog(mux, c)
	return guard(self, mux)
}
func TestBookCatalogSelection(t *testing.T) {
	var calls [][]string
	c := newBookCatalog(self)
	c.run = func(_ context.Context, args ...string) ([]byte, string, error) {
		calls = append(calls, args)
		if args[0] == "search" {
			return []byte(`[{"ID":"gutenberg:1","Title":"<script>bad</script>","Author":"Machado","Formats":{"epub":"https://example.test/book"}}]`), "Standard Ebooks unavailable", nil
		}
		return []byte(`{"path":"/tmp/Book.epub","sent":true,"messages":[]}`), "", nil
	}
	h := catalogHandler(c)
	rec := call(h, "GET", "/api/books/search?q=dom+casmurro", self, "", "")
	if rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	var result struct {
		Books    []catalogBook
		Warnings string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Books) != 1 || len(result.Books[0].Token) != 48 || result.Warnings == "" {
		t.Fatal(result)
	}
	token := result.Books[0].Token
	rec = call(h, "POST", "/api/books/get", self, "http://"+self, `{"token":"`+token+`","format":"epub"}`, "Content-Type", "application/json")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"sent":true`) {
		t.Fatal(rec.Code, rec.Body)
	}
	want := [][]string{{"search", "dom casmurro", "--json"}, {"get", "gutenberg:1", "--format", "epub", "--send", "--json"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatal(calls)
	}
	// Browsers cannot turn a source URL into an arbitrary fetch or choose flags.
	rec = call(h, "POST", "/api/books/get", self, "http://"+self, `{"token":"https://evil.test/payload","format":"epub"}`, "Content-Type", "application/json")
	if rec.Code != 400 || len(calls) != 2 {
		t.Fatal(rec.Code, calls)
	}
}
func TestBookCatalogFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  bookgetRun
		want int
	}{
		{"missing binary", func(context.Context, ...string) ([]byte, string, error) {
			return nil, "", errors.New("missing bookget")
		}, 502},
		{"malformed", func(context.Context, ...string) ([]byte, string, error) { return []byte("bad"), "", nil }, 502},
		{"empty", func(context.Context, ...string) ([]byte, string, error) { return []byte("null"), "", nil }, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newBookCatalog(self)
			c.run = tt.run
			rec := call(catalogHandler(c), "GET", "/api/books/search?q=title", self, "", "")
			if rec.Code != tt.want {
				t.Fatal(rec.Code, rec.Body)
			}
		})
	}
}
func TestBookCatalogLimitsAndGuard(t *testing.T) {
	c := newBookCatalog(self)
	called := false
	c.run = func(context.Context, ...string) ([]byte, string, error) { called = true; return nil, "", nil }
	h := catalogHandler(c)
	for _, tt := range []struct {
		method, path, host, origin, body string
		want                             int
	}{
		{"GET", "/api/books/search?q=title", "evil.test", "", "", 403},
		{"POST", "/api/books/get", self, "http://evil.test", `{}`, 403},
		{"GET", "/api/books/search?q=", self, "", "", 400},
		{"POST", "/api/books/get", self, "http://" + self, `{"token":"bad","format":"exe"}`, 400},
		{"POST", "/api/books/get", self, "http://" + self, `{"token":"bad","format":"epub"}`, 400},
	} {
		rec := call(h, tt.method, tt.path, tt.host, tt.origin, tt.body, "Content-Type", "application/json")
		if rec.Code != tt.want {
			t.Fatal(tt, rec.Code, rec.Body)
		}
	}
	c.choices["expired"] = catalogChoice{"gutenberg:1", time.Now().Add(-time.Hour)}
	rec := call(h, "POST", "/api/books/get", self, "http://"+self, `{"token":"expired","format":"epub"}`, "Content-Type", "application/json")
	if rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	c.busy <- struct{}{}
	rec = call(h, "GET", "/api/books/search?q=title", self, "", "")
	<-c.busy
	if rec.Code != 409 || called {
		t.Fatal(rec.Code, called)
	}
}
func TestBookCatalogSelectionsSurviveOtherSearch(t *testing.T) {
	c := newBookCatalog(self)
	n := 0
	c.run = func(_ context.Context, args ...string) ([]byte, string, error) {
		if args[0] == "get" {
			if args[1] != "gutenberg:1" {
				t.Error(args)
			}
			return []byte(`{"path":"/tmp/file","sent":false}`), "", nil
		}
		n++
		return []byte(fmt.Sprintf(`[{"ID":"gutenberg:%d"}]`, n)), "", nil
	}
	h := catalogHandler(c)
	rec := call(h, "GET", "/api/books/search?q=one", self, "", "")
	var first struct{ Books []catalogBook }
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	_ = call(h, "GET", "/api/books/search?q=two", self, "", "")
	rec = call(h, "POST", "/api/books/get", self, "http://"+self, `{"token":"`+first.Books[0].Token+`","format":"epub"}`, "Content-Type", "application/json")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
}
func TestCappedBookgetOutput(t *testing.T) {
	b := &cappedOutput{limit: 4}
	if _, err := b.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("5")); err == nil {
		t.Fatal("unbounded output")
	}
}

func TestBookCatalogLiveProgress(t *testing.T) {
	c := newBookCatalog(self)
	started := make(chan struct{})
	finish := make(chan struct{})
	c.run = func(context.Context, ...string) ([]byte, string, error) {
		c.setStatus("Aguardando verificação na janela do navegador")
		close(started)
		<-finish
		return []byte(`[]`), "", nil
	}
	h := catalogHandler(c)
	done := make(chan struct{})
	go func() { _ = call(h, "GET", "/api/books/search?q=title", self, "", ""); close(done) }()
	<-started
	rec := call(h, "GET", "/api/books/status", self, "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"busy":true`) || !strings.Contains(rec.Body.String(), "Aguardando verificação") {
		t.Fatal(rec.Code, rec.Body)
	}
	close(finish)
	<-done
	rec = call(h, "GET", "/api/books/status", self, "", "")
	if !strings.Contains(rec.Body.String(), `"busy":false`) {
		t.Fatal(rec.Body)
	}
}
func TestBookgetProgressHandlesPartialLines(t *testing.T) {
	var messages []string
	p := &progressOutput{output: &cappedOutput{limit: 100}, update: func(s string) { messages = append(messages, s) }}
	if _, err := p.Write([]byte("Abrindo ")); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatal(messages)
	}
	if _, err := p.Write([]byte("navegador\nAguardando verificação\n")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(messages, []string{"Abrindo navegador", "Aguardando verificação"}) {
		t.Fatal(messages)
	}
}

func TestBookCatalogLanguage(t *testing.T) {
	for _, tt := range []struct {
		language string
		status   int
	}{{"pt", 200}, {"en", 200}, {"", 200}, {"--send", 400}, {"pt-BR", 400}, {"PORTUGUESE", 400}, {"p", 400}} {
		t.Run(tt.language, func(t *testing.T) {
			c := newBookCatalog(self)
			called := false
			c.run = func(_ context.Context, args ...string) ([]byte, string, error) {
				called = true
				want := []string{"search", "verity", "--json"}
				if tt.language != "" {
					want = append(want, "--language", tt.language)
				}
				if !reflect.DeepEqual(args, want) {
					t.Errorf("got %v, want %v", args, want)
				}
				return []byte(`[]`), "", nil
			}
			rec := call(catalogHandler(c), "GET", "/api/books/search?q=verity&language="+tt.language, self, "", "")
			if rec.Code != tt.status || called != (tt.status == 200) {
				t.Fatal(rec.Code, called, rec.Body)
			}
		})
	}
}
