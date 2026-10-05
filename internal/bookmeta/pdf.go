package bookmeta

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// How much of a PDF is read at each step.
const (
	pdfTailLen = 64 << 10 // the end of the file: startxref, the trailer, often the Info
	pdfHeadLen = 64 << 10 // the start, searched when the xref does not lead to the Info
	pdfXrefLen = 64 << 10 // one xref section
	pdfObjLen  = 16 << 10 // the Info object
	// maxXrefStream and maxXrefData bound an xref stream, compressed and not.
	maxXrefStream = 1 << 20
	maxXrefData   = 4 << 20
	// maxXrefSections bounds the /Prev links followed.
	maxXrefSections = 8
	// maxPDFDepth bounds the nesting of arrays and dictionaries.
	maxPDFDepth = 32
)

type (
	pdfDict    map[string]any
	pdfName    string
	pdfString  []byte
	pdfKeyword string
	pdfRef     struct{ num, gen int64 }
)

// objLoc is where an xref section puts an object.
type objLoc struct {
	off      int64
	gen      int64
	inStream bool // inside an object stream, out of reach without decoding it
}

// readPDF reads /Title and /Author from the document information dictionary.
// It follows startxref to the trailer and the xref section that locates the
// dictionary; when that fails it searches the start and end of the file for
// the object itself.
func readPDF(r *reader) (Metadata, error) {
	tail, err := r.at(max(0, r.size-pdfTailLen), pdfTailLen)
	if err != nil {
		return Metadata{}, err
	}
	var (
		trailers []pdfDict
		sections []map[int64]objLoc
		info     *pdfRef
	)
	locate := func() (objLoc, bool) {
		for _, s := range sections {
			if loc, ok := s[info.num]; ok {
				return loc, true
			}
		}
		return objLoc{}, false
	}
	seen := map[int64]bool{}
	for off := startxref(tail); off >= 0 && !seen[off] && len(seen) < maxXrefSections; {
		seen[off] = true
		trailer, entries, err := xrefSection(r, off)
		if err != nil {
			if r.broken() {
				return Metadata{}, err
			}
			break
		}
		sections = append(sections, entries)
		if trailer != nil {
			trailers = append(trailers, trailer)
		}
		if ref, ok := trailer["Info"].(pdfRef); ok && info == nil {
			info = &ref
		}
		if info != nil {
			if _, ok := locate(); ok {
				break
			}
		}
		prev, ok := trailer["Prev"].(int64)
		if !ok {
			break
		}
		off = prev
	}
	if t := tailTrailer(tail); t != nil {
		trailers = append(trailers, t)
		if ref, ok := t["Info"].(pdfRef); ok && info == nil {
			info = &ref
		}
	}
	if len(trailers) == 0 {
		return Metadata{}, malformed("no PDF trailer")
	}
	for _, t := range trailers {
		if _, ok := t["Encrypt"]; ok {
			return Metadata{}, nil
		}
	}
	if info == nil {
		return Metadata{}, nil
	}

	var obj []byte
	if loc, ok := locate(); ok {
		if loc.inStream {
			return Metadata{}, nil
		}
		if obj, err = pdfObject(r, loc.off, *info); err != nil && r.broken() {
			return Metadata{}, err
		}
	}
	if obj == nil {
		if obj, err = findObject(r, *info); err != nil {
			return Metadata{}, err
		}
	}
	l := &lexer{b: obj}
	d, _ := l.value(0).(pdfDict)
	return infoMetadata(d), nil
}

// startxref returns the offset after the last startxref in tail, or -1.
func startxref(tail []byte) int64 {
	i := bytes.LastIndex(tail, []byte("startxref"))
	if i < 0 {
		return -1
	}
	l := &lexer{b: tail, i: i + len("startxref")}
	if n, ok := l.int(); ok {
		return n
	}
	return -1
}

// tailTrailer parses the dictionary after the last "trailer" in tail.
func tailTrailer(tail []byte) pdfDict {
	i := bytes.LastIndex(tail, []byte("trailer"))
	if i < 0 {
		return nil
	}
	l := &lexer{b: tail, i: i + len("trailer")}
	d, _ := l.value(0).(pdfDict)
	return d
}

