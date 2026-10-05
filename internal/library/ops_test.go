package library

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func exists(t *testing.T, d disk, p string) bool {
	t.Helper()
	_, err := os.Lstat(d.Path(p))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func content(t *testing.T, d disk, p string) string {
	t.Helper()
	b, err := os.ReadFile(d.Path(p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// listed lists a folder by its exact names, as the Device spells them.
func listed(t *testing.T, fsys FS, rel string) string {
	t.Helper()
	f, err := New(fsys, root).List(rel)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(names(f), ",")
}

func mustNot(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func must(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// spy wraps an FS, records every rename and can make chosen writes fail.
type spy struct {
	FS
	mu      sync.Mutex
	renames []string
	// failRename, when set, makes the rename from -> to fail.
	failRename func(from, to string) bool
}

var errInjected = errors.New("injected failure")

func (s *spy) Rename(from, to string) error {
	s.mu.Lock()
	fail := s.failRename != nil && s.failRename(from, to)
	s.renames = append(s.renames, from+" -> "+to)
	s.mu.Unlock()
	if fail {
		return errInjected
	}
	return s.FS.Rename(from, to)
}

func TestValidName(t *testing.T) {
	ok := []string{"Dune.epub", "Sci-Fi", "Ação & Aventura", "a", "名前", strings.Repeat("x", 240), "Dune. Messiah", "#1 [draft]"}
	for _, name := range ok {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v", name, err)
		}
	}
	bad := map[string]string{
		"":                       ReasonEmpty,
		".":                      ReasonDots,
		"..":                     ReasonDots,
		"a/b":                    ReasonReserved,
		`a\b`:                    ReasonReserved,
		"a:b":                    ReasonReserved,
		"a*":                     ReasonReserved,
		"a?":                     ReasonReserved,
		`a"b`:                    ReasonReserved,
		"a<b":                    ReasonReserved,
		"a>b":                    ReasonReserved,
		"a|b":                    ReasonReserved,
		"a\x00b":                 ReasonControl,
		"a\nb":                   ReasonControl,
		"a\tb":                   ReasonControl,
		"a\x7f":                  ReasonControl,
		"name.":                  ReasonTrailing,
		"name ":                  ReasonTrailing,
		strings.Repeat("x", 241): ReasonTooLong,
		strings.Repeat("é", 121): ReasonTooLong, // 242 bytes
		".hidden":                ReasonHidden,
		"Dune.sdr":               ReasonSidecar,
		"Dune.SDR":               ReasonSidecar,
		"bad\xffutf8":            ReasonEncoding,
	}
	for name, reason := range bad {
		err := ValidName(name)
		var ne *NameError
		if !errors.As(err, &ne) || !errors.Is(err, ErrInvalidName) || ne.Reason != reason {
			t.Errorf("ValidName(%q) = %v, want reason %s", name, err, reason)
		}
	}
}

func TestCreateFolder(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		p, err := lib.CreateFolder("Sci-Fi", "Herbert")
		must(t, "create", err)
		if p != "Sci-Fi/Herbert" || !exists(t, d, root+"/Sci-Fi/Herbert") {
			t.Fatalf("created %q", p)
		}
		if p, err := lib.CreateFolder("", "Fantasy"); err != nil || p != "Fantasy" {
			t.Fatalf("at root: %q %v", p, err)
		}
		mustNot(t, "same name, other case", func() error { _, err := lib.CreateFolder("", "empty"); return err }(), ErrExists)
		mustNot(t, "a book's name", func() error { _, err := lib.CreateFolder("", "DUNE.EPUB"); return err }(), ErrExists)
		mustNot(t, "bad name", func() error { _, err := lib.CreateFolder("", "a:b"); return err }(), ErrInvalidName)
		mustNot(t, "climbing name", func() error { _, err := lib.CreateFolder("", "../x"); return err }(), ErrInvalidName)
		mustNot(t, "parent is a book", func() error { _, err := lib.CreateFolder("Dune.epub", "x"); return err }(), ErrNotFolder)
		mustNot(t, "parent missing", func() error { _, err := lib.CreateFolder("Nope", "x"); return err }(), ErrNotFound)
		if exists(t, d, "/mnt/us/x") || exists(t, d, root+"/Nope") {
			t.Fatal("something was created outside")
		}
	})
}

func TestRenameBookCarriesItsSidecar(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		warns, err := New(fsys, root).Rename("Dune.epub", "Dune Messiah.epub", Options{})
		must(t, "rename", err)
		if len(warns) != 0 {
			t.Fatalf("warnings %v", warns)
		}
		if exists(t, d, root+"/Dune.epub") || exists(t, d, root+"/Dune.sdr") {
			t.Fatal("old names left behind")
		}
		if content(t, d, root+"/Dune Messiah.sdr/metadata.epub.lua") != "return {}" || len(content(t, d, root+"/Dune Messiah.epub")) != 1800 {
			t.Fatal("book or sidecar lost")
		}
	})
}

func TestRenameFolderAndOtherFile(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		d.WriteFile(t, root+"/Empty/a.txt", "a")
		_, err := lib.Rename("Empty", "Full", Options{})
		must(t, "folder", err)
		if content(t, d, root+"/Full/a.txt") != "a" {
			t.Fatal("contents did not follow")
		}
		_, err = lib.Rename("cover.png", "front.png", Options{})
		must(t, "other file", err)
		if !exists(t, d, root+"/front.png") {
			t.Fatal("not renamed")
		}
		if _, err := lib.Rename("Full", "Full", Options{}); err != nil {
			t.Fatalf("same name should be a no-op: %v", err)
		}
	})
}

