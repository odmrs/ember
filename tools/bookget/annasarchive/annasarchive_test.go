package annasarchive

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"bookget/safehttp"
	"bookget/source"
)

const row = `<a href="/md5/0123456789abcdef0123456789abcdef"><div>Português [pt], epub, 1 MB</div><h3>No rastro da mentira</h3><div>Editora Exemplo, 2024</div><div class="truncate italic">Autora Exemplo</div></a>`

func TestParse(t *testing.T) {
	books, err := parse([]byte(row+row), "https://annas-archive.test")
	if err != nil || len(books) != 1 || books[0].Title != "No rastro da mentira" || books[0].Author != "Autora Exemplo" || !strings.Contains(books[0].Details, "Editora Exemplo") {
		t.Fatalf("%+v %v", books, err)
	}
	if _, err = parse([]byte("<h1>Verify you are human</h1>"), ""); err == nil {
		t.Fatal("challenge became empty results")
	}
	books, err = parse([]byte("No files found"), "")
	if err != nil || len(books) != 0 {
		t.Fatal(books, err)
	}
}
func TestDiscover(t *testing.T) {
	bases, err := discover([]byte(`<table class="infobox"><a href="https://annas-archive.pk/">Site</a><a href="https://evil.test">Fake</a></table>`))
	if err != nil || len(bases) != 1 || bases[0] != "https://annas-archive.pk" {
		t.Fatal(bases, err)
	}
	if _, err = discover([]byte(`<a href="https://annas-archive.pk">untrusted outside infobox</a>`)); err == nil {
		t.Fatal("accepted unrelated link")
	}
}
func TestSourceWorkflow(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != "no rastro da mentira" || r.URL.Query().Get("ext") != "epub" || r.URL.Query().Get("lang") != "pt" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, row)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	s := &Source{Base: srv.URL, Client: c, Language: "pt"}
	books, err := s.Search(context.Background(), "no rastro da mentira")
	if err != nil || len(books) != 1 {
		t.Fatal(books, err)
	}
}

func TestAuthenticatedSearchAndCurrentMarkup(t *testing.T) {
	const key = "local-test-secret-never-a-real-key"
	var base string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/account/" {
			if r.Method != "POST" || r.URL.RawQuery != "" || r.ParseForm() != nil || r.Form.Get("key") != key {
				t.Error("invalid login request")
			}
			http.SetCookie(w, &http.Cookie{Name: "aa_account_id2", Value: "test-session", Path: "/", Secure: true, HttpOnly: true})
			http.Redirect(w, r, "/account", 302)
			return
		}
		if r.URL.Path == "/account" {
			fmt.Fprint(w, "Account")
			return
		}
		cookie, err := r.Cookie("aa_account_id2")
		if err != nil || cookie.Value != "test-session" {
			t.Error("missing session cookie")
		}
		fmt.Fprint(w, `<div class="flex pt-3"><div><a class="js-vim-focus" href="/md5/0123456789abcdef0123456789abcdef">No rastro da mentira</a><a><span class="icon-[mdi--user-edit]"></span>Amy Tintera</a><a><span class="icon-[mdi--domain]"></span>Editora Arqueiro</a><div class="font-semibold text-sm leading-[1.2]">Portuguese [pt] · EPUB · 5.0MB · 2026</div></div></div>`)
	}))
	defer srv.Close()
	base = srv.URL
	u, _ := url.Parse(base)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	s := &Source{Base: base, Client: c, Key: key}
	books, err := s.Search(context.Background(), "no rastro da mentira")
	if err != nil || len(books) != 1 {
		t.Fatal(books, err)
	}
	b := books[0]
	if b.Title != "No rastro da mentira" || b.Author != "Amy Tintera" || b.Publisher != "Editora Arqueiro" || b.Year != "2026" {
		t.Fatalf("%+v", b)
	}
}
func TestRejectedKeyIsNotLogged(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "Log in") }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	s := &Source{Base: srv.URL, Client: c, Key: "secret-for-fake-only"}
	_, err := s.Search(context.Background(), "title")
	if err == nil || strings.Contains(err.Error(), s.Key) {
		t.Fatal("rejection missing or secret disclosed")
	}
}

func TestCurrentCardsExcludeScriptsAndKeepExactEditions(t *testing.T) {
	html := `<div class="flex pt-3"><a class="js-vim-focus" href="/md5/0123456789abcdef0123456789abcdef">No rastro da mentira</a><a><span class="icon-[mdi--company]"></span>Editora Arqueiro</a><div class="font-semibold text-sm">Portuguese [pt] · EPUB · 5.0MB · 2026<script>doNotLeakJavaScript()</script></div></div>`
	books, err := parse([]byte(html), "https://annas-archive.gl")
	if err != nil || len(books) != 1 || strings.Contains(books[0].Details, "JavaScript") || books[0].Publisher != "Editora Arqueiro" {
		t.Fatal(books, err)
	}
	unrelated := books[0]
	unrelated.ID = "anna:other"
	unrelated.Title = "Other book"
	books = append(books, unrelated)
	if got := matching(books, "no rastro da mentira"); len(got) != 1 {
		t.Fatal(got)
	}
}

func TestDownloadRejectsConversionAndInvalidRecords(t *testing.T) {
	s := &Source{}
	if _, err := s.Fetch(context.Background(), "anna:0123456789abcdef0123456789abcdef", "pdf"); err == nil || !strings.Contains(err.Error(), "conversion is disabled") {
		t.Fatal(err)
	}
	if _, err := s.Fetch(context.Background(), "anna:../../other", "epub"); err == nil {
		t.Fatal("invalid record accepted")
	}
}

func TestLanguageSelection(t *testing.T) {
	books := []source.Book{
		{ID: "pt", Title: "Verity", Details: "Portuguese [pt] · EPUB"},
		{ID: "en", Title: "Verity", Details: "English [en] · EPUB"},
		{ID: "br", Title: "Verity", Details: "Português [pt-BR] · EPUB"},
		{ID: "unknown", Title: "Verity", Details: "EPUB"},
	}
	for _, tt := range []struct {
		language string
		count    int
	}{{"pt", 2}, {"en", 1}, {"es", 0}, {"", 4}} {
		got := matchingLanguage(books, "verity", tt.language)
		if len(got) != tt.count {
			t.Fatalf("%q: %+v", tt.language, got)
		}
		values, err := url.ParseQuery(encodeSearch("verity & author", tt.language))
		if err != nil || values.Get("lang") != tt.language || values.Get("q") != "verity & author" || values.Get("ext") != "epub" {
			t.Fatal(values, err)
		}
	}
}
