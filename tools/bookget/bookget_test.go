package bookget

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bookget/ember"
	"bookget/gutenberg"
	"bookget/safehttp"
	"bookget/source"
	"bookget/virustotal"
)

func fixture(t *testing.T, extra bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	entries := map[string]string{"mimetype": "application/epub+zip", "META-INF/container.xml": `<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`, "content.opf": `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Dom Casmurro</dc:title><dc:creator>Machado de Assis</dc:creator></metadata></package>`}
	if extra {
		entries["../bad"] = "bad"
	}
	for name, data := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func TestPipeline(t *testing.T) {
	for _, tt := range []struct {
		name, format               string
		bad, malicious, convertBad bool
		wantErr                    bool
	}{
		{name: "save", format: "epub"}, {name: "convert", format: "pdf"}, {name: "unsafe input", format: "pdf", bad: true, wantErr: true}, {name: "malicious input", format: "epub", malicious: true, wantErr: true}, {name: "unsafe conversion", format: "pdf", convertBad: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := fixture(t, tt.bad)
			var base string
			vtCalls := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/vt/"):
					vtCalls++
					mal := 0
					if tt.malicious {
						mal = 1
					}
					fmt.Fprintf(w, `{"data":{"attributes":{"last_analysis_stats":{"malicious":%d}}}}`, mal)
				case r.URL.Path == "/book.epub":
					_, _ = w.Write(data)
				default:
					fmt.Fprint(w, `{"id":1,"title":"Fallback","authors":[{"name":"Fallback author"}],"copyright":false,"formats":{"application/epub+zip":"`+base+`/book.epub"}}`)
				}
			}))
			defer srv.Close()
			base = srv.URL
			u, _ := url.Parse(base)
			c := safehttp.New(u.Host)
			c.HTTP = srv.Client()
			output := t.TempDir()
			converted := false
			app := App{Sources: []source.Source{&gutenberg.Source{Base: base + "/books", Client: c}}, VT: &virustotal.Client{HTTP: c, Base: base + "/vt/", Key: "key"}, Convert: func(_ context.Context, in, out string) error {
				converted = true
				if _, err := os.Stat(in); err != nil {
					return err
				}
				body := []byte("%PDF-1.7\n")
				if tt.convertBad {
					body = []byte("HTML error")
				}
				return os.WriteFile(out, body, 0600)
			}}
			result, err := app.Get(context.Background(), "gutenberg:1", Options{Format: tt.format, OutputDir: output})
			if (err != nil) != tt.wantErr {
				t.Fatalf("%+v %v", result, err)
			}
			entries, e := os.ReadDir(output)
			if e != nil {
				t.Fatal(e)
			}
			if tt.wantErr {
				if len(entries) != 0 {
					t.Fatalf("leftover files: %v", entries)
				}
				if (tt.bad || tt.malicious) && converted {
					t.Fatal("converted unsafe input")
				}
				if tt.bad && vtCalls != 0 {
					t.Fatal("VT touched file before local checks")
				}
				return
			}
			if len(entries) != 1 || filepath.Base(result.Path) != "Dom Casmurro - Machado de Assis."+tt.format {
				t.Fatalf("%+v %v", result, entries)
			}
			before, e := os.Stat(result.Path)
			if e != nil {
				t.Fatal(e)
			}
			reused, e := app.Get(context.Background(), "gutenberg:1", Options{Format: tt.format, OutputDir: output})
			if e != nil || reused.Path != result.Path {
				t.Fatal(reused, e)
			}
			after, e := os.Stat(result.Path)
			if e != nil || !os.SameFile(before, after) {
				t.Fatal("existing file was replaced", e)
			}
			if e = os.WriteFile(result.Path, []byte("different existing content"), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = app.Get(context.Background(), "gutenberg:1", Options{Format: tt.format, OutputDir: output}); e == nil {
				t.Fatal("different existing content was accepted")
			}
			preserved, e := os.ReadFile(result.Path)
			if e != nil || string(preserved) != "different existing content" {
				t.Fatal("different file overwritten", e)
			}
		})
	}
}
func TestOfflineSendSaves(t *testing.T) {
	data := fixture(t, false)
	var base string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file" {
			_, _ = w.Write(data)
			return
		}
		fmt.Fprint(w, `{"id":1,"copyright":false,"formats":{"application/epub+zip":"`+base+`/file"}}`)
	}))
	defer srv.Close()
	base = srv.URL
	u, _ := url.Parse(base)
	c := safehttp.New(u.Host)
	c.HTTP = srv.Client()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"state":"unpaired"}`) }))
	defer local.Close()
	app := App{Sources: []source.Source{&gutenberg.Source{Base: base, Client: c}}, Ember: &ember.Client{Base: local.URL, HTTP: local.Client()}}
	r, err := app.Get(context.Background(), "gutenberg:1", Options{OutputDir: t.TempDir(), Send: true})
	if err != nil || r.Sent || r.Path == "" || len(r.Messages) == 0 {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestFilename(t *testing.T) {
	for _, title := range []string{"../../bad", "/absolute", "", strings.Repeat("ó", 300), "test\x00name"} {
		name := Filename(title, "../Author", "epub")
		if filepath.Base(name) != name || len(name) > 225 || strings.ContainsRune(name, 0) {
			t.Fatal(name)
		}
	}
}

func TestCalibreMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := Calibre(context.Background(), "in.epub", "out.pdf"); err == nil || !strings.Contains(err.Error(), "install Calibre") {
		t.Fatal(err)
	}
}

func TestFailedDownloadsLeaveNothing(t *testing.T) {
	for _, mode := range []string{"oversize", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			var base string
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/file" {
					if mode == "oversize" {
						w.Header().Set("Content-Length", fmt.Sprint(safehttp.MaxDownload+1))
					} else {
						w.Header().Set("Content-Length", "100000")
						_, _ = w.Write([]byte("partial"))
					}
					return
				}
				fmt.Fprint(w, `{"id":1,"copyright":false,"formats":{"application/epub+zip":"`+base+`/file"}}`)
			}))
			defer srv.Close()
			base = srv.URL
			u, _ := url.Parse(base)
			c := safehttp.New(u.Host)
			c.HTTP = srv.Client()
			dir := t.TempDir()
			app := App{Sources: []source.Source{&gutenberg.Source{Base: base, Client: c}}}
			if _, err := app.Get(context.Background(), "gutenberg:1", Options{OutputDir: dir}); err == nil {
				t.Fatal("download should fail")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) > 0 {
				t.Fatal(entries, err)
			}
		})
	}
}
