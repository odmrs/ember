package bookmeta

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

const (
	palmHeaderLen = 78
	// record0Read is how much of record 0 is read at first. The MOBI header
	// and the EXTH after it rarely pass a few KiB; more is read if they do.
	record0Read = 16 << 10
	// maxRecord0 bounds how far into record 0 the metadata may lie.
	maxRecord0 = 1 << 20
)

// EXTH record types.
const (
	exthAuthor   = 100
	exthTitle    = 503
	exthLanguage = 524
)

// readMOBI reads a PalmDB book: MOBI, AZW, AZW3 and PRC are BOOKMOBI
// databases with a MOBI header and EXTH in record 0; a PalmDOC (TEXtREAd)
// has only the database name.
func readMOBI(r *reader) (Metadata, error) {
	h, err := r.at(0, palmHeaderLen+16)
	if err != nil {
		return Metadata{}, err
	}
	if len(h) < palmHeaderLen {
		return Metadata{}, malformed("short PalmDB header")
	}
	name := h[:32]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	palmName := strings.ReplaceAll(decodeCP1252(name), "_", " ")
	switch string(h[60:68]) {
	case "TEXtREAd":
		return Metadata{Title: palmName}, nil
	case "BOOKMOBI":
	default:
		return Metadata{}, ErrUnsupported
	}

	records := binary.BigEndian.Uint16(h[76:])
	if records == 0 || len(h) < palmHeaderLen+8 {
		return Metadata{}, malformed("no records")
	}
	start, end := int64(be32(h[78:])), r.size
	if records > 1 && len(h) >= palmHeaderLen+16 {
		end = int64(be32(h[86:]))
	}
	if start < palmHeaderLen || end <= start || end > r.size {
		return Metadata{}, malformed("record 0 at %d–%d", start, end)
	}
	rec := &record0{r: r, off: start, limit: min(end-start, maxRecord0)}
	if rec.b, err = r.at(start, int(min(rec.limit, record0Read))); err != nil {
		return Metadata{}, err
	}
	if err := rec.need(24); err != nil {
		return Metadata{}, err
	}
	b := rec.b
	if string(b[16:20]) != "MOBI" {
		return Metadata{}, malformed("no MOBI header")
	}
	headerEnd := 16 + int64(be32(b[20:]))
	decode := decodeCP1252
	if be32(b[28:]) == 65001 {
		decode = func(b []byte) string { return strings.ToValidUTF8(string(b), string(utf8.RuneError)) }
	}

	var m Metadata
	var fullName, updated string
	if headerEnd >= 92 {
		if err := rec.need(92); err != nil {
			return Metadata{}, err
		}
		off, n := int64(be32(rec.b[84:])), int64(be32(rec.b[88:]))
		if n > 0 {
			if err := rec.need(off + n); err != nil {
				return Metadata{}, err
			}
			fullName = decode(rec.b[off : off+n])
		}
	}
	if headerEnd >= 132 {
		if err := rec.need(132); err != nil {
			return Metadata{}, err
		}
		if be32(rec.b[128:])&0x40 != 0 {
			if err := rec.need(headerEnd + 12); err != nil {
				return Metadata{}, err
			}
			x := rec.b[headerEnd:]
			if string(x[:4]) != "EXTH" {
				return Metadata{}, malformed("no EXTH header")
			}
			length, count := int64(be32(x[4:])), be32(x[8:])
			if length < 12 {
				return Metadata{}, malformed("EXTH length %d", length)
			}
			if err := rec.need(headerEnd + length); err != nil {
				return Metadata{}, err
			}
			x = rec.b[headerEnd : headerEnd+length]
			for p := int64(12); count > 0 && p+8 <= length; count-- {
				typ, n := be32(x[p:]), int64(be32(x[p+4:]))
				if n < 8 || p+n > length {
					return Metadata{}, malformed("EXTH record of %d bytes", n)
				}
				data := decode(x[p+8 : p+n])
				switch typ {
				case exthAuthor:
					m.Authors = append(m.Authors, data)
				case exthTitle:
					updated = data
				case exthLanguage:
					m.Language = data
				}
				p += n
			}
		}
	}
	for _, t := range []string{updated, fullName, palmName} {
		if m.Title = cleanText(t); m.Title != "" {
			break
		}
	}
	return m, nil
}

// record0 is the start of MOBI record 0, read further on demand.
type record0 struct {
	r     *reader
	off   int64
	limit int64 // the bytes of record 0 that may be read
	b     []byte
}

// need makes the first n bytes of the record available in b.
func (rec *record0) need(n int64) error {
	if n <= int64(len(rec.b)) {
		return nil
	}
	if n > rec.limit {
		return malformed("record 0 needs %d bytes of %d", n, rec.limit)
	}
	more, err := rec.r.at(rec.off+int64(len(rec.b)), int(n)-len(rec.b))
	if err != nil {
		return err
	}
	if int64(len(rec.b)+len(more)) < n {
		return malformed("record 0 is cut short")
	}
	rec.b = append(rec.b[:len(rec.b):len(rec.b)], more...)
	return nil
}

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