func TestRenameCaseOnlyGoesThroughATempName(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		s := &spy{FS: fsys}
		lib := New(s, root)
		_, err := lib.Rename("Dune.epub", "DUNE.epub", Options{})
		must(t, "book", err)
		_, err = lib.Rename("Empty", "empty", Options{})
		must(t, "folder", err)
		if got := listed(t, fsys, ""); got != "empty,Sci-Fi,cover.png,DUNE.epub,Neuromancer.mobi" {
			t.Fatalf("listed %s", got)
		}
		if !exists(t, d, root+"/DUNE.sdr/metadata.epub.lua") {
			t.Fatal("sidecar lost")
		}
		f, _ := New(fsys, root).List("")
		if !entry(t, f, "DUNE.epub").HasSidecar {
			t.Fatal("sidecar not next to the book")
		}
		// Book, Sidecar and folder each take two steps: away to a temp name
		// and back under the new case.
		if len(s.renames) != 6 {
			t.Fatalf("renames %v", s.renames)
		}
		for i := 0; i < 6; i += 2 {
			from, tmp, _ := strings.Cut(s.renames[i], " -> ")
			back, to, _ := strings.Cut(s.renames[i+1], " -> ")
			if tmp != back || strings.EqualFold(tmp, from) || !strings.EqualFold(from, to) {
				t.Fatalf("not through a temp name: %v", s.renames)
			}
		}
	})
}

func TestRenameRefusesCollisions(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		d.WriteFile(t, root+"/Taken.epub", "taken")
		d.WriteFile(t, root+"/Free.sdr/metadata.epub.lua", "someone else's")
		lib := New(fsys, root)
		_, err := lib.Rename("Dune.epub", "taken.epub", Options{})
		mustNot(t, "book name", err, ErrExists)
		_, err = lib.Rename("Dune.epub", "Free.epub", Options{})
		mustNot(t, "sidecar name", err, ErrExists)
		_, err = lib.Rename("Empty", "SCI-FI", Options{})
		mustNot(t, "folder name", err, ErrExists)
		_, err = lib.Rename("Neuromancer.mobi", "dune.sdr.mobi", Options{})
		if err != nil {
			t.Fatalf("a book may be named like that: %v", err)
		}
		if content(t, d, root+"/Taken.epub") != "taken" || content(t, d, root+"/Free.sdr/metadata.epub.lua") != "someone else's" {
			t.Fatal("overwritten")
		}
		if !exists(t, d, root+"/Dune.epub") || !exists(t, d, root+"/Dune.sdr") {
			t.Fatal("moved anyway")
		}
	})
}

