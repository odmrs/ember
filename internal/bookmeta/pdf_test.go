package bookmeta

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
)

// buildPDF lays out objs as objects 1, 2, ..., then a classic xref table and
// a trailer that adds extra to /Size. base is where the result starts in the
// file, so the offsets are right when it is the tail of a bigger one.
func buildPDF(base int64, extra string, objs ...string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offs := make([]int64, len(objs))
	for i, o := range objs {
		offs[i] = base + int64(b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := base + int64(b.Len())
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d %s >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, extra, xref)
	return b.Bytes()
}

func utf16Hex(s string) string {
	b := []byte{0xfe, 0xff}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u>>8), byte(u))
	}
	return "<" + strings.ToUpper(hex.EncodeToString(b)) + ">"
}

var classicPDF = buildPDF(0, "/Root 1 0 R /Info 2 0 R",
	"<< /Type /Catalog >>",
	`<< /Producer (pdfTeX) /Title (Caf\351 \(au lait\) \\ x\nyz \101\102 (nested (parens)) cont\`+"\n"+`inued)
	   /Author (Ann Smith; Administrator;  Bob   Jones) /CreationDate (D:20200101) >>`)

func TestPDFLiteralStrings(t *testing.T) {
	checkMetadata(t, read(t, classicPDF, "PDF"), Metadata{
		Title:   `Café (au lait) \ x yz AB (nested (parens)) continued`,
		Authors: []string{"Ann Smith", "Bob Jones"},
	})
}

func TestPDFTextEncodings(t *testing.T) {
	for _, tc := range []struct{ title, author, wantTitle, wantAuthor string }{
		{utf16Hex("Ação – Ω 𝄞"), "(\357\273\277Jos\303\251)", "Ação – Ω 𝄞", "José"},
		{`(\223x \240 \215quoted\216)`, `<4D61 72 6>`, "ﬁx € “quoted”", "Mar`"},
	} {
		b := buildPDF(0, "/Info 1 0 R", fmt.Sprintf("<</Title %s/Author %s>>", tc.title, tc.author))
		checkMetadata(t, read(t, b, "PDF"), Metadata{Title: tc.wantTitle, Authors: []string{tc.wantAuthor}})
	}
}

func TestPDFJunk(t *testing.T) {
	b := buildPDF(0, "/Info 1 0 R", "<< /Title (Microsoft Word - thesis.doc) /Author (Windows User) >>")
	checkMetadata(t, read(t, b, "PDF"), Metadata{})

	b = buildPDF(0, "/Info 1 0 R", "<< /Title 2 0 R /Author (Real Person) >>", "(Indirect)")
	checkMetadata(t, read(t, b, "PDF"), Metadata{Authors: []string{"Real Person"}})

	for title, junk := range map[string]bool{
		"Untitled": true, " document1 ": true, "report.PDF": true, "chapter1.indd": true,
		"1234 - 5678": true, "Microsoft PowerPoint - deck": true, "Slide 1": true,
		"Dune": false, "A.I. and You": false, "Maps of Meaning": false, "Document Analysis": false,
	} {
		if got := junkTitle(cleanText(title)); got != junk {
			t.Errorf("junkTitle(%q) = %v", title, got)
		}
	}
}

func TestPDFEncrypted(t *testing.T) {
	b := buildPDF(0, "/Info 1 0 R /Encrypt 2 0 R", "<< /Title (Secret) >>", "<< /Filter /Standard >>")
	checkMetadata(t, read(t, b, "PDF"), Metadata{})
}

func TestPDFBrokenStartxref(t *testing.T) {
	b := buildPDF(0, "/Info 2 0 R", "<< >>", "<< /Title (Recovered) >>")
	i := bytes.LastIndex(b, []byte("startxref\n")) + len("startxref\n")
	b = append(b[:i:i], "17\n%%EOF\n"...)
	checkMetadata(t, read(t, b, "PDF"), Metadata{Title: "Recovered"})
}

