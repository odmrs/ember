package bookmeta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// mobiBook describes a PalmDB book for build.
type mobiBook struct {
	palmName string
	kind     string // type and creator, BOOKMOBI by default
	encoding uint32 // 1252 by default
	fullName []byte
	exth     []exthRecord
	// fullNameAt places the full name at this offset in record 0; zero puts
	// it right after the headers.
	fullNameAt int
}

type exthRecord struct {
	typ  uint32
	data string
}

func (m mobiBook) build() []byte {
	const mobiLen = 232
	be32 := binary.BigEndian.PutUint32
	if m.kind == "" {
		m.kind = "BOOKMOBI"
	}
	if m.encoding == 0 {
		m.encoding = 1252
	}

	rec0 := make([]byte, 16+mobiLen)
	binary.BigEndian.PutUint16(rec0, 1)
	copy(rec0[16:], "MOBI")
	be32(rec0[20:], mobiLen)
	be32(rec0[24:], 2)
	be32(rec0[28:], m.encoding)
	if len(m.exth) > 0 {
		be32(rec0[128:], 0x50)
		var body []byte
		for _, r := range m.exth {
			body = binary.BigEndian.AppendUint32(body, r.typ)
			body = binary.BigEndian.AppendUint32(body, uint32(8+len(r.data)))
			body = append(body, r.data...)
		}
		rec0 = append(rec0, "EXTH"...)
		rec0 = binary.BigEndian.AppendUint32(rec0, uint32(12+len(body)))
		rec0 = binary.BigEndian.AppendUint32(rec0, uint32(len(m.exth)))
		rec0 = append(rec0, body...)
		for len(rec0)%4 != 0 {
			rec0 = append(rec0, 0)
		}
	}
	if m.fullNameAt > len(rec0) {
		rec0 = append(rec0, make([]byte, m.fullNameAt-len(rec0))...)
	}
	be32(rec0[84:], uint32(len(rec0)))
	be32(rec0[88:], uint32(len(m.fullName)))
	rec0 = append(rec0, m.fullName...)
	rec0 = append(rec0, 0, 0)

	h := make([]byte, 78)
	copy(h[:31], m.palmName)
	copy(h[60:], m.kind)
	binary.BigEndian.PutUint16(h[76:], 2)
	start := uint32(78 + 2*8 + 2)
	h = binary.BigEndian.AppendUint32(h, start)
	h = binary.BigEndian.AppendUint32(h, 0)
	h = binary.BigEndian.AppendUint32(h, start+uint32(len(rec0)))
	h = binary.BigEndian.AppendUint32(h, 2)
	h = append(h, 0, 0)
	h = append(h, rec0...)
	return append(h, "the text of the book"...)
}

var exthBook = mobiBook{
	palmName: "Iracema_Old",
	fullName: []byte("Iracema (old name)"),
	exth: []exthRecord{
		{100, "Machado de Assis"},
		{101, "A Publisher"},
		{503, "A\xe7\xe3o \x97 \x93Iracema\x94 \x80 \xc7"},
		{100, "Jos\xe9 de Alencar"},
		{524, "pt"},
	},
}

func TestMOBIWithEXTH(t *testing.T) {
	m := read(t, exthBook.build(), "MOBI")
	checkMetadata(t, m, Metadata{
		Title:    "Ação — “Iracema” € Ç",
		Authors:  []string{"Machado de Assis", "José de Alencar"},
		Language: "pt",
	})
}

func TestMOBIFullName(t *testing.T) {
	b := mobiBook{palmName: "Dom_Casmurro", encoding: 65001, fullName: []byte("Dom Casmurro – edição")}.build()
	checkMetadata(t, read(t, b, "AZW3"), Metadata{Title: "Dom Casmurro – edição"})
}

func TestMOBIPalmName(t *testing.T) {
	b := mobiBook{palmName: "My_Book"}.build()
	checkMetadata(t, read(t, b, "PRC"), Metadata{Title: "My Book"})
}

func TestMOBIFullNameBeyondFirstRead(t *testing.T) {
	b := mobiBook{palmName: "x", fullName: []byte("Far Away"), fullNameAt: 100 << 10}.build()
	checkMetadata(t, read(t, b, "AZW"), Metadata{Title: "Far Away"})
}

func TestPalmDOC(t *testing.T) {
	b := mobiBook{palmName: "The_Time_Machine", kind: "TEXtREAd"}.build()
	checkMetadata(t, read(t, b, "PDB"), Metadata{Title: "The Time Machine"})
}

func TestMOBIOneReadForSmallBooks(t *testing.T) {
	b := exthBook.build()
	c := &countingReader{r: bytes.NewReader(b)}
	if _, err := Read(c, int64(len(b)), "MOBI"); err != nil {
		t.Fatal(err)
	}
	if c.calls != 1 {
		t.Errorf("%d round trips, want 1", c.calls)
	}
}

func TestMOBIMalformed(t *testing.T) {
	good := exthBook.build()
	for name, mutate := range map[string]func([]byte){
		"record 0 past the end": func(b []byte) { binary.BigEndian.PutUint32(b[78:], 1<<30) },
		"no MOBI magic":         func(b []byte) { copy(b[96+16:], "XXXX") },
		"full name past record": func(b []byte) { binary.BigEndian.PutUint32(b[96+84:], 1<<30) },
		"EXTH length too large": func(b []byte) { binary.BigEndian.PutUint32(b[96+16+232+4:], 1<<30) },
	} {
		b := bytes.Clone(good)
		mutate(b)
		if _, err := Read(bytes.NewReader(b), int64(len(b)), "MOBI"); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v, want ErrMalformed", name, err)
		}
	}
	b := bytes.Clone(good)
	copy(b[60:], "DATAXXXX")
	if _, err := Read(bytes.NewReader(b), int64(len(b)), "PDB"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unknown PalmDB type: %v, want ErrUnsupported", err)
	}
}