// xrefSection reads the xref table or stream at off. The trailer of a table
// too long to read whole is nil.
func xrefSection(r *reader, off int64) (pdfDict, map[int64]objLoc, error) {
	b, err := r.at(off, pdfXrefLen)
	if err != nil {
		return nil, nil, err
	}
	l := &lexer{b: b}
	if l.keyword("xref") {
		trailer, entries := xrefTable(l)
		return trailer, entries, nil
	}
	return xrefStream(r, off, l)
}

// xrefTable reads the subsections of a classic xref table, then its trailer.
// It stops quietly where the bytes read end.
func xrefTable(l *lexer) (pdfDict, map[int64]objLoc) {
	entries := map[int64]objLoc{}
	for {
		if l.keyword("trailer") {
			d, _ := l.value(0).(pdfDict)
			return d, entries
		}
		first, ok1 := l.int()
		count, ok2 := l.int()
		if !ok1 || !ok2 {
			return nil, entries
		}
		for i := range count {
			off, ok1 := l.int()
			gen, ok2 := l.int()
			l.skipSpace()
			kind := string(l.token())
			if !ok1 || !ok2 || (kind != "n" && kind != "f") {
				return nil, entries
			}
			if _, dup := entries[first+i]; kind == "n" && !dup {
				entries[first+i] = objLoc{off: off, gen: gen}
			}
		}
	}
}

// xrefStream reads the xref stream object whose dictionary l is about to
// parse, at off in the file.
func xrefStream(r *reader, off int64, l *lexer) (pdfDict, map[int64]objLoc, error) {
	if _, ok := l.objectHeader(); !ok {
		return nil, nil, malformed("no xref at %d", off)
	}
	d, ok := l.value(0).(pdfDict)
	if !ok || d["Type"] != pdfName("XRef") {
		return nil, nil, malformed("no xref stream at %d", off)
	}
	if !l.keyword("stream") {
		return nil, nil, malformed("xref stream without data")
	}
	if l.i < len(l.b) && l.b[l.i] == '\r' {
		l.i++
	}
	if l.i < len(l.b) && l.b[l.i] == '\n' {
		l.i++
	}
	n, ok := d["Length"].(int64)
	if !ok {
		n = int64(bytes.Index(l.b[l.i:], []byte("endstream")))
	}
	if n < 0 || n > maxXrefStream {
		return nil, nil, malformed("xref stream of %d bytes", n)
	}
	raw, err := r.at(off+int64(l.i), int(n))
	if err != nil {
		return nil, nil, err
	}
	data, err := streamData(d, raw)
	if err != nil {
		return nil, nil, err
	}
	entries, err := xrefStreamEntries(d, data)
	return d, entries, err
}

// streamData decodes the data of a stream with dictionary d: FlateDecode or
// no filter, and the PNG predictors.
func streamData(d pdfDict, raw []byte) ([]byte, error) {
	filter, params := d["Filter"], d["DecodeParms"]
	if a, ok := filter.([]any); ok && len(a) == 1 {
		filter = a[0]
		if p, ok := params.([]any); ok && len(p) == 1 {
			params = p[0]
		}
	}
	data := raw
	switch filter {
	case nil:
	case pdfName("FlateDecode"):
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		// A damaged end of stream is common; keep what decompressed.
		data, _ = io.ReadAll(io.LimitReader(zr, maxXrefData))
	default:
		return nil, malformed("xref stream filter %v", filter)
	}
	p, _ := params.(pdfDict)
	predictor := intOr(p["Predictor"], 1)
	switch {
	case predictor == 1:
		return data, nil
	case predictor < 10:
		return nil, malformed("predictor %d", predictor)
	}
	colors, bits, columns := intOr(p["Colors"], 1), intOr(p["BitsPerComponent"], 8), intOr(p["Columns"], 1)
	if colors < 1 || colors > 32 || bits < 1 || bits > 16 || columns < 1 || columns > 1<<16 {
		return nil, malformed("predictor parameters")
	}
	return pngUnpredict(data, int((colors*bits*columns+7)/8), int(max(1, colors*bits/8)))
}

