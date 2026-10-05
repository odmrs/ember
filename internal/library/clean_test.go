package library

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/odmrs/ember/internal/names"
)

// epub builds an EPUB whose OPF names title and creators, padded so the
// file is larger than what reading its metadata may take.
func epub(t *testing.T, title string, creators ...string) string {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	add := func(name, body string, method uint16) {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate)
	var dc strings.Builder
	for _, c := range creators {
		fmt.Fprintf(&dc, `<dc:creator opf:role="aut">%s</dc:creator>`, c)
	}
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" xmlns:opf="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>`+title+`</dc:title>`+dc.String()+`</metadata></package>`, zip.Deflate)
	add("OEBPS/text.html", strings.Repeat("<p>Lorem ipsum.</p>", 1), zip.Store)
	add("OEBPS/padding.bin", strings.Repeat("\x00", 6<<20), zip.Store)
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// shelf lays out a folder of downloaded Books:
//
//	Downloads/{<Anna's Archive name>.epub + Sidecar, <hash>.epub (metadata),
//	Dune - Frank Herbert.epub, Dune.epub, dune (1).epub, cover.png}
func shelf(t *testing.T, d disk) {
	t.Helper()
	dir := root + "/Downloads/"
	anna := "Animal Farm -- Orwell, George -- 2021 -- AmazonClassics -- 9eb17e015e45ed6e371a245ee96e84ab -- Anna's Archive.epub"
	d.WriteFile(t, dir+anna, "not a zip")
	d.WriteFile(t, dir+strings.TrimSuffix(anna, ".epub")+".sdr/metadata.epub.lua", "return {}")
	d.WriteFile(t, dir+"f5c597940cfefa073fb07b2d5b8d0c12.epub", epub(t, "Do zero ao infinito (e além)", "Goldsmith, Mike"))
	d.WriteFile(t, dir+"Dune - Frank Herbert.epub", "d")
	d.WriteFile(t, dir+"Dune.epub", "d")
	d.WriteFile(t, dir+"dune (1).epub", "d")
	d.WriteFile(t, dir+"cover.png", "png")
}

func proposal(t *testing.T, ps []Proposal, name string) Proposal {
	t.Helper()
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no proposal for %q in %+v", name, ps)
	return Proposal{}
}

func TestProposeNamesForAFolder(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		shelf(t, d)
		settings(t, d, `    ["lastfile"] = "/mnt/us/documents/Downloads/Dune.epub",`+"\n")
		before := listed(t, fsys, "Downloads")
		ps, err := New(fsys, root).ProposeNames("Downloads")
		must(t, "propose", err)
		if len(ps) != 5 {
			t.Fatalf("%d proposals, want one per Book: %+v", len(ps), ps)
		}

		anna := proposal(t, ps, "Animal Farm -- Orwell, George -- 2021 -- AmazonClassics -- 9eb17e015e45ed6e371a245ee96e84ab -- Anna's Archive.epub")
		if anna.Proposed != "Animal Farm - George Orwell.epub" || anna.Source != names.SourcePattern || !anna.Changed || anna.Taken != "" {
			t.Errorf("Anna's Archive name: %+v", anna)
		}
		if anna.Path != "Downloads/"+anna.Name {
			t.Errorf("path = %q", anna.Path)
		}
		meta := proposal(t, ps, "f5c597940cfefa073fb07b2d5b8d0c12.epub")
		if meta.Proposed != "Do zero ao infinito (e além) - Mike Goldsmith.epub" || meta.Source != names.SourceMetadata {
			t.Errorf("metadata name: %+v", meta)
		}
		if p := proposal(t, ps, "Dune - Frank Herbert.epub"); p.Changed || p.Proposed != p.Name || p.Source != "" {
			t.Errorf("clean name: %+v", p)
		}
		if p := proposal(t, ps, "Dune.epub"); p.Changed || !p.OpenNow {
			t.Errorf("open Book: %+v", p)
		}
		// "dune (1).epub" cleans to "Dune.epub", which is taken.
		if p := proposal(t, ps, "dune (1).epub"); !p.Changed || p.Proposed != "Dune.epub" || p.Taken != "Dune.epub" {
			t.Errorf("taken name: %+v", p)
		}
		if after := listed(t, fsys, "Downloads"); after != before {
			t.Errorf("a preview renamed something: %s, was %s", after, before)
		}
	})
}

func TestProposeNamesForOneBook(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		shelf(t, d)
		lib := New(fsys, root)
		ps, err := lib.ProposeNames("Downloads/f5c597940cfefa073fb07b2d5b8d0c12.epub")
		must(t, "propose", err)
		if len(ps) != 1 || ps[0].Proposed != "Do zero ao infinito (e além) - Mike Goldsmith.epub" || ps[0].Path != "Downloads/f5c597940cfefa073fb07b2d5b8d0c12.epub" {
			t.Fatalf("proposals = %+v", ps)
		}
		ps, err = lib.ProposeNames("Downloads/cover.png")
		if err != nil || len(ps) != 0 {
			t.Errorf("other file: %+v, %v", ps, err)
		}
		_, err = lib.ProposeNames("Downloads/missing.epub")
		mustNot(t, "missing Book", err, ErrNotFound)
		_, err = lib.ProposeNames("../x")
		mustNot(t, "outside", err, ErrOutside)
		_, err = lib.ProposeNames("Downloads/Animal Farm -- Orwell, George -- 2021 -- AmazonClassics -- 9eb17e015e45ed6e371a245ee96e84ab -- Anna's Archive.sdr")
		mustNot(t, "inside a Sidecar", err, ErrNotFound)
	})
}

func TestProposeNamesMarksTwoBooksCleaningToOneName(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		d.WriteFile(t, root+"/Dune -- Herbert, Frank -- Anna's Archive.epub", "a")
		d.WriteFile(t, root+"/dune_-_frank_herbert.epub", "b")
		ps, err := New(fsys, root).ProposeNames("")
		must(t, "propose", err)
		taken := 0
		for _, p := range ps {
			if p.Proposed != "Dune - Frank Herbert.epub" {
				t.Errorf("proposal %+v", p)
			}
			if p.Taken != "" {
				taken++
			}
		}
		if taken != 1 {
			t.Errorf("%d proposals marked taken, want the second one: %+v", taken, ps)
		}
	})
}

// counting records how many bytes are read through ReadAt.
type counting struct {
	FS
	mu   sync.Mutex
	read int64
}

func (c *counting) Open(p string) (File, error) {
	f, err := c.FS.Open(p)
	if err != nil {
		return nil, err
	}
	return &countedFile{File: f, c: c}, nil
}

type countedFile struct {
	File
	c *counting
}

func (f *countedFile) ReadAt(b []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(b, off)
	f.c.mu.Lock()
	f.c.read += int64(n)
	f.c.mu.Unlock()
	return n, err
}

func (f *countedFile) Read(b []byte) (int, error) {
	panic("metadata is read by offset, never from the start")
}

func TestProposeNamesReadsOnlyTheMetadata(t *testing.T) {
	d := newDirFS(t)
	shelf(t, d)
	c := &counting{FS: d}
	_, err := New(c, root).ProposeNames("Downloads")
	must(t, "propose", err)
	if c.read == 0 || c.read > 1<<20 {
		t.Errorf("read %d bytes of a 6 MiB Book", c.read)
	}
}

func TestRenameAll(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		shelf(t, d)
		lib := New(fsys, root)
		anna := "Downloads/Animal Farm -- Orwell, George -- 2021 -- AmazonClassics -- 9eb17e015e45ed6e371a245ee96e84ab -- Anna's Archive.epub"
		changes := []NameChange{
			{Path: anna, Name: "Animal Farm - George Orwell.epub"},
			{Path: "Downloads/f5c597940cfefa073fb07b2d5b8d0c12.epub", Name: "Do zero ao infinito - Mike Goldsmith.epub"},
		}
		_, err := lib.RenameAll(changes, Options{})
		must(t, "rename all", err)
		if got, want := listed(t, fsys, "Downloads"), "Animal Farm - George Orwell.epub,cover.png,Do zero ao infinito - Mike Goldsmith.epub,dune (1).epub,Dune - Frank Herbert.epub,Dune.epub"; got != want {
			t.Errorf("after = %s\nwant    %s", got, want)
		}
		if !exists(t, d, root+"/Downloads/Animal Farm - George Orwell.sdr/metadata.epub.lua") {
			t.Error("the Sidecar did not travel")
		}
	})
}

func TestRenameAllRefusesBeforeRenamingAnything(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		shelf(t, d)
		settings(t, d, `    ["lastfile"] = "/mnt/us/documents/Downloads/Dune.epub",`+"\n")
		lib := New(fsys, root)
		before := listed(t, fsys, "Downloads")
		ok := NameChange{Path: "Downloads/f5c597940cfefa073fb07b2d5b8d0c12.epub", Name: "Do zero - Mike Goldsmith.epub"}
		for what, tc := range map[string]struct {
			bad  NameChange
			want error
		}{
			"taken":         {NameChange{Path: "Downloads/dune (1).epub", Name: "DUNE.epub"}, ErrExists},
			"batch claims":  {NameChange{Path: "Downloads/Dune.epub", Name: "do zero - mike goldsmith.EPUB"}, ErrExists},
			"invalid":       {NameChange{Path: "Downloads/dune (1).epub", Name: "Dune: Messiah.epub"}, ErrInvalidName},
			"format":        {NameChange{Path: "Downloads/dune (1).epub", Name: "Dune.pdf"}, ErrExtension},
			"missing":       {NameChange{Path: "Downloads/gone.epub", Name: "x.epub"}, ErrNotFound},
			"open, unasked": {NameChange{Path: "Downloads/Dune.epub", Name: "Dune 1965.epub"}, ErrOpenNow},
			"outside":       {NameChange{Path: "../Dune.epub", Name: "x.epub"}, ErrOutside},
		} {
			_, err := lib.RenameAll([]NameChange{ok, tc.bad}, Options{})
			mustNot(t, what, err, tc.want)
			if after := listed(t, fsys, "Downloads"); after != before {
				t.Fatalf("%s: renamed before refusing: %s", what, after)
			}
		}
		_, err := lib.RenameAll([]NameChange{ok, {Path: "Downloads/Dune.epub", Name: "Dune 1965.epub"}}, Options{ConfirmOpen: true})
		must(t, "open, confirmed", err)
	})
}

func TestRenameAllReportsWhatItRenamedBeforeFailing(t *testing.T) {
	d := newDirFS(t)
	shelf(t, d)
	s := &spy{FS: d, failRename: func(from, to string) bool { return strings.HasSuffix(to, "/Dune 1965.epub") }}
	_, err := New(s, root).RenameAll([]NameChange{
		{Path: "Downloads/dune (1).epub", Name: "Dune copy.epub"},
		{Path: "Downloads/Dune.epub", Name: "Dune 1965.epub"},
	}, Options{})
	var re *RenameError
	if !errors.As(err, &re) || strings.Join(re.Renamed, ",") != "Downloads/dune (1).epub" || re.Failed != "Downloads/Dune.epub" {
		t.Fatalf("err = %#v", err)
	}
}

func TestUploadName(t *testing.T) {
	for in, want := range map[string]string{
		"Animal Farm -- Orwell, George -- 2021 -- Anna's Archive.epub": "Animal Farm - George Orwell.epub",
		"Dune.epub": "Dune.epub",
		"_OceanofPDF.com_Dune_-_Frank_Herbert.pdf": "Dune - Frank Herbert.pdf",
		"notes (z-lib.org).txt.zip":                "Notes.txt.zip",
		"photo (1).jpg":                            "photo (1).jpg",
		"Dioses (Shelby Mahurin) (z-library.sk, 1lib.sk, z-lib.sk).epub.zip": "Dioses (Shelby Mahurin) (z-library.sk, 1lib.sk, z-lib.sk).epub.zip",
	} {
		if got := UploadName(in); got != want {
			t.Errorf("UploadName(%q) = %q, want %q", in, got, want)
		}
	}
}
