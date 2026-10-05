package library

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// source serves n bytes of a repeating pattern. When at is reached it calls
// hook once, and fails the read with the hook's error if it returns one.
type source struct {
	n, off int64
	at     int64
	hook   func() error
	hooked bool
	failAt int64 // when > 0, reads stop there with failErr
	failer error
}

func pattern(i int64) byte { return byte(i*7 + i/251) }

func (s *source) Read(p []byte) (int, error) {
	if s.failAt > 0 && s.off >= s.failAt {
		return 0, s.failer
	}
	if s.off >= s.n {
		return 0, io.EOF
	}
	if s.hook != nil && !s.hooked && s.off >= s.at {
		s.hooked = true
		if err := s.hook(); err != nil {
			return 0, err
		}
	}
	end := min(s.n, s.off+int64(len(p)))
	if s.hook != nil && !s.hooked && s.off < s.at {
		end = min(end, s.at)
	}
	if s.failAt > 0 {
		end = min(end, s.failAt)
	}
	for i := s.off; i < end; i++ {
		p[i-s.off] = pattern(i)
	}
	k := int(end - s.off)
	s.off = end
	return k, nil
}

func want(n int64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = pattern(int64(i))
	}
	return b
}

func upload(lib *Library, folder, name string, n int64, opt UploadOptions) (string, error) {
	p, _, err := lib.Upload(folder, name, n, &source{n: n}, opt)
	return p, err
}

// untouched fails the test if anything reads it.
type untouched struct{ t *testing.T }

func (u untouched) Read([]byte) (int, error) {
	u.t.Error("the upload was read although it was refused")
	return 0, io.EOF
}

func TestUploadWritesAPartThenRenames(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		const n = 3<<20 + 17
		final, part := root+"/Sci-Fi/Dune Messiah.epub", root+"/Sci-Fi/Dune Messiah.epub.part"
		src := &source{n: n, at: n / 2, hook: func() error {
			if !exists(t, d, part) || exists(t, d, final) {
				return errors.New("halfway: want only the .part on the Device")
			}
			return nil
		}}
		p, warns, err := lib.Upload("Sci-Fi", "Dune Messiah.epub", n, src, UploadOptions{})
		must(t, "upload", err)
		if p != "Sci-Fi/Dune Messiah.epub" || len(warns) != 0 {
			t.Fatalf("path %q, warnings %v", p, warns)
		}
		if exists(t, d, part) || !bytes.Equal([]byte(content(t, d, final)), want(n)) {
			t.Fatal("the uploaded file is not what was sent")
		}
	})
}

// The Device receives the file while it is still being read, so nothing
// holds it whole in memory on the way.
func TestUploadStreams(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		const n = 16 << 20
		part := d.Path(root + "/Big.pdf.part")
		src := &source{n: n, at: n - 1<<20, hook: func() error {
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				if fi, err := os.Stat(part); err == nil && fi.Size() >= 4<<20 {
					return nil
				}
			}
			return errors.New("15 MiB read and the Device still has less than 4 MiB of it")
		}}
		_, _, err := New(fsys, root).Upload("", "Big.pdf", n, src, UploadOptions{})
		must(t, "upload", err)
	})
}

func TestUploadRefusesWhatDoesNotFit(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		d.SetFreeSpace(64 << 20)
		lib := New(fsys, root)
		_, _, err := lib.Upload("", "Huge.cbz", 64<<20, untouched{t}, UploadOptions{})
		mustNot(t, "no room", err, ErrNoSpace)
		var nse *NoSpaceError
		if !errors.As(err, &nse) || nse.Need <= 64<<20 || nse.Free != 64<<20 {
			t.Fatalf("err = %#v", err)
		}
		if exists(t, d, root+"/Huge.cbz.part") || exists(t, d, root+"/Huge.cbz") {
			t.Fatal("something was written")
		}
		_, err = upload(lib, "", "Fits.cbz", 64<<20-UploadMargin, UploadOptions{})
		must(t, "just fits", err)
	})
}

func TestUploadNeverOverwritesUnlessAskedTo(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		before := listed(t, fsys, "")
		for _, name := range []string{"Dune.epub", "dune.EPUB", "EMPTY", "Sci-fi"} {
			_, _, err := lib.Upload("", name, 5, untouched{t}, UploadOptions{})
			mustNot(t, name, err, ErrExists)
		}
		_, _, err := lib.Upload("", "Empty", 5, untouched{t}, UploadOptions{Replace: true})
		mustNot(t, "replace a folder", err, ErrExists)
		if got := listed(t, fsys, ""); got != before || content(t, d, root+"/Dune.epub") != strings.Repeat("d", 1800) {
			t.Fatalf("listing changed: %s", got)
		}
	})
}