func TestRenameKeepsTheBookFormat(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		for _, name := range []string{"Dune.pdf", "Dune", "Dune.epub3", "Dune.epub.txt"} {
			_, err := lib.Rename("Dune.epub", name, Options{})
			mustNot(t, name, err, ErrExtension)
		}
		_, err := lib.Rename("Dune.epub", "Dune.EPUB", Options{})
		must(t, "extension case", err)
		_, err = lib.Rename("Dune.EPUB", "bad/name.epub", Options{})
		mustNot(t, "bad name", err, ErrInvalidName)
	})
}

func TestMoveCarriesSidecarsAndFolders(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		d.WriteFile(t, root+"/Empty/x.txt", "x")
		_, err := New(fsys, root).Move([]string{"Dune.epub", "Neuromancer.mobi", "Empty", "cover.png"}, "Sci-Fi/Asimov", Options{})
		must(t, "move", err)
		if got := listed(t, fsys, ""); got != "Sci-Fi" {
			t.Fatalf("root still has %s", got)
		}
		if got := listed(t, fsys, "Sci-Fi/Asimov"); got != "Empty,cover.png,Dune.epub,Neuromancer.mobi,Robots.azw3" {
			t.Fatalf("destination has %s", got)
		}
		if !exists(t, d, root+"/Sci-Fi/Asimov/Dune.sdr/metadata.epub.lua") || !exists(t, d, root+"/Sci-Fi/Asimov/Empty/x.txt") {
			t.Fatal("sidecar or folder contents lost")
		}
		if exists(t, d, root+"/Dune.sdr") {
			t.Fatal("sidecar left behind")
		}
	})
}

func TestMoveIntoTheSameFolderIsANoOp(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		s := &spy{FS: fsys}
		_, err := New(s, root).Move([]string{"Dune.epub"}, "", Options{})
		must(t, "move", err)
		if len(s.renames) != 0 {
			t.Fatalf("renamed %v", s.renames)
		}
	})
}

func TestMoveRefuses(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		d.WriteFile(t, root+"/Sci-Fi/Asimov/dune.EPUB", "theirs")
		mkdir(t, d, root+"/Box/Dune.sdr")
		d.WriteFile(t, root+"/Box/Dune.sdr/metadata.epub.lua", "theirs")
		lib := New(fsys, root)
		s := &spy{FS: fsys}
		spied := New(s, root)
		cases := []struct {
			what  string
			paths []string
			to    string
			want  error
		}{
			{"book name taken", []string{"Dune.epub"}, "Sci-Fi/Asimov", ErrExists},
			{"sidecar name taken", []string{"cover.png", "Dune.epub"}, "Box", ErrExists},
			{"into itself", []string{"Sci-Fi"}, "Sci-Fi/Asimov", ErrIntoItself},
			{"onto itself", []string{"Empty"}, "Empty", ErrIntoItself},
			{"to a book", []string{"cover.png"}, "Dune.epub", ErrNotFolder},
			{"to nowhere", []string{"cover.png"}, "Nope", ErrNotFound},
			{"the root", []string{""}, "Empty", ErrRoot},
			{"a missing item", []string{"cover.png", "Gone.epub"}, "Empty", ErrNotFound},
		}
		for _, c := range cases {
			_, err := spied.Move(c.paths, c.to, Options{ConfirmOpen: true})
			mustNot(t, c.what, err, c.want)
		}
		if len(s.renames) != 0 {
			t.Fatalf("a refused batch moved something: %v", s.renames)
		}
		d.WriteFile(t, root+"/Sci-Fi/Asimov/Neuromancer.mobi", "other")
		_, err := lib.Move([]string{"Neuromancer.mobi", "Sci-Fi/Asimov/Neuromancer.mobi"}, "Empty", Options{})
		mustNot(t, "two items with one name", err, ErrExists)
		if content(t, d, root+"/Sci-Fi/Asimov/dune.EPUB") != "theirs" || content(t, d, root+"/Box/Dune.sdr/metadata.epub.lua") != "theirs" {
			t.Fatal("overwritten")
		}
	})
}

