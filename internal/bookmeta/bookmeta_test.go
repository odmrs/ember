package bookmeta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"slices"
	"testing"
)

// countingReader counts the calls and bytes read through it.
type countingReader struct {
	r     io.ReaderAt
	calls int
	bytes int64
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.calls++
	c.bytes += int64(n)
	return n, err
}

// tailFile is a file of size bytes, all zero except tail at its end.
type tailFile struct {
	size int64
	tail []byte
}

func (f tailFile) ReadAt(p []byte, off int64) (int, error) {
	return patternFile{size: f.size, at: func(i int64) byte {
		if base := f.size - int64(len(f.tail)); i >= base {
			return f.tail[i-base]
		}
		return 0
	}}.ReadAt(p, off)
}

// patternFile is a file whose bytes are computed.
type patternFile struct {
	size int64
	at   func(i int64) byte
}

func (f patternFile) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for ; n < len(p) && off+int64(n) < f.size; n++ {
		p[n] = f.at(off + int64(n))
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func read(t *testing.T, b []byte, format string) Metadata {
	t.Helper()
	m, err := Read(bytes.NewReader(b), int64(len(b)), format)
	if err != nil {
		t.Fatalf("Read(%s): %v", format, err)
	}
	return m
}

func checkMetadata(t *testing.T, got, want Metadata) {
	t.Helper()
	if got.Title != want.Title || got.Language != want.Language || !slices.Equal(got.Authors, want.Authors) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestReadUnsupported(t *testing.T) {
	for _, f := range []string{"", "TXT", "CBZ", "DJVU"} {
		if _, err := Read(bytes.NewReader(nil), 0, f); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Read(%q) = %v, want ErrUnsupported", f, err)
		}
	}
}

func TestReaderFetchesInBlocks(t *testing.T) {
	data := make([]byte, 1<<20)
	c := &countingReader{r: bytes.NewReader(data)}
	r := newReader(c, int64(len(data)))
	p := make([]byte, 100)
	for _, off := range []int64{0, 10, 5000, 30000} {
		if _, err := r.ReadAt(p, off); err != nil {
			t.Fatal(err)
		}
	}
	if c.calls != 1 {
		t.Errorf("small reads in one block took %d round trips", c.calls)
	}
	if n, err := r.ReadAt(p, int64(len(data))-10); n != 10 || err != io.EOF {
		t.Errorf("read past the end = %d, %v; want 10, EOF", n, err)
	}
}

func TestReaderBudget(t *testing.T) {
	c := &countingReader{r: tailFile{size: 100 << 20}}
	r := newReader(c, 100<<20)
	p := make([]byte, 1<<20)
	var err error
	n := 0
	for ; n < 10; n++ {
		if _, err = r.ReadAt(p, int64(n)<<21); err != nil {
			break
		}
	}
	if !errors.Is(err, ErrBudget) || n != MaxRead>>20 {
		t.Errorf("after %d reads of 1 MiB: %v, want ErrBudget after %d", n, err, MaxRead>>20)
	}
	if c.bytes > MaxRead {
		t.Errorf("read %d bytes, more than MaxRead", c.bytes)
	}
}

// A zip whose central directory claims 65535 entries of 246 bytes each must
// not be read past MaxRead.
func TestHostileZipDirectory(t *testing.T) {
	const records, nameLen = 0xffff, 200
	entry := make([]byte, 46+nameLen)
	binary.LittleEndian.PutUint32(entry, 0x02014b50)
	binary.LittleEndian.PutUint16(entry[4:], 20)
	binary.LittleEndian.PutUint16(entry[6:], 20)
	binary.LittleEndian.PutUint16(entry[28:], nameLen)
	copy(entry[46:], bytes.Repeat([]byte("a"), nameLen))
	dirSize := int64(records * len(entry))
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], records)
	binary.LittleEndian.PutUint16(end[10:], records)
	binary.LittleEndian.PutUint32(end[12:], uint32(dirSize))
	size := dirSize + int64(len(end))
	c := &countingReader{r: patternFile{size: size, at: func(i int64) byte {
		if i >= dirSize {
			return end[i-dirSize]
		}
		return entry[i%int64(len(entry))]
	}}}
	_, err := Read(c, size, "EPUB")
	if !errors.Is(err, ErrBudget) {
		t.Errorf("Read = %v, want ErrBudget", err)
	}
	if c.bytes > MaxRead {
		t.Errorf("read %d bytes, more than MaxRead", c.bytes)
	}
}