func TestPDFIncrementalUpdate(t *testing.T) {
	b := buildPDF(0, "/Info 2 0 R", "<< >>", "<< /Title (Kept) >>")
	prev := bytes.LastIndex(b, []byte("\nxref\n")) + 1
	obj3 := len(b)
	b = append(b, "3 0 obj\n<< /Type /Catalog >>\nendobj\n"...)
	xref := len(b)
	b = fmt.Appendf(b, "xref\n3 1\n%010d 00000 n \ntrailer\n<< /Size 4 /Root 3 0 R /Info 2 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n", obj3, prev, xref)
	checkMetadata(t, read(t, b, "PDF"), Metadata{Title: "Kept"})
}

// pngPredict encodes rows with the PNG filter types in filters, one per row,
// cycling.
func pngPredict(rows [][]byte, filters []byte) []byte {
	var out []byte
	prev := make([]byte, len(rows[0]))
	for n, row := range rows {
		f := filters[n%len(filters)]
		out = append(out, f)
		for i, x := range row {
			var a, c byte
			if i > 0 {
				a, c = row[i-1], prev[i-1]
			}
			b := prev[i]
			switch f {
			case 1:
				x -= a
			case 2:
				x -= b
			case 3:
				x -= byte((int(a) + int(b)) / 2)
			case 4:
				x -= paeth(a, b, c)
			}
			out = append(out, x)
		}
		prev = row
	}
	return out
}

func TestPaeth(t *testing.T) {
	for _, tc := range [][4]byte{
		{20, 10, 10, 20},   // nearest to a
		{10, 20, 10, 20},   // nearest to b
		{0, 255, 128, 128}, // nearest to c
		{10, 10, 20, 10},   // a wins ties
	} {
		if got := paeth(tc[0], tc[1], tc[2]); got != tc[3] {
			t.Errorf("paeth(%d, %d, %d) = %d, want %d", tc[0], tc[1], tc[2], got, tc[3])
		}
	}
}

// buildXrefStreamPDF makes a PDF 1.5 with info as object 2 and an xref stream
// in two subsections, PNG-predicted with every filter type. If compressed, the
// xref stream says object 2 lives in an object stream.
func buildXrefStreamPDF(info string, compressed bool) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.5\n")
	off1 := b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog >>\nendobj\n")
	off2 := b.Len()
	fmt.Fprintf(&b, "2 0 obj\n%s\nendobj\n", info)
	off3 := b.Len()
	rows := [][]byte{
		{0, 0, 0, 255},
		{1, byte(off1 >> 8), byte(off1), 0},
		{1, byte(off2 >> 8), byte(off2), 0},
		{1, byte(off3 >> 8), byte(off3), 0},
	}
	if compressed {
		rows[2] = []byte{2, 0, 9, 0}
	}
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write(pngPredict(rows, []byte{1, 2, 3, 4, 0}))
	w.Close()
	fmt.Fprintf(&b, "3 0 obj\n<< /Type /XRef /Size 4 /Index [0 1 1 3] /W [1 2 1] /Root 1 0 R /Info 2 0 R\n"+
		"/Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 4 >> /Length %d >>\nstream\n", z.Len())
	b.Write(z.Bytes())
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", off3)
	return b.Bytes()
}

func TestPDFXrefStream(t *testing.T) {
	b := buildXrefStreamPDF("<< /Title (Streamed) /Author (A. Writer) >>", false)
	checkMetadata(t, read(t, b, "PDF"), Metadata{Title: "Streamed", Authors: []string{"A. Writer"}})

	b = buildXrefStreamPDF("<< /Title (In An Object Stream) >>", true)
	checkMetadata(t, read(t, b, "PDF"), Metadata{})
}

func TestPDFWithoutTrailer(t *testing.T) {
	b := []byte("%PDF-1.4\nnothing to see here\n")
	if _, err := Read(bytes.NewReader(b), int64(len(b)), "PDF"); err == nil {
		t.Error("a PDF without trailer read fine")
	}
}