// Replacing a Book writes the new file under the name already there, so
// its Sidecar, and the reading progress in it, stays with it.
func TestReplaceKeepsTheSidecar(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		d.WriteFile(t, root+"/Dune.sdr/metadata.epub.lua", "return { percent_finished = 0.42 }")
		lib := New(fsys, root)
		before := listed(t, fsys, "")
		p, err := upload(lib, "", "DUNE.epub", 1000, UploadOptions{Replace: true})
		must(t, "replace", err)
		if p != "Dune.epub" || listed(t, fsys, "") != before {
			t.Fatalf("path %q, listing %s", p, listed(t, fsys, ""))
		}
		if !bytes.Equal([]byte(content(t, d, root+"/Dune.epub")), want(1000)) {
			t.Fatal("not replaced")
		}
		if content(t, d, root+"/Dune.sdr/metadata.epub.lua") != "return { percent_finished = 0.42 }" {
			t.Fatal("the progress was lost")
		}
		if names := deviceNames(t, d, root); slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, ".~") || strings.HasSuffix(n, ".part") }) {
			t.Fatalf("left behind: %v", names)
		}
	})
}

func deviceNames(t *testing.T, d disk, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(d.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestReplacingTheOpenBookNeedsConfirmation(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		_, _, err := lib.Upload("Sci-Fi", "Foundation.pdf", 4, untouched{t}, UploadOptions{Replace: true})
		mustNot(t, "unconfirmed", err, ErrOpenNow)
		_, err = upload(lib, "Sci-Fi", "Foundation.pdf", 4, UploadOptions{Replace: true, ConfirmOpen: true})
		must(t, "confirmed", err)
	})
}

// A file is only renamed into place when the name is still free once the
// upload is over; whatever took it meanwhile stays as it was.
func TestUploadChecksTheNameAgainBeforeTheRename(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		src := &source{n: 1 << 20, at: 1 << 19, hook: func() error {
			d.WriteFile(t, root+"/Sci-Fi/NEW.epub", "theirs")
			return nil
		}}
		_, _, err := lib.Upload("Sci-Fi", "New.epub", 1<<20, src, UploadOptions{})
		mustNot(t, "taken meanwhile", err, ErrExists)
		if content(t, d, root+"/Sci-Fi/NEW.epub") != "theirs" || exists(t, d, root+"/Sci-Fi/New.epub.part") {
			t.Fatal("overwrote it, or left the .part")
		}
	})
}

// An upload cut short, or longer than announced, leaves nothing behind.
func TestCutUploadRemovesThePart(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		cases := map[string]struct {
			src  io.Reader
			size int64
		}{
			"client gone": {&source{n: 2 << 20, failAt: 1 << 20, failer: io.ErrUnexpectedEOF}, 2 << 20},
			"read error":  {&source{n: 2 << 20, failAt: 1<<20 + 5, failer: errInjected}, 2 << 20},
			"short":       {&source{n: 1 << 20}, 2 << 20},
			"long":        {&source{n: 2<<20 + 1}, 2 << 20},
		}
		for what, c := range cases {
			_, _, err := lib.Upload("", "Cut.epub", c.size, c.src, UploadOptions{})
			mustNot(t, what, err, ErrIncomplete)
			if exists(t, d, root+"/Cut.epub.part") || exists(t, d, root+"/Cut.epub") {
				t.Fatalf("%s: left a file", what)
			}
		}
	})
}

func TestUploadRefusesBadNamesAndPlaces(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		for _, name := range []string{"a:b.epub", ".hidden.epub", "x.sdr", "x.epub.part", "../x.epub", "a/b.epub", `..\x.epub`, "", "x.epub "} {
			_, _, err := lib.Upload("", name, 1, untouched{t}, UploadOptions{})
			mustNot(t, fmt.Sprintf("name %q", name), err, ErrInvalidName)
		}
		for _, folder := range []string{"..", "../koreader", "/mnt/us", "escape", "Sci-Fi/../..", `..\koreader`, "Dune.sdr/.."} {
			_, _, err := lib.Upload(folder, "x.epub", 1, untouched{t}, UploadOptions{})
			mustNot(t, "folder "+folder, err, ErrOutside)
		}
		_, _, err := lib.Upload("Dune.epub", "x.epub", 1, untouched{t}, UploadOptions{})
		mustNot(t, "into a book", err, ErrNotFolder)
		for _, folder := range []string{"Dune.sdr", "Sci-Fi/Foundation.SDR", ".hidden"} {
			_, _, err = lib.Upload(folder, "x.epub", 1, untouched{t}, UploadOptions{})
			mustNot(t, "into "+folder, err, ErrNotFound)
		}
		if exists(t, d, "/mnt/us/koreader/x.epub") || exists(t, d, "/mnt/us/x.epub") {
			t.Fatal("wrote outside")
		}
	})
}

