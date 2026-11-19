package main

import (
	"archive/zip"
	"bookget/annasarchive"
	"bookget/gutenberg"
	"bookget/safehttp"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"bookget/source"
)

func TestCacheSelection(t *testing.T) {
	t.Setenv("BOOKGET_CACHE_DIR", t.TempDir())
	if err := saveCache([]source.Book{{ID: "gutenberg:1"}, {ID: "gutenberg:2"}}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		in, want string
		bad      bool
	}{{"1", "gutenberg:1", false}, {"2", "gutenberg:2", false}, {"0", "", true}, {"3", "", true}, {"gutenberg:10", "gutenberg:10", false}} {
		id, err := resolveID(tt.in)
		if (err != nil) != tt.bad || id != tt.want {
			t.Fatalf("%s: %s %v", tt.in, id, err)
		}
	}
}
func TestInvalidArgs(t *testing.T) {
	for _, args := range [][]string{nil, {"search"}, {"wat", "title"}, {"get", "gutenberg:1", "--format", "html"}, {"get", "1", "2"}} {
		var b bytes.Buffer
		if err := run(context.Background(), args, &b, &b); err == nil {
			t.Fatal(args)
		}
	}
}

func TestSearchSelectGetAndSend(t *testing.T) {
	t.Setenv("BOOKGET_CACHE_DIR", t.TempDir())
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, data := range map[string]string{"mimetype": "application/epub+zip", "META-INF/container.xml": `<container><rootfiles><rootfile full-path="book.opf"/></rootfiles></container>`, "book.opf": `<package><metadata><title>Dom Casmurro</title><creator>Machado</creator></metadata></package>`} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(data))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	ebook := buf.Bytes()
	var base string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file" {
			_, _ = w.Write(ebook)
			return
		}
		record := `{"id":1,"title":"Dom Casmurro","authors":[{"name":"Machado"}],"copyright":false,"formats":{"application/epub+zip":"` + base + `/file"}}`
		if r.URL.Path == "/books" {
			fmt.Fprint(w, `{"results":[`+record+`],"next":null}`)
		} else {
			fmt.Fprint(w, record)
		}
	}))
	defer srv.Close()
	base = srv.URL
	u, _ := url.Parse(base)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	sources := []source.Source{&gutenberg.Source{Base: base + "/books", Client: c}}
	uploaded := false
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			fmt.Fprint(w, `{"state":"paired"}`)
			return
		}
		got, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(got, ebook) {
			t.Error("wrong book bytes", err)
		}
		uploaded = true
		fmt.Fprint(w, `{}`)
	}))
	defer local.Close()
	t.Setenv("BOOKGET_EMBER_URL", local.URL)
	var out, logs bytes.Buffer
	if err := runWithSources(context.Background(), []string{"search", "dom casmurro", "--json"}, &out, &logs, sources); err != nil {
		t.Fatal(err)
	}
	var books []source.Book
	if err := json.Unmarshal(out.Bytes(), &books); err != nil || len(books) != 1 {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	dir := t.TempDir()
	if err := runWithSources(context.Background(), []string{"get", "1", "--format", "epub", "--send", "--json", "--out", dir}, &out, &logs, sources); err != nil {
		t.Fatal(err, logs.String())
	}
	var result struct {
		Path string
		Sent bool
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Sent || !uploaded {
		t.Fatal(out.String(), err)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatal(err)
	}
}

func TestSearchLanguageFlag(t *testing.T) {
	t.Setenv("BOOKGET_CACHE_DIR", t.TempDir())
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "Verity" || r.URL.Query().Get("lang") != "pt" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `<a href="/md5/0123456789abcdef0123456789abcdef"><h3>Verity</h3><div>Portuguese [pt], epub</div></a><a href="/md5/1123456789abcdef0123456789abcdef"><h3>Verity</h3><div>English [en], epub</div></a>`)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	client := safehttp.New(u.Host)
	client.HTTP = srv.Client()
	for _, args := range [][]string{{"search", "Verity", "--language", "pt", "--json"}, {"search", "--language=pt", "Verity", "--json"}} {
		var out, logs bytes.Buffer
		err := runWithSources(context.Background(), args, &out, &logs, []source.Source{&annasarchive.Source{Base: srv.URL, Client: client}})
		if err != nil {
			t.Fatal(err, logs.String())
		}
		var books []source.Book
		if err := json.Unmarshal(out.Bytes(), &books); err != nil || len(books) != 1 || books[0].ID != "anna:0123456789abcdef0123456789abcdef" {
			t.Fatal(out.String(), err)
		}
	}
}