// The Sidecar step failing puts the Book back where it was, for rename and
// for move, including the two-step case-only rename.
func TestRollbackWhenTheSidecarStepFails(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		s := &spy{FS: fsys, failRename: func(from, _ string) bool { return strings.HasSuffix(from, ".sdr") }}
		lib := New(s, root)
		check := func(what string, err error) {
			t.Helper()
			mustNot(t, what, err, errInjected)
			if got := listed(t, fsys, ""); got != "Empty,Sci-Fi,cover.png,Dune.epub,Neuromancer.mobi" {
				t.Fatalf("%s: root lists %s", what, got)
			}
			if len(content(t, d, root+"/Dune.epub")) != 1800 || !exists(t, d, root+"/Dune.sdr/metadata.epub.lua") {
				t.Fatalf("%s: book or sidecar not back in place", what)
			}
			if got := listed(t, fsys, "Empty"); got != "" {
				t.Fatalf("%s: Empty lists %s", what, got)
			}
		}
		_, err := lib.Rename("Dune.epub", "Arrakis.epub", Options{})
		check("rename", err)
		_, err = lib.Rename("Dune.epub", "dune.epub", Options{})
		check("case-only rename", err)
		_, err = lib.Move([]string{"Dune.epub"}, "Empty", Options{})
		check("move", err)
	})
}

func TestRollbackFailingIsReported(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		s := &spy{FS: fsys, failRename: func(from, to string) bool {
			return strings.HasSuffix(from, ".sdr") || strings.HasSuffix(from, "/Arrakis.epub")
		}}
		_, err := New(s, root).Rename("Dune.epub", "Arrakis.epub", Options{})
		var pe *PartialError
		if !errors.As(err, &pe) || !errors.Is(err, ErrPartial) || !strings.Contains(err.Error(), "Arrakis.epub") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestTheOpenBookNeedsConfirmation(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		book := "Sci-Fi/Foundation.pdf"
		_, err := lib.Rename(book, "Foundation 2.pdf", Options{})
		mustNot(t, "rename", err, ErrOpenNow)
		_, err = lib.Move([]string{"Dune.epub", book}, "Empty", Options{})
		mustNot(t, "move", err, ErrOpenNow)
		_, err = lib.Delete([]string{book}, Options{})
		mustNot(t, "delete", err, ErrOpenNow)
		_, err = lib.Rename("Sci-Fi", "SF", Options{})
		mustNot(t, "rename the folder holding it", err, ErrOpenNow)
		_, err = lib.Move([]string{"Sci-Fi"}, "Empty", Options{})
		mustNot(t, "move the folder holding it", err, ErrOpenNow)
		_, err = lib.Delete([]string{"Sci-Fi"}, Options{})
		mustNot(t, "delete the folder holding it", err, ErrOpenNow)
		if !exists(t, d, root+"/"+book) || !exists(t, d, root+"/Dune.epub") {
			t.Fatal("changed without confirmation")
		}

		_, err = lib.Rename(book, "Foundation 2.pdf", Options{ConfirmOpen: true})
		must(t, "confirmed rename", err)
		if !exists(t, d, root+"/Sci-Fi/Foundation 2.sdr/metadata.pdf.lua") {
			t.Fatal("sidecar did not follow")
		}
		// lastfile still names the old path, so nothing is open any more.
		_, err = lib.Move([]string{"Sci-Fi/Foundation 2.pdf"}, "Empty", Options{})
		must(t, "no longer open", err)
		settings(t, d, `["lastfile"] = "/mnt/us/documents/Empty/Foundation 2.pdf",`+"\n")
		_, err = lib.Delete([]string{"Empty/Foundation 2.pdf"}, Options{ConfirmOpen: true})
		must(t, "confirmed delete", err)
		if exists(t, d, root+"/Empty/Foundation 2.pdf") || exists(t, d, root+"/Empty/Foundation 2.sdr") {
			t.Fatal("not deleted")
		}
	})
}

