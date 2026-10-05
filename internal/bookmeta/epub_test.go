package bookmeta

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

type zipEntry struct {
	name, body string
	store      bool
}

func buildZip(t testing.TB, entries ...zipEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		method := zip.Deflate
		if e.store {
			method = zip.Store
		}
		f, err := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// buildEPUB makes an EPUB whose container points at rootfile, with the OPF
// stored at rootfile without its leading slash, followed by extra.
func buildEPUB(t testing.TB, rootfile, opf string, extra ...zipEntry) []byte {
	container := `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="` + rootfile + `" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
	entries := []zipEntry{
		{name: "mimetype", body: "application/epub+zip", store: true},
		{name: "META-INF/container.xml", body: container},
	}
	entries = append(entries, extra...)
	entries = append(entries, zipEntry{name: strings.TrimPrefix(rootfile, "/"), body: opf})
	return buildZip(t, entries...)
}

const epub2OPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Nineteen
      Eighty-Four</dc:title>
    <dc:creator opf:role="aut" opf:file-as="Orwell, George">George   Orwell</dc:creator>
    <dc:creator opf:role="ill">Some Illustrator</dc:creator>
    <dc:creator opf:file-as="Huxley, Aldous">Aldous Huxley</dc:creator>
    <dc:language>en</dc:language>
    <meta name="cover" content="cover-image"/>
  </metadata>
  <manifest/>
</package>`

const epub3OPF = `<?xml version="1.0" encoding="UTF-8"?>
<opf:package xmlns:opf="http://www.idpf.org/2007/opf" version="3.0">
  <opf:metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title id="t2">A Subtitle That Comes First</dc:title>
    <dc:title id="t1">The Left Hand of Darkness</dc:title>
    <opf:meta refines="#t2" property="title-type">subtitle</opf:meta>
    <opf:meta refines="#t1" property="title-type">main</opf:meta>
    <dc:creator id="c1">Ursula K. Le Guin</dc:creator>
    <opf:meta refines="#c1" property="role" scheme="marc:relators">aut</opf:meta>
    <opf:meta refines="#c1" property="file-as">Le Guin, Ursula K.</opf:meta>
    <dc:creator id="c2">Tra Nslator</dc:creator>
    <opf:meta refines="#c2" property="role" scheme="marc:relators">trl</opf:meta>
    <dc:creator id="c3">Ed Itor</dc:creator>
    <opf:meta refines="#c3" property="role" scheme="marc:relators">edt</opf:meta>
    <dc:creator id="c4">Plain Creator</dc:creator>
    <dc:language>pt-BR</dc:language>
    <opf:meta property="dcterms:modified">2020-01-01T00:00:00Z</opf:meta>
  </opf:metadata>
</opf:package>`

func TestEPUB2(t *testing.T) {
	m := read(t, buildEPUB(t, "OEBPS/content.opf", epub2OPF), "EPUB")
	checkMetadata(t, m, Metadata{
		Title:    "Nineteen Eighty-Four",
		Authors:  []string{"George Orwell", "Aldous Huxley"},
		Language: "en",
	})
}

func TestEPUB3(t *testing.T) {
	m := read(t, buildEPUB(t, "EPUB/package.opf", epub3OPF), "EPUB")
	checkMetadata(t, m, Metadata{
		Title:    "The Left Hand of Darkness",
		Authors:  []string{"Ursula K. Le Guin", "Plain Creator"},
		Language: "pt-BR",
	})
}

func TestEPUBPaths(t *testing.T) {
	opf := `<package><metadata><title>Found</title></metadata></package>`
	for name, b := range map[string][]byte{
		"rootfile with leading slash": buildEPUB(t, "/OPS/book.opf", opf),
		"zip name with leading slash": buildZip(t,
			zipEntry{name: "META-INF/container.xml", body: `<container><rootfiles><rootfile full-path="OPS/book.opf"/></rootfiles></container>`},
			zipEntry{name: "/OPS/book.opf", body: opf}),
		"no container": buildZip(t, zipEntry{name: "OPS/book.opf", body: opf}),
		"other case": buildZip(t,
			zipEntry{name: "META-INF/container.xml", body: `<container><rootfiles><rootfile full-path="ops/Book.opf"/></rootfiles></container>`},
			zipEntry{name: "OPS/book.opf", body: opf}),
	} {
		if m := read(t, b, "EPUB"); m.Title != "Found" {
			t.Errorf("%s: title %q", name, m.Title)
		}
	}
}

func TestEPUBTitlesAndRoles(t *testing.T) {
	opf := `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata>
		<dc:title>First &amp; Only</dc:title>
		<dc:title>Second</dc:title>
		<dc:creator id="a" role="ill">Drawer</dc:creator>
		<dc:creator id="b">Editor</dc:creator>
		<meta refines="#b" property="role">edt</meta>
	</metadata></package>`
	m := read(t, buildEPUB(t, "a.opf", opf), "EPUB")
	checkMetadata(t, m, Metadata{Title: "First & Only"})
}

func TestEPUBWithoutOPF(t *testing.T) {
	b := buildZip(t, zipEntry{name: "mimetype", body: "application/epub+zip"})
	if _, err := Read(bytes.NewReader(b), int64(len(b)), "EPUB"); err == nil {
		t.Error("an EPUB without a package document read fine")
	}
}
