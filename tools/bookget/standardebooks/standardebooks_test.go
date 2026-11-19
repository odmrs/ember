package standardebooks

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"bookget/safehttp"
)

func TestCatalog(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := r.BasicAuth()
		if !ok || user != "reader" {
			t.Error("missing auth")
		}
		if r.URL.Path == "/feed" {
			fmt.Fprint(w, `<feed xmlns="http://www.w3.org/2005/Atom"><link rel="next" href="/page2"/></feed>`)
			return
		}
		fmt.Fprint(w, `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>urn:book:1</id><title>Dom Casmurro</title><author><name>Machado</name></author><published>2020-01-01</published><link rel="http://opds-spec.org/acquisition/open-access" type="application/epub+zip" href="/book.epub"/></entry></feed>`)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	c.Username = "reader"
	c.Password = "x"
	s := &Source{srv.URL + "/feed", c}
	books, err := s.Search(context.Background(), "dom casmurro")
	if err != nil || len(books) != 1 || books[0].Formats["epub"] != srv.URL+"/book.epub" {
		t.Fatalf("%+v %v", books, err)
	}
	dl, err := s.Fetch(context.Background(), books[0].ID, "epub")
	if err != nil || dl.Book.Year != "2020" {
		t.Fatalf("%+v %v", dl, err)
	}
	books, err = s.Search(context.Background(), "absent")
	if err != nil || len(books) != 0 {
		t.Fatalf("%v %v", books, err)
	}
}
func TestUnauthorized(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	s := &Source{srv.URL, c}
	if _, err := s.Search(context.Background(), "book"); err == nil {
		t.Fatal("missing auth accepted")
	}
}