// pngUnpredict undoes the PNG filters of rows of rowLen bytes, each led by
// its filter type, with bpp bytes per pixel.
func pngUnpredict(data []byte, rowLen, bpp int) ([]byte, error) {
	out := make([]byte, 0, len(data)/(rowLen+1)*rowLen)
	prev := make([]byte, rowLen)
	for ; len(data) > rowLen; data = data[rowLen+1:] {
		filter := data[0]
		start := len(out)
		out = append(out, data[1:rowLen+1]...)
		cur := out[start:]
		for i := range cur {
			var a, c byte
			if i >= bpp {
				a, c = cur[i-bpp], prev[i-bpp]
			}
			switch b := prev[i]; filter {
			case 0:
			case 1:
				cur[i] += a
			case 2:
				cur[i] += b
			case 3:
				cur[i] += byte((int(a) + int(b)) / 2)
			case 4:
				cur[i] += paeth(a, b, c)
			default:
				return nil, malformed("PNG filter %d", filter)
			}
		}
		prev = cur
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// xrefStreamEntries reads the rows of a decoded xref stream, as /W and
// /Index lay them out.
func xrefStreamEntries(d pdfDict, data []byte) (map[int64]objLoc, error) {
	w := ints(d["W"])
	if len(w) != 3 {
		return nil, malformed("xref stream /W")
	}
	rowLen := 0
	for _, n := range w {
		if n < 0 || n > 8 {
			return nil, malformed("xref stream /W")
		}
		rowLen += int(n)
	}
	if rowLen == 0 {
		return nil, malformed("xref stream /W")
	}
	index := ints(d["Index"])
	if index == nil {
		index = []int64{0, intOr(d["Size"], 0)}
	}
	field := func(b []byte, n int64, def int64) int64 {
		if n == 0 {
			return def
		}
		var v int64
		for _, c := range b[:n] {
			v = v<<8 | int64(c)
		}
		return v
	}
	entries := map[int64]objLoc{}
	for k := 0; k+1 < len(index); k += 2 {
		first, count := index[k], index[k+1]
		for i := int64(0); i < count && len(data) >= rowLen; i++ {
			row := data[:rowLen]
			data = data[rowLen:]
			kind := field(row, w[0], 1)
			a := field(row[w[0]:], w[1], 0)
			b := field(row[w[0]+w[1]:], w[2], 0)
			if _, dup := entries[first+i]; dup {
				continue
			}
			switch kind {
			case 1:
				entries[first+i] = objLoc{off: a, gen: b}
			case 2:
				entries[first+i] = objLoc{inStream: true}
			}
		}
	}
	return entries, nil
}

// pdfObject returns the bytes after "num gen obj" at off, or nil if another
// object, or none, is there.
func pdfObject(r *reader, off int64, ref pdfRef) ([]byte, error) {
	b, err := r.at(off, pdfObjLen)
	if err != nil {
		return nil, err
	}
	l := &lexer{b: b}
	if got, ok := l.objectHeader(); !ok || got != ref {
		return nil, nil
	}
	return b[l.i:], nil
}

// findObject searches the tail, then the head of the file, for the last
// "num gen obj" and returns the object, or nil.
func findObject(r *reader, ref pdfRef) ([]byte, error) {
	re := regexp.MustCompile(fmt.Sprintf(`(?:^|[^0-9])%d\s+%d\s+obj\b`, ref.num, ref.gen))
	type part struct {
		off int64
		n   int
	}
	parts := []part{{max(0, r.size-pdfTailLen), pdfTailLen}}
	if parts[0].off > 0 {
		parts = append(parts, part{0, pdfHeadLen})
	}
	for _, p := range parts {
		b, err := r.at(p.off, p.n)
		if err != nil {
			return nil, err
		}
		all := re.FindAllIndex(b, -1)
		if len(all) == 0 {
			continue
		}
		at := all[len(all)-1][0]
		if b[at] < '0' || b[at] > '9' {
			at++
		}
		return pdfObject(r, p.off+int64(at), ref)
	}
	return nil, nil
}

// junkTitles are titles that producers write when the author gave none.
var junkTitles = map[string]bool{
	"untitled": true, "untitled document": true, "document": true, "document1": true,
	"title": true, "unknown": true, "none": true, "no title": true, "slide 1": true,
	"powerpoint presentation": true, "print": true,
}

// junkExtensions end titles that are the name of the source file.
var junkExtensions = []string{
	".doc", ".docx", ".pdf", ".indd", ".tex", ".dvi", ".qxd", ".rtf", ".odt",
	".ps", ".p65", ".pmd", ".fm",
}

// junkAuthors are the account names producers write as the author.
var junkAuthors = map[string]bool{
	"administrator": true, "admin": true, "user": true, "owner": true,
	"unknown": true, "author": true, "default": true, "pc": true,
	"windows user": true, "microsoft office user": true,
}

func junkTitle(t string) bool {
	l := strings.ToLower(t)
	if junkTitles[l] || strings.HasPrefix(l, "microsoft word - ") || strings.HasPrefix(l, "microsoft powerpoint - ") {
		return true
	}
	for _, ext := range junkExtensions {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	return !strings.ContainsFunc(t, unicode.IsLetter)
}

// infoMetadata reads a document information dictionary. Values that are not
// strings, such as indirect references, are ignored.
func infoMetadata(d pdfDict) Metadata {
	var m Metadata
	if s, ok := d["Title"].(pdfString); ok {
		if t := cleanText(pdfText(s)); !junkTitle(t) {
			m.Title = t
		}
	}
	if s, ok := d["Author"].(pdfString); ok {
		for _, a := range strings.Split(pdfText(s), ";") {
			if a = cleanText(a); a != "" && !junkAuthors[strings.ToLower(a)] {
				m.Authors = append(m.Authors, a)
			}
		}
	}
	return m
}

// pdfDocEncoding maps the bytes 0x80–0x9E of PDFDocEncoding.
var pdfDocEncoding = [31]rune{
	'•', '†', '‡', '…', '—', '–', 'ƒ', '⁄', '‹', '›', '−', '‰', '„', '“', '”', '‘',
	'’', '‚', '™', 'ﬁ', 'ﬂ', 'Ł', 'Œ', 'Š', 'Ÿ', 'Ž', 'ı', 'ł', 'œ', 'š', 'ž',
}

// pdfText decodes a PDF text string: UTF-16BE or UTF-8 after their byte order
// marks, else PDFDocEncoding.
func pdfText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xfe, 0xff}):
		b = b[2:]
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		}
		return string(utf16.Decode(u))
	case bytes.HasPrefix(b, []byte{0xef, 0xbb, 0xbf}):
		return strings.ToValidUTF8(string(b[3:]), string(utf8.RuneError))
	}
	var s strings.Builder
	for _, c := range b {
		switch {
		case c >= 0x80 && c <= 0x9e:
			s.WriteRune(pdfDocEncoding[c-0x80])
		case c == 0xa0:
			s.WriteRune('€')
		default:
			s.WriteRune(rune(c))
		}
	}
	return s.String()
}

