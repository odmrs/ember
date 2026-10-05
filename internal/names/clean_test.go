package names

import (
	"math/rand/v2"
	"strings"
	"testing"
)

type cleanCase struct {
	in, want, source string
	meta             Metadata
}

func runClean(t *testing.T, cases []cleanCase) {
	t.Helper()
	for _, c := range cases {
		got, source, changed := Clean(c.in, c.meta)
		if got != c.want || source != c.source || changed != (c.want != c.in) {
			t.Errorf("Clean(%q)\n got  %q (%s, changed %v)\n want %q (%s)", c.in, got, source, changed, c.want, c.source)
		}
		if err := Valid(got); err != nil && changed {
			t.Errorf("Clean(%q) = %q, which Valid refuses: %v", c.in, got, err)
		}
	}
}

func TestCleanUsersOwnFiles(t *testing.T) {
	runClean(t, []cleanCase{
		{in: "Animal Farm (AmazonClassics Edition) -- Orwell, George -- AmazonClassics Edition, 2021 -- AmazonClassics -- 9eb17e015e45ed6e371a245ee96e84ab -- Anna's Archive.epub",
			want: "Animal Farm - George Orwell.epub", source: SourcePattern},
		{in: "Do zero ao infinito (e além) -- Mike Goldsmith -- 2016 -- Benvirá -- isbn13 9788557170612 -- f5c597940cfefa073fb07b2d5b8d0c12 -- Anna's Archive.epub",
			want: "Do zero ao infinito (e além) - Mike Goldsmith.epub", source: SourcePattern},
		{in: "The Little Schemer, fourth edition -- Daniel P_ Friedman, Matthias Felleisen; drawings by Duane -- Schemer (1), 4 edition, December 21, 1995 -- The -- isbn13 9780262560993 -- e851170d78a83eaf5596616ce3321889 -- Ann.pdf",
			want: "The Little Schemer - Daniel P. Friedman & Matthias Felleisen.pdf", source: SourcePattern},
		{in: "O Velho E O Mar -- Ernest Hemingway -- 2013;2016 -- Edição ''Livros do Brasil'' -- e8b179d8f63db8ad52a69e10015f7367 -- Anna's Archive.mobi",
			want: "O Velho E O Mar - Ernest Hemingway.mobi", source: SourcePattern},
	})
}

func TestCleanPrefersMetadata(t *testing.T) {
	runClean(t, []cleanCase{
		{in: "f5c597940cfefa073fb07b2d5b8d0c12.epub", meta: Metadata{Title: "Dune", Authors: []string{"Herbert, Frank"}},
			want: "Dune - Frank Herbert.epub", source: SourceMetadata},
		// Metadata keeps its case; the pattern's author fills a gap.
		{in: "Animal Farm -- Orwell, George -- 2021 -- Anna's Archive.epub", meta: Metadata{Title: "ANIMAL FARM: A Fairy Story"},
			want: "ANIMAL FARM - George Orwell.epub", source: SourceMetadata},
		{in: "thesis_final_v2.pdf", meta: Metadata{Title: "Microsoft Word - thesis_final_v2.doc", Authors: []string{"Administrator"}},
			want: "Thesis Final.pdf", source: SourceGeneric},
		{in: "sapiens_ebook.epub", meta: Metadata{Title: "Sapiens", Authors: []string{"Yuval Noah Harari", "John Purcell (Translator)"}},
			want: "Sapiens - Yuval Noah Harari.epub", source: SourceMetadata},
		{in: "download (1).epub", meta: Metadata{Title: "Os Sertões", Authors: []string{"Euclides da Cunha"}},
			want: "Os Sertões - Euclides da Cunha.epub", source: SourceMetadata},
		{in: "book.epub", meta: Metadata{Title: "Structure and Interpretation of Computer Programs", Authors: []string{"Harold Abelson", "Gerald Jay Sussman", "Julie Sussman"}},
			want: "Structure and Interpretation of Computer Programs - Harold Abelson et al.epub", source: SourceMetadata},
	})
}

func TestCleanLeavesCleanNamesAlone(t *testing.T) {
	for _, name := range []string{"Dune - Frank Herbert.epub", "Dune.epub", "1984.epub", "O Velho E O Mar - Ernest Hemingway.mobi", "IT.epub"} {
		if got, _, changed := Clean(name, Metadata{}); changed {
			t.Errorf("Clean(%q) = %q, want no change", name, got)
		}
	}
	// Not even metadata overrides a name someone already made clean.
	if got, _, changed := Clean("Dune.epub", Metadata{Title: "Dune (Dune Chronicles, Book 1)", Authors: []string{"Frank Herbert"}}); changed {
		t.Errorf("clean name changed to %q", got)
	}
}

func TestCleanReadsAnnasArchiveCutAtTheHash(t *testing.T) {
	runClean(t, []cleanCase{
		{in: "Unaccustomed Earth -- c4417dd09ccf55a5c17d.epub", want: "Unaccustomed Earth.epub", source: SourcePattern},
		{in: "Unaccustomed Earth -- Lahiri, Jhumpa -- c4417dd0.epub", want: "Unaccustomed Earth - Jhumpa Lahiri.epub", source: SourcePattern},
		// Two fields and no hash is not Anna's Archive; the dashes still collapse.
		{in: "Unaccustomed Earth -- Jhumpa Lahiri.epub", want: "Unaccustomed Earth - Jhumpa Lahiri.epub", source: SourceGeneric},
	})
}