func TestLargeBooksReadLittle(t *testing.T) {
	pdf := buildPDF(50<<20-2000, "/Info 2 0 R", "<< /Type /Catalog >>", "<< /Title (Big) >>")
	big := make([]byte, 20<<20)
	epub := buildEPUB(t, "content.opf", epub2OPF, zipEntry{name: "big.bin", body: string(big), store: true})
	for _, tc := range []struct {
		format string
		r      io.ReaderAt
		size   int64
	}{
		{"PDF", tailFile{size: 50 << 20, tail: pdf}, 50 << 20},
		{"EPUB", bytes.NewReader(epub), int64(len(epub))},
	} {
		c := &countingReader{r: tc.r}
		m, err := Read(c, tc.size, tc.format)
		if err != nil || m.Title == "" {
			t.Errorf("%s: %#v, %v", tc.format, m, err)
		}
		if c.bytes > 200<<10 {
			t.Errorf("%s: read %d KiB of %d MiB", tc.format, c.bytes>>10, tc.size>>20)
		}
	}
}

type fixture struct {
	name, format string
	data         []byte
}

func fixtures(t testing.TB) []fixture {
	return []fixture{
		{"epub2", "EPUB", buildEPUB(t, "OEBPS/content.opf", epub2OPF)},
		{"epub3", "EPUB", buildEPUB(t, "EPUB/package.opf", epub3OPF)},
		{"mobi", "MOBI", exthBook.build()},
		{"mobi-big-record0", "AZW3", mobiBook{palmName: "x", fullName: []byte("Far"), fullNameAt: 100 << 10}.build()},
		{"palmdoc", "PDB", mobiBook{palmName: "The_Time_Machine", kind: "TEXtREAd"}.build()},
		{"pdf", "PDF", classicPDF},
		{"pdf-xref-stream", "PDF", buildXrefStreamPDF("<< /Title (Streamed) /Author (A. Writer) >>", false)},
	}
}

// Truncated, mutated and random books give an error or some metadata, never
// a panic or an error outside the package's own.
func TestReadNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	try := func(name, format string, b []byte) {
		_, err := Read(bytes.NewReader(b), int64(len(b)), format)
		if err != nil && !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrBudget) && !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s (%d bytes): unexpected error %v", name, len(b), err)
		}
	}
	for _, f := range fixtures(t) {
		step := max(1, len(f.data)/500)
		for n := 0; n <= len(f.data); n += step {
			try(f.name, f.format, f.data[:n])
		}
		for range 300 {
			b := slices.Clone(f.data)
			for range 1 + rng.IntN(8) {
				b[rng.IntN(len(b))] = byte(rng.Uint32())
			}
			try(f.name+" mutated", f.format, b)
		}
	}
	for _, format := range []string{"EPUB", "MOBI", "PDF"} {
		for range 300 {
			b := make([]byte, rng.IntN(4096))
			for i := range b {
				b[i] = byte(rng.Uint32())
			}
			try("random", format, b)
		}
	}
}

func FuzzRead(f *testing.F) {
	formats := []string{"EPUB", "MOBI", "PDF"}
	for _, fx := range fixtures(f) {
		f.Add(fx.data, uint8(slices.Index(formats, fx.format)))
	}
	f.Fuzz(func(t *testing.T, b []byte, format uint8) {
		_, err := Read(bytes.NewReader(b), int64(len(b)), formats[int(format)%len(formats)])
		if err != nil && !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrBudget) && !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unexpected error %v", err)
		}
	})
}