func intOr(v any, def int64) int64 {
	if n, ok := v.(int64); ok {
		return n
	}
	return def
}

// ints returns an array of integers, or nil if v is anything else.
func ints(v any) []int64 {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int64, len(a))
	for i, x := range a {
		if out[i], ok = x.(int64); !ok {
			return nil
		}
	}
	return out
}

// lexer reads PDF objects from b. It is no full PDF parser: it reads the
// values a trailer, an xref stream dictionary and an Info dictionary hold.
type lexer struct {
	b []byte
	i int
}

func isPDFSpace(c byte) bool {
	return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}

func isPDFDelim(c byte) bool {
	return strings.IndexByte("()<>[]{}/%", c) >= 0
}

// skipSpace skips white space and comments.
func (l *lexer) skipSpace() {
	for l.i < len(l.b) {
		switch c := l.b[l.i]; {
		case c == '%':
			for l.i < len(l.b) && l.b[l.i] != '\n' && l.b[l.i] != '\r' {
				l.i++
			}
		case isPDFSpace(c):
			l.i++
		default:
			return
		}
	}
}

// token returns the regular characters at l.i.
func (l *lexer) token() []byte {
	start := l.i
	for l.i < len(l.b) && !isPDFSpace(l.b[l.i]) && !isPDFDelim(l.b[l.i]) {
		l.i++
	}
	return l.b[start:l.i]
}

// keyword consumes the keyword k, if it comes next.
func (l *lexer) keyword(k string) bool {
	l.skipSpace()
	save := l.i
	if string(l.token()) == k {
		return true
	}
	l.i = save
	return false
}

// int consumes an unsigned integer, if one comes next.
func (l *lexer) int() (int64, bool) {
	l.skipSpace()
	save := l.i
	tok := l.token()
	if len(tok) == 0 || len(tok) > 15 || tok[0] < '0' || tok[0] > '9' {
		l.i = save
		return 0, false
	}
	n, err := strconv.ParseInt(string(tok), 10, 64)
	if err != nil {
		l.i = save
		return 0, false
	}
	return n, true
}