func TestCheckUpload(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		d.SetFreeSpace(10 << 20)
		lib := New(fsys, root)
		check := func(folder, name string, size int64) Arrival {
			t.Helper()
			a, err := lib.CheckUpload(folder, name, size)
			must(t, "check "+name, err)
			return a
		}
		if a := check("", "New.epub", 1<<20); a != (Arrival{Name: "New.epub", Book: true, Fits: true, FreeBytes: 10 << 20}) {
			t.Errorf("new book: %+v", a)
		}
		if a := check("", "notes.zip", 1); a.Book || !a.Fits {
			t.Errorf("other file: %+v", a)
		}
		if a := check("", "dune.epub", 1); a.Existing != "Dune.epub" || !a.Replaceable || a.OpenNow {
			t.Errorf("taken: %+v", a)
		}
		if a := check("Sci-Fi", "Foundation.pdf", 1); a.Existing != "Foundation.pdf" || !a.OpenNow {
			t.Errorf("open: %+v", a)
		}
		if a := check("", "empty", 1); a.Existing != "Empty" || a.Replaceable {
			t.Errorf("a folder: %+v", a)
		}
		if a := check("", "Huge.cbz", 10<<20); a.Fits {
			t.Errorf("huge: %+v", a)
		}
		if a := check("", "a?.epub", 1); a.Reason != ReasonReserved {
			t.Errorf("bad name: %+v", a)
		}
		if _, err := lib.CheckUpload("../koreader", "x.epub", 1); !errors.Is(err, ErrOutside) {
			t.Errorf("outside: %v", err)
		}
	})
}

func TestCleanPartsRemovesOnlyUploadLeftovers(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		leftovers := []string{root + "/Lost.epub.part", root + "/Sci-Fi/Asimov/I, Robot.pdf.part"}
		keep := []string{
			root + "/Dune.sdr/cache.part",        // inside a Sidecar
			root + "/.hidden.part",               // hidden: never ember's
			root + "/.part",                      // no name before it
			root + "/bad:name.part",              // a name ember would refuse
			root + "/Sci-Fi/Notes.PART",          // ember writes .part in lower case
			"/mnt/us/koreader/settings.lua.part", // outside, behind the escape link
			"/mnt/us/stray.part",                 // outside
			root + "/Dune.epub",
		}
		for _, p := range append(slices.Clone(leftovers), keep...) {
			d.WriteFile(t, p, "x")
		}
		mkdir(t, d, root+"/Folder.part/inside")
		keep = append(keep, root+"/Folder.part/inside")

		removed, err := New(fsys, root).CleanParts()
		must(t, "clean", err)
		slices.Sort(removed)
		if got := strings.Join(removed, "|"); got != "Lost.epub.part|Sci-Fi/Asimov/I, Robot.pdf.part" {
			t.Fatalf("removed %s", got)
		}
		for _, p := range leftovers {
			if exists(t, d, p) {
				t.Errorf("%s is still there", p)
			}
		}
		for _, p := range keep {
			if !exists(t, d, p) {
				t.Errorf("%s was removed", p)
			}
		}
	})
}

func TestDownload(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		for rel, body := range map[string]string{"Sci-Fi/Foundation.pdf": "ffff", "cover.png": "png", "inner/Foundation.pdf": "ffff"} {
			dl, err := lib.Download(rel)
			must(t, rel, err)
			got, err := io.ReadAll(dl.Body)
			dl.Body.Close()
			must(t, "read "+rel, err)
			if string(got) != body || dl.Size != int64(len(body)) || dl.Name != rel[strings.LastIndex(rel, "/")+1:] {
				t.Errorf("%s: %+v %q", rel, dl, got)
			}
		}
		for p, wantErr := range map[string]error{
			"Sci-Fi":                     ErrNotFile,
			"":                           ErrRoot,
			"../secret.txt":              ErrOutside,
			"/mnt/us/secret.txt":         ErrOutside,
			"escape/settings.reader.lua": ErrOutside,
			`..\secret.txt`:              ErrOutside,
			"Dune.sdr/metadata.epub.lua": ErrNotFound,
			"Dune.sdr":                   ErrNotFound,
			"escape":                     ErrNotFound,
			".hidden.epub":               ErrNotFound,
			"Nope.epub":                  ErrNotFound,
		} {
			_, err := lib.Download(p)
			mustNot(t, "download "+p, err, wantErr)
		}
	})
}