func TestCleanNeverInvents(t *testing.T) {
	for _, name := range []string{"f5c597940cfefa073fb07b2d5b8d0c12.epub", "9788557170612.epub", "(1).pdf"} {
		if got, _, changed := Clean(name, Metadata{}); changed {
			t.Errorf("Clean(%q) = %q, want no change", name, got)
		}
	}
}

func TestCleanFitsTheValidator(t *testing.T) {
	long := strings.Repeat("Palavra ", 60)
	got, _, changed := Clean(long+"-- Autora Muito Longa -- 2020 -- Anna's Archive.epub", Metadata{})
	if !changed || len(got) > MaxBytes || Valid(got) != nil || !strings.HasSuffix(got, " - Autora Muito Longa.epub") {
		t.Errorf("long name: %q (%d bytes)", got, len(got))
	}
	got, _, _ = Clean("x (2).epub", Metadata{Title: `Who? What: "Why" <not> | * \ / now.`, Authors: []string{"A. Writer"}})
	if Valid(got) != nil || got != "Who What - A. Writer.epub" {
		t.Errorf("forbidden characters: %q", got)
	}
	got, _, _ = Clean("x (2).epub", Metadata{Title: "Tabs\tand\x00 nul é", Authors: nil})
	if got != "Tabs and nul é.epub" {
		t.Errorf("control characters: %q", got)
	}
}

func TestCleanNeverProposesAnInvalidName(t *testing.T) {
	pieces := []string{"a", "É", " ", "_", ".", "-", " -- ", ":", "?", "*", `"`, "|", "/", `\`, "<", ">", "\t", "(", ")", "[", "]",
		"’", "''", "2019", "isbn13 9780262560993", "f5c597940cfefa073fb07b2d5b8d0c12", "Anna's Archive", "z-lib.org", "Orwell, George", "名前"}
	r := rand.New(rand.NewPCG(1, 2))
	for range 3000 {
		var b strings.Builder
		for range 1 + r.IntN(40) {
			b.WriteString(pieces[r.IntN(len(pieces))])
		}
		name := b.String() + []string{".epub", ".PDF", ".fb2.zip", ""}[r.IntN(4)]
		meta := Metadata{}
		if r.IntN(2) == 0 {
			meta = Metadata{Title: b.String(), Authors: []string{b.String()}}
		}
		got, _, changed := Clean(name, meta)
		if changed && Valid(got) != nil {
			t.Fatalf("Clean(%q) = %q: %v", name, got, Valid(got))
		}
		if !changed && got != name {
			t.Fatalf("Clean(%q) = %q but says unchanged", name, got)
		}
	}
}

func TestCleanIsDeterministic(t *testing.T) {
	in := "The Little Schemer, fourth edition -- Daniel P_ Friedman, Matthias Felleisen; drawings by Duane -- Ann.pdf"
	first, _, _ := Clean(in, Metadata{})
	for range 50 {
		if got, _, _ := Clean(in, Metadata{}); got != first {
			t.Fatalf("Clean gave %q then %q", first, got)
		}
	}
}

func TestSplitAuthors(t *testing.T) {
	for in, want := range map[string]string{
		"Orwell, George":                           "George Orwell",
		"Le Guin, Ursula K.":                       "Ursula K. Le Guin",
		"Tolkien, J. R. R.":                        "J. R. R. Tolkien",
		"García Márquez, Gabriel":                  "Gabriel García Márquez",
		"Ann Smith, Bob Jones":                     "Ann Smith|Bob Jones",
		"Friedman, Daniel P.; Felleisen, Matthias": "Daniel P. Friedman|Matthias Felleisen",
		"Neil Gaiman and Terry Pratchett":          "Neil Gaiman|Terry Pratchett",
		"Neil Gaiman & Terry Pratchett":            "Neil Gaiman|Terry Pratchett",
		"Homer; translated by Robert Fagles":       "Homer",
		"Jane Doe (editor)":                        "Jane Doe",
		"Jane Doe (Org.)":                          "Jane Doe",
		"Homer, Robert Fagles (Translator)":        "Homer",
		"Ana Lima (tradutora); Machado de Assis":   "Machado de Assis",
		"Smith, John (ed.)":                        "John Smith",
		"José Ortega y Gasset":                     "José Ortega y Gasset",
		"Unknown":                                  "",
	} {
		if got := strings.Join(splitAuthors(in), "|"); got != want {
			t.Errorf("splitAuthors(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFixCase(t *testing.T) {
	for in, want := range map[string]string{
		"THE LORD OF THE RINGS": "The Lord of the Rings",
		"the lord of the rings": "The Lord of the Rings",
		"O VELHO E O MAR":       "O Velho e o Mar",
		"CIEN AÑOS DE SOLEDAD":  "Cien Años de Soledad",
		"ROCKY II":              "Rocky II",
		"SPIDER-MAN":            "Spider-Man",
		"j.r.r. tolkien":        "J.R.R. Tolkien",
		"O Velho E O Mar":       "O Velho E O Mar",
		"iPhone for dummies":    "iPhone for dummies",
		"IT":                    "IT",
	} {
		if got := fixCase(in); got != want {
			t.Errorf("fixCase(%q) = %q, want %q", in, got, want)
		}
	}
}