func TestDeleteBookTakesItsSidecar(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		warns, err := New(fsys, root).Delete([]string{"Dune.epub", "cover.png", "Neuromancer.mobi"}, Options{})
		must(t, "delete", err)
		if len(warns) != 0 {
			t.Fatalf("warnings %v", warns)
		}
		if exists(t, d, root+"/Dune.epub") || exists(t, d, root+"/Dune.sdr") || exists(t, d, root+"/cover.png") {
			t.Fatal("left behind")
		}
		if got := listed(t, fsys, ""); got != "Empty,Sci-Fi" {
			t.Fatalf("listed %s", got)
		}
	})
}

// Recursive delete removes links as links: what they point at, inside the
// Library or outside it, survives.
func TestDeleteFolderNeverFollowsLinks(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		d.WriteFile(t, "/mnt/us/koreader/keep.lua", "keep")
		d.WriteFile(t, root+"/Box/Inner/book.epub", "b")
		d.WriteFile(t, root+"/Box/Inner/book.sdr/metadata.epub.lua", "m")
		symlink(t, d, "/mnt/us/koreader", root+"/Box/out")
		symlink(t, d, "/mnt/us/koreader/keep.lua", root+"/Box/Inner/file-out")
		symlink(t, d, root+"/Sci-Fi", root+"/Box/Inner/in")
		_, err := New(fsys, root).Delete([]string{"Box"}, Options{})
		must(t, "delete", err)
		if exists(t, d, root+"/Box") {
			t.Fatal("Box still there")
		}
		if content(t, d, "/mnt/us/koreader/keep.lua") != "keep" || !exists(t, d, root+"/Sci-Fi/Foundation.pdf") || !exists(t, d, "/mnt/us/secret.txt") {
			t.Fatal("followed a link")
		}
	})
}

func TestDeleteRefuses(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		for p, want := range map[string]error{
			"":             ErrRoot,
			"Dune.sdr":     ErrNotFound,
			".hidden.epub": ErrNotFound,
			"escape":       ErrNotFound,
			"inner":        ErrNotFound,
			"Gone.epub":    ErrNotFound,
		} {
			_, err := lib.Delete([]string{"cover.png", p}, Options{ConfirmOpen: true})
			mustNot(t, p, err, want)
		}
		if !exists(t, d, root+"/cover.png") || !exists(t, d, root+"/Dune.sdr") || !exists(t, d, root+"/inner") {
			t.Fatal("deleted something")
		}
	})
}

func TestPreview(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		symlink(t, d, root+"/Sci-Fi", root+"/Sci-Fi/Asimov/loop")
		lib := New(fsys, root)
		p, err := lib.Preview([]string{"Sci-Fi"})
		must(t, "folder", err)
		if p != (Preview{Books: 2, Folders: 2, OpenNow: true}) {
			t.Fatalf("preview %+v", p)
		}
		p, err = lib.Preview([]string{"Dune.epub", "cover.png", "Empty"})
		must(t, "mixed", err)
		if p != (Preview{Books: 1, Folders: 1, Others: 1}) {
			t.Fatalf("preview %+v", p)
		}
		_, err = lib.Preview([]string{"../koreader"})
		mustNot(t, "outside", err, ErrOutside)
	})
}

// A Sidecar that two Books in the same folder share stays where it is:
// deleting, moving or renaming one of them leaves it to the other.
func TestSharedSidecarStays(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		pdf := func() {
			d.WriteFile(t, root+"/Dune.pdf", "pdf")
			d.WriteFile(t, root+"/Dune.sdr/metadata.pdf.lua", "return {}")
		}
		shared := func(what string, warns []Warning, err error, path string) {
			t.Helper()
			must(t, what, err)
			if len(warns) != 1 || warns[0].Code != WarnSidecarShared || warns[0].Path != path {
				t.Fatalf("%s: warnings %v", what, warns)
			}
			if !exists(t, d, root+"/Dune.sdr/metadata.epub.lua") {
				t.Fatalf("%s: shared sidecar touched", what)
			}
		}
		lib := New(fsys, root)
		pdf()
		warns, err := lib.Delete([]string{"Dune.pdf"}, Options{})
		shared("delete", warns, err, "Dune.pdf")
		pdf()
		warns, err = lib.Move([]string{"Dune.pdf"}, "Empty", Options{})
		shared("move", warns, err, "Dune.pdf")
		if exists(t, d, root+"/Empty/Dune.sdr") {
			t.Fatal("moved a shared sidecar")
		}
		pdf()
		warns, err = lib.Rename("Dune.epub", "Arrakis.epub", Options{})
		shared("rename", warns, err, "Dune.epub")
		if exists(t, d, root+"/Arrakis.sdr") {
			t.Fatal("renamed a shared sidecar")
		}
	})
}