// objectHeader consumes "num gen obj".
func (l *lexer) objectHeader() (pdfRef, bool) {
	num, ok1 := l.int()
	gen, ok2 := l.int()
	if !ok1 || !ok2 || !l.keyword("obj") {
		return pdfRef{}, false
	}
	return pdfRef{num, gen}, true
}

// value parses the next value. It returns nil for null and for anything it
// cannot parse.
func (l *lexer) value(depth int) any {
	v, _ := l.parse(depth)
	return v
}

func (l *lexer) parse(depth int) (any, bool) {
	if depth > maxPDFDepth {
		return nil, false
	}
	l.skipSpace()
	if l.i >= len(l.b) {
		return nil, false
	}
	switch c := l.b[l.i]; {
	case c == '/':
		l.i++
		return pdfName(l.token()), true
	case c == '(':
		return l.literal()
	case c == '<' && l.i+1 < len(l.b) && l.b[l.i+1] == '<':
		return l.dict(depth)
	case c == '<':
		return l.hex()
	case c == '[':
		l.i++
		var a []any
		for {
			l.skipSpace()
			if l.i >= len(l.b) {
				return nil, false
			}
			if l.b[l.i] == ']' {
				l.i++
				return a, true
			}
			v, ok := l.parse(depth + 1)
			if !ok {
				return nil, false
			}
			a = append(a, v)
		}
	case isPDFDelim(c):
		return nil, false
	}
	save := l.i
	if n, ok := l.int(); ok {
		after := l.i
		if gen, ok := l.int(); ok && l.keyword("R") {
			return pdfRef{n, gen}, true
		}
		l.i = after
		return n, true
	}
	l.i = save
	tok := string(l.token())
	switch tok {
	case "true", "false":
		return tok == "true", true
	case "null":
		return nil, true
	}
	if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
		return n, true
	}
	if f, err := strconv.ParseFloat(tok, 64); err == nil {
		return f, true
	}
	return pdfKeyword(tok), true
}

func (l *lexer) dict(depth int) (any, bool) {
	l.i += 2
	d := pdfDict{}
	for {
		l.skipSpace()
		if l.i+1 < len(l.b) && l.b[l.i] == '>' && l.b[l.i+1] == '>' {
			l.i += 2
			return d, true
		}
		if l.i >= len(l.b) || l.b[l.i] != '/' {
			return nil, false
		}
		l.i++
		key := string(l.token())
		v, ok := l.parse(depth + 1)
		if !ok {
			return nil, false
		}
		d[key] = v
	}
}

// literal parses a literal string: balanced parentheses, backslash escapes,
// octal codes and line continuations.
func (l *lexer) literal() (any, bool) {
	l.i++
	var s []byte
	for nest := 1; l.i < len(l.b); {
		c := l.b[l.i]
		l.i++
		switch c {
		case '(':
			nest++
		case ')':
			if nest--; nest == 0 {
				return pdfString(s), true
			}
		case '\r':
			if l.i < len(l.b) && l.b[l.i] == '\n' {
				l.i++
			}
			c = '\n'
		case '\\':
			if l.i >= len(l.b) {
				return nil, false
			}
			e := l.b[l.i]
			l.i++
			switch e {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case '\r':
				if l.i < len(l.b) && l.b[l.i] == '\n' {
					l.i++
				}
				continue
			case '\n':
				continue
			case '0', '1', '2', '3', '4', '5', '6', '7':
				v := int(e - '0')
				for k := 0; k < 2 && l.i < len(l.b) && l.b[l.i] >= '0' && l.b[l.i] <= '7'; k++ {
					v = v*8 + int(l.b[l.i]-'0')
					l.i++
				}
				c = byte(v)
			default:
				c = e
			}
		}
		s = append(s, c)
	}
	return nil, false
}

// hex parses a hexadecimal string; a missing last digit is zero.
func (l *lexer) hex() (any, bool) {
	l.i++
	var s []byte
	digits := 0
	for ; l.i < len(l.b); l.i++ {
		c := l.b[l.i]
		var v byte
		switch {
		case c == '>':
			l.i++
			return pdfString(s), true
		case isPDFSpace(c):
			continue
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			return nil, false
		}
		if digits%2 == 0 {
			s = append(s, v<<4)
		} else {
			s[len(s)-1] |= v
		}
		digits++
	}
	return nil, false
}
