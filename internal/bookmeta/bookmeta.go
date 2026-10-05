// Package bookmeta reads the title, authors and language a Book declares
// about itself. It reads through an io.ReaderAt that may be a file on the
// Device, where every ReadAt is a round trip, so it reads only the parts that
// hold the metadata, in few large reads, and never more than MaxRead bytes of
// one Book whatever the file claims about itself.
package bookmeta

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// MaxRead bounds the bytes Read fetches from one Book.
const MaxRead = 4 << 20

// readAhead is the least one fetch asks for, so the small sequential reads
// of archive/zip and of the parsers cost one round trip, not one each.
const readAhead = 64 << 10

// Errors returned by Read. Errors from the io.ReaderAt are returned as they
// are.
var (
	ErrUnsupported = errors.New("bookmeta: unsupported format")
	ErrBudget      = errors.New("bookmeta: the metadata lies too deep in the file")
	ErrMalformed   = errors.New("bookmeta: malformed book")
)

// Metadata is what a Book says about itself. Empty fields were not found.
type Metadata struct {
	Title string
	// Authors are in display order, as written in the book: "Last, First"
	// stays as it is.
	Authors  []string
	Language string
}

// Read reads the metadata of a book in the given format, the upper-case
// extension as library.BookFormat returns it, from r, which holds size
// bytes. It never reads more than MaxRead bytes in total; formats it does not
// know give ErrUnsupported.
func Read(r io.ReaderAt, size int64, format string) (Metadata, error) {
	var parse func(*reader) (Metadata, error)
	switch strings.ToUpper(format) {
	case "EPUB":
		parse = readEPUB
	case "MOBI", "AZW3", "AZW", "PRC", "PDB":
		parse = readMOBI
	case "PDF":
		parse = readPDF
	default:
		return Metadata{}, ErrUnsupported
	}
	if size < 0 {
		return Metadata{}, fmt.Errorf("%w: negative size", ErrMalformed)
	}
	br := newReader(r, size)
	m, err := parse(br)
	if err != nil {
		return Metadata{}, br.cause(err)
	}
	return m.clean(), nil
}

func (m Metadata) clean() Metadata {
	out := Metadata{Title: cleanText(m.Title), Language: cleanText(m.Language)}
	for _, a := range m.Authors {
		if a = cleanText(a); a != "" {
			out.Authors = append(out.Authors, a)
		}
	}
	return out
}

// cleanText trims s and collapses its runs of white space and control
// characters into single spaces.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
	return strings.Join(strings.Fields(s), " ")
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrMalformed}, args...)...)
}

// reader is the io.ReaderAt the formats read through. It keeps everything it
// fetched, so going back to a part already read costs nothing, and refuses
// to fetch past MaxRead in total.
type reader struct {
	r      io.ReaderAt
	size   int64
	left   int64
	chunks []chunk
	over   bool  // a fetch was refused for the budget
	ioErr  error // the first error from r
}

type chunk struct {
	off int64
	b   []byte
}

func newReader(r io.ReaderAt, size int64) *reader {
	return &reader{r: r, size: size, left: MaxRead}
}

func (r *reader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, malformed("negative offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= r.size {
		return 0, io.EOF
	}
	b, err := r.fetch(off, int(min(int64(len(p)), r.size-off)))
	if err != nil {
		return 0, err
	}
	n := copy(p, b)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// at returns up to n bytes at off, fewer only at the end of the file. The
// result belongs to the reader and must not be changed.
func (r *reader) at(off int64, n int) ([]byte, error) {
	if off < 0 || off > r.size || n < 0 {
		return nil, malformed("offset %d outside the file", off)
	}
	return r.fetch(off, int(min(int64(n), r.size-off)))
}

// fetch returns the n bytes at off, which lie inside the file.
func (r *reader) fetch(off int64, n int) ([]byte, error) {
	end := off + int64(n)
	for _, c := range r.chunks {
		if off >= c.off && end <= c.off+int64(len(c.b)) {
			return c.b[off-c.off : end-c.off], nil
		}
	}
	if int64(n) > r.left {
		r.over = true
		return nil, ErrBudget
	}
	b := make([]byte, min(max(int64(n), readAhead), r.left, r.size-off))
	r.left -= int64(len(b))
	got, err := r.r.ReadAt(b, off)
	if got < n {
		if err == nil || err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		if r.ioErr == nil {
			r.ioErr = err
		}
		return nil, err
	}
	r.chunks = append(r.chunks, chunk{off, b[:got]})
	return b[:n], nil
}

// broken reports whether reading cannot go on: the budget is spent or r
// failed.
func (r *reader) broken() bool {
	return r.over || r.ioErr != nil
}

// cause turns a parser's error into the one Read returns.
func (r *reader) cause(err error) error {
	switch {
	case r.ioErr != nil:
		return r.ioErr
	case r.over:
		return ErrBudget
	case errors.Is(err, ErrMalformed), errors.Is(err, ErrUnsupported):
		return err
	}
	return fmt.Errorf("%w: %v", ErrMalformed, err)
}

// cp1252 maps the bytes 0x80–0x9F of Windows-1252; zero marks the five it
// leaves undefined. Every other byte is the Latin-1 code point.
var cp1252 = [32]rune{
	'€', 0, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0, 'Ž', 0,
	0, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0, 'ž', 'Ÿ',
}

func decodeCP1252(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		r := rune(c)
		if c >= 0x80 && c < 0xa0 && cp1252[c-0x80] != 0 {
			r = cp1252[c-0x80]
		}
		s.WriteRune(r)
	}
	return s.String()
}
