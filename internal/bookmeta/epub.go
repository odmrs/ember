package bookmeta

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// How much of each EPUB part is read, decompressed. A bigger part is read
// up to the cap and parsed as far as it goes: the metadata comes first.
const (
	maxContainer = 64 << 10
	maxOPF       = 1 << 20
)

func readEPUB(r *reader) (Metadata, error) {
	z, err := zip.NewReader(r, r.size)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return Metadata{}, err
	}
	var opf *zip.File
	if f := findFile(z, "META-INF/container.xml"); f != nil {
		b, err := readZipped(f, maxContainer)
		if err != nil {
			return Metadata{}, err
		}
		if p := rootfile(b); p != "" {
			opf = findFile(z, p)
		}
	}
	if opf == nil {
		for _, f := range z.File {
			if strings.EqualFold(path.Ext(f.Name), ".opf") {
				opf = f
				break
			}
		}
	}
	if opf == nil {
		return Metadata{}, malformed("no package document")
	}
	b, err := readZipped(opf, maxOPF)
	if err != nil {
		return Metadata{}, err
	}
	return parseOPF(b), nil
}

// findFile finds name in z, ignoring leading slashes, and then case.
func findFile(z *zip.Reader, name string) *zip.File {
	clean := func(s string) string { return path.Clean("/" + strings.ReplaceAll(s, `\`, "/"))[1:] }
	name = clean(name)
	var folded *zip.File
	for _, f := range z.File {
		n := clean(f.Name)
		if n == name {
			return f
		}
		if folded == nil && strings.EqualFold(n, name) {
			folded = f
		}
	}
	return folded
}

// readZipped reads up to max bytes of f, decompressed.
func readZipped(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, max))
	if errors.Is(err, zip.ErrChecksum) {
		err = nil
	}
	return b, err
}

// rootfile returns the full-path of the first rootfile in container.xml.
func rootfile(container []byte) string {
	d := newXMLDecoder(container)
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		if t, ok := tok.(xml.StartElement); ok && t.Name.Local == "rootfile" {
			if p := attr(t, "full-path"); p != "" {
				return p
			}
		}
	}
}

// opfEntry is a title or creator, with what its attributes and the <meta
// refines> elements pointing at it say about it.
type opfEntry struct {
	text      string
	roles     []string
	titleType string
}

func (e *opfEntry) isAuthor() bool {
	for _, r := range e.roles {
		if strings.EqualFold(strings.TrimSpace(r), "aut") {
			return true
		}
	}
	return len(e.roles) == 0
}

// parseOPF reads the <metadata> of an OPF package document, EPUB 2 or 3. It
// matches elements by local name, whatever their namespace prefix, and keeps
// what it read before any syntax error.
func parseOPF(b []byte) Metadata {
	var (
		m        Metadata
		titles   []*opfEntry
		creators []*opfEntry
		byID     = map[string]*opfEntry{}
		refines  [][3]string // id, property, value
	)
	d := newXMLDecoder(b)
	depth := 0 // inside <metadata> when > 0
scan:
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				if t.Name.Local == "metadata" {
					depth = 1
				}
				continue
			}
			switch t.Name.Local {
			case "title", "creator":
				e := &opfEntry{text: innerText(d)}
				if role := attr(t, "role"); role != "" {
					e.roles = append(e.roles, role)
				}
				if id := attr(t, "id"); id != "" {
					byID[id] = e
				}
				if t.Name.Local == "title" {
					titles = append(titles, e)
				} else {
					creators = append(creators, e)
				}
			case "language":
				if s := innerText(d); m.Language == "" {
					m.Language = s
				}
			case "meta":
				if ref := attr(t, "refines"); ref != "" {
					refines = append(refines, [3]string{strings.TrimPrefix(ref, "#"), attr(t, "property"), innerText(d)})
				} else {
					depth++
				}
			default:
				depth++
			}
		case xml.EndElement:
			if depth == 0 {
				continue
			}
			if depth--; depth == 0 {
				break scan
			}
		}
	}
	for _, r := range refines {
		e := byID[r[0]]
		if e == nil {
			continue
		}
		switch strings.TrimSpace(r[1]) {
		case "role":
			e.roles = append(e.roles, r[2])
		case "title-type":
			e.titleType = strings.TrimSpace(r[2])
		}
	}
	for _, e := range titles {
		if e.titleType == "main" && e.text != "" {
			m.Title = e.text
			break
		}
	}
	for _, e := range titles {
		if m.Title == "" && e.text != "" {
			m.Title = e.text
		}
	}
	for _, e := range creators {
		if e.isAuthor() {
			m.Authors = append(m.Authors, e.text)
		}
	}
	return m
}

func newXMLDecoder(b []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "iso-8859-1", "latin1", "windows-1252", "cp1252", "us-ascii", "ascii":
			b, err := io.ReadAll(in)
			return strings.NewReader(decodeCP1252(b)), err
		}
		return nil, fmt.Errorf("unknown charset %q", label)
	}
	return d
}

// innerText reads the text inside the element just started, up to its end.
func innerText(d *xml.Decoder) string {
	var s strings.Builder
	for depth := 1; depth > 0; {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			s.Write(t)
		}
	}
	return strings.TrimSpace(s.String())
}

func attr(t xml.StartElement, local string) string {
	for _, a := range t.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}