func TestMetadataModes(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		settings(t, d, `["document_metadata_folder"] = "dir",`+"\n")
		lib := New(fsys, root)
		_, err := lib.Rename("Dune.epub", "Arrakis.epub", Options{})
		mustNot(t, "rename book", err, ErrMetadataMode)
		_, err = lib.Rename("Empty", "Void", Options{})
		mustNot(t, "rename folder", err, ErrMetadataMode)
		_, err = lib.Move([]string{"cover.png", "Neuromancer.mobi"}, "Empty", Options{})
		mustNot(t, "move", err, ErrMetadataMode)
		if !exists(t, d, root+"/cover.png") {
			t.Fatal("a refused batch moved something")
		}
		_, err = lib.Rename("cover.png", "front.png", Options{})
		must(t, "other files carry no progress", err)
		_, err = lib.CreateFolder("", "New")
		must(t, "create", err)
		_, err = lib.Delete([]string{"Neuromancer.mobi"}, Options{})
		must(t, "delete", err)

		settings(t, d, `["document_metadata_folder"] = "hash",`+"\n")
		_, err = lib.Move([]string{"Dune.epub"}, "Empty", Options{})
		must(t, "hash mode finds progress by content", err)
		if !exists(t, d, root+"/Empty/Dune.sdr") {
			t.Fatal("a doc sidecar still travels")
		}
		settings(t, d, `["document_metadata_folder"] = "elsewhere",`+"\n")
		_, err = lib.Move([]string{"Empty/Dune.epub"}, "", Options{})
		mustNot(t, "unknown mode", err, ErrMetadataMode)
	})
}

// Every operation refuses what Resolve refuses, plus links and the names
// the listing hides.
func TestNoOperationReachesOutside(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		opt := Options{ConfirmOpen: true}
		for _, p := range []string{
			"..", "../", "../koreader", "Sci-Fi/../../koreader", "/mnt/us/../..", "/mnt/us/documents/Dune.epub",
			"/etc", `..\koreader`, `Sci-Fi\..\..`, "Dune.epub\x00", "escape/settings.reader.lua", "../secret.txt",
		} {
			_, err := lib.Rename(p, "x", opt)
			mustNot(t, "rename "+p, err, ErrOutside)
			_, err = lib.Move([]string{p}, "Empty", opt)
			mustNot(t, "move "+p, err, ErrOutside)
			_, err = lib.Move([]string{"cover.png"}, p, opt)
			mustNot(t, "move to "+p, err, ErrOutside)
			_, err = lib.Delete([]string{p}, opt)
			mustNot(t, "delete "+p, err, ErrOutside)
			_, err = lib.CreateFolder(p, "x")
			mustNot(t, "create in "+p, err, ErrOutside)
			_, err = lib.Preview([]string{p})
			mustNot(t, "preview "+p, err, ErrOutside)
		}
		_, err := lib.Move([]string{"cover.png"}, "escape", opt)
		mustNot(t, "move into a link out", err, ErrOutside)
		_, err = lib.Rename("escape", "x", opt)
		mustNot(t, "rename a link", err, ErrNotFound)
		_, err = lib.Rename("Dune.sdr", "x.sdr2", opt)
		mustNot(t, "rename a sidecar", err, ErrNotFound)
		if !exists(t, d, "/mnt/us/secret.txt") || !exists(t, d, "/mnt/us/koreader/settings.reader.lua") || !exists(t, d, root+"/cover.png") {
			t.Fatal("touched")
		}
	})
}
