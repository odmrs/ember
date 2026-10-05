package library

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckRootAcceptsAFolderInUserStorage(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		mkdir(t, d, "/mnt/us/books/fiction")
		for in, want := range map[string]string{
			"/mnt/us/documents":        "/mnt/us/documents",
			"/mnt/us/books/fiction/":   "/mnt/us/books/fiction",
			"/mnt/us//books/./fiction": "/mnt/us/books/fiction",
		} {
			got, err := CheckRoot(fsys, in)
			if err != nil || got != want {
				t.Errorf("CheckRoot(%q) = %q, %v; want %q", in, got, err, want)
			}
		}
	})
}

func TestCheckRootRefusesPathsOutsideUserStorage(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		mkdir(t, d, "/mnt/base-us/documents")
		mkdir(t, d, "/etc")
		for _, in := range []string{
			"", "documents", "/", "/mnt", "/mnt/us", "/mnt/us/", "/mnt/usb", "/etc",
			"/mnt/us/../base-us/documents", "/mnt/us/documents/..", "/mnt/us/doc\\uments",
			"/mnt/us/docu\x00ments", "/mnt/us/docu\nments", "/mnt/us/" + strings.Repeat("a", 1100),
		} {
			if _, err := CheckRoot(fsys, in); !errors.Is(err, ErrRootOutside) {
				t.Errorf("CheckRoot(%q) err = %v, want ErrRootOutside", in, err)
			}
		}
	})
}

// A link inside user storage that points out of it is judged by where it
// leads.
func TestCheckRootFollowsLinksOut(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		mkdir(t, d, "/etc/secrets")
		symlink(t, d, "/etc/secrets", "/mnt/us/escape")
		if _, err := CheckRoot(fsys, "/mnt/us/escape"); !errors.Is(err, ErrRootOutside) {
			t.Fatalf("err = %v, want ErrRootOutside", err)
		}
	})
}

func TestCheckRootNeedsAnExistingFolder(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		d.WriteFile(t, "/mnt/us/notes.txt", "x")
		if _, err := CheckRoot(fsys, "/mnt/us/missing"); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing folder: err = %v, want ErrNotFound", err)
		}
		if _, err := CheckRoot(fsys, "/mnt/us/notes.txt"); !errors.Is(err, ErrNotFolder) {
			t.Errorf("file: err = %v, want ErrNotFolder", err)
		}
	})
}

// What is plainly outside is refused before the Device is asked anything:
// a nil FS would panic if it were used.
func TestCheckRootRefusesWithoutAskingTheDevice(t *testing.T) {
	for _, in := range []string{"/mnt/us", "/mnt/us/", "/etc", "/mnt/us/../etc", "relative"} {
		if _, err := CheckRoot(nil, in); !errors.Is(err, ErrRootOutside) {
			t.Errorf("CheckRoot(%q) err = %v", in, err)
		}
	}
}

func TestCheckRootRefusesALinkToUserStorageItself(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		symlink(t, d, "/mnt/us", "/mnt/us/everything")
		if _, err := CheckRoot(fsys, "/mnt/us/everything"); !errors.Is(err, ErrRootOutside) {
			t.Fatalf("err = %v, want ErrRootOutside", err)
		}
	})
}
