package checker

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeEPUB(t *testing.T, extras map[string]string, mime string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entries := map[string]string{"mimetype": mime, "META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`, "OEBPS/content.opf": `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Dom Casmurro</dc:title><dc:creator>Machado de Assis</dc:creator></metadata></package>`, "OEBPS/chapter.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Hello</p></body></html>`}
	for k, v := range extras {
		entries[k] = v
	}
	for name, data := range entries {
		entry, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = entry.Write([]byte(data)); e != nil {
			t.Fatal(e)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestEPUB(t *testing.T) {
	for _, tt := range []struct {
		name   string
		extras map[string]string
		mime   string
		want   string
	}{
		{"valid", nil, "application/epub+zip", ""},
		{"traversal", map[string]string{"../evil": "x"}, "application/epub+zip", "path traversal"},
		{"absolute", map[string]string{"/evil": "x"}, "application/epub+zip", "path traversal"},
		{"windows", map[string]string{`C:\evil`: "x"}, "application/epub+zip", "path traversal"},
		{"backslash", map[string]string{`..\evil`: "x"}, "application/epub+zip", "path traversal"},
		{"bomb", map[string]string{"bomb": strings.Repeat("a", 1<<20)}, "application/epub+zip", "zip bomb"},
		{"script", map[string]string{"OEBPS/chapter.xhtml": `<html><ScRiPt>alert(1)</ScRiPt></html>`}, "application/epub+zip", "script in XHTML"},
		{"externalJS", map[string]string{"evil.js": "alert(1)"}, "application/epub+zip", "executable content"},
		{"wrong mimetype", nil, "text/plain", "wrong EPUB mimetype"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := makeEPUB(t, tt.extras, tt.mime)
			r, err := Check(p, "epub")
			if tt.want == "" {
				if err != nil || r.Title != "Dom Casmurro" || r.Author != "Machado de Assis" {
					t.Fatalf("%+v %v", r, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error()+strings.Join(r.Findings, " "), tt.want) {
				t.Fatalf("want %s: %+v %v", tt.want, r, err)
			}
		})
	}
}
func TestSymlink(t *testing.T) {
	p := makeEPUB(t, nil, "application/epub+zip")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	// Construct a fresh archive with a symlink; detection precedes metadata reads.
	p = filepath.Join(t.TempDir(), "link.epub")
	f, err = os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0777)
	entry, err := w.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("target"))
	_ = w.Close()
	_ = f.Close()
	r, _ := Check(p, "epub")
	if !strings.Contains(strings.Join(r.Findings, " "), "symlink") {
		t.Fatal(r)
	}
}
func TestMagic(t *testing.T) {
	for _, format := range []string{"epub", "pdf", "azw3"} {
		p := filepath.Join(t.TempDir(), "bad")
		if err := os.WriteFile(p, []byte("<html>error</html>"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Check(p, format); err == nil {
			t.Fatal("accepted wrong magic", format)
		}
	}
}

func TestActiveAttributes(t *testing.T) {
	p := makeEPUB(t, map[string]string{"OEBPS/chapter.xhtml": `<html><body onload="alert(1)">text</body></html>`}, "application/epub+zip")
	r, err := Check(p, "epub")
	if err == nil || !strings.Contains(strings.Join(r.Findings, " "), "active attribute") {
		t.Fatal(r, err)
	}
}

func TestManifestMarkupRegardlessOfExtension(t *testing.T) {
	for _, tt := range []struct {
		name, href, file, mime, content string
		blocked                         bool
	}{
		{"xhtml dat", "chapter.dat", "OEBPS/chapter.dat", "application/xhtml+xml", `<html><script>bad()</script></html>`, true},
		{"extensionless svg", "picture", "OEBPS/picture", "image/svg+xml", `<svg onload="bad()"/>`, true},
		{"escaped href", "chapter%20one.bin", "OEBPS/chapter one.bin", "application/xhtml+xml", `<html><body onload="bad()"/></html>`, true},
		{"relative href", "../chapter.bin", "chapter.bin", "application/xhtml+xml", `<html><script/></html>`, true},
		{"safe renamed chapter", "chapter.dat", "OEBPS/chapter.dat", "application/xhtml+xml", `<html><body><p>Safe</p></body></html>`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opf := `<package><metadata><title>Title</title></metadata><manifest><item href="` + tt.href + `" media-type="` + tt.mime + `"/></manifest></package>`
			file := makeEPUB(t, map[string]string{"OEBPS/content.opf": opf, tt.file: tt.content}, "application/epub+zip")
			report, err := Check(file, "epub")
			if (err != nil) != tt.blocked {
				t.Fatal(report, err)
			}
			if tt.blocked && !strings.Contains(strings.Join(report.Findings, " "), "unsafe XHTML") {
				t.Fatal(report, err)
			}
		})
	}
}

func TestInspectEveryOPFRootfile(t *testing.T) {
	file := makeEPUB(t, map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/><rootfile full-path="Other/content.opf"/></rootfiles></container>`,
		"Other/content.opf":      `<package><manifest><item href="chapter.dat" media-type="application/xhtml+xml"/></manifest></package>`,
		"Other/chapter.dat":      `<html><script/></html>`,
	}, "application/epub+zip")
	report, err := Check(file, "epub")
	if err == nil || !strings.Contains(strings.Join(report.Findings, " "), "Other/chapter.dat") {
		t.Fatal(report, err)
	}
}
