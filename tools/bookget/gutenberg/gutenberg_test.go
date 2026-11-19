package gutenberg

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"bookget/safehttp"
)

func TestSource(t *testing.T) {
	var base string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/books" {
			if r.URL.Query().Get("search") != "dom casmurro" || r.URL.Query().Get("copyright") != "false" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, `{"next":null,"results":[{"id":1,"title":"Dom Casmurro","authors":[{"name":"Machado"}],"copyright":false,"formats":{"application/epub+zip":"`+base+`/book.epub"}},{"id":2,"copyright":true},{"id":3,"copyright":null}]}`)
		} else {
			fmt.Fprint(w, `{"id":1,"title":"Dom Casmurro","copyright":false,"formats":{"application/epub+zip":"`+base+`/book.epub"}}`)
		}
	}))
	defer srv.Close()
	base = srv.URL
	u, _ := url.Parse(base)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	s := &Source{base + "/books", c}
	books, err := s.Search(context.Background(), "dom casmurro")
	if err != nil || len(books) != 1 {
		t.Fatalf("%v %v", books, err)
	}
	dl, err := s.Fetch(context.Background(), "gutenberg:1", "pdf")
	if err != nil || dl.Format != "epub" {
		t.Fatalf("%v %v", dl, err)
	}
	if _, err = s.Fetch(context.Background(), "gutenberg:../secret", "epub"); err == nil {
		t.Fatal("bad ID")
	}
}
func TestRejectCopyrightUnknown(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"id":1,"copyright":null}`) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	s := &Source{srv.URL, c}
	if _, err := s.Fetch(context.Background(), "1", "epub"); err == nil {
		t.Fatal("unknown copyright accepted")
	}
}
