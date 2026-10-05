package library

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/koreader"
)

const root = "/mnt/us/documents"

// disk writes test files at Device paths, whatever backs them.
type disk interface {
	WriteFile(t testing.TB, devicePath, content string)
	Path(devicePath string) string
	SetFreeSpace(free uint64)
}

// backends runs a test against a local directory and against the fake
// Device over real SFTP.
func backends(t *testing.T, test func(t *testing.T, fsys FS, d disk)) {
	t.Run("dir", func(t *testing.T) {
		d := newDirFS(t)
		test(t, d, d)
	})
	t.Run("device", func(t *testing.T) {
		fake := fakedevice.New(t, fakedevice.Passwordless())
		dev, err := device.Dial(context.Background(), device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { dev.Close() })
		test(t, dev, fake)
	})
}

func mkdir(t *testing.T, d disk, p string) {
	t.Helper()
	if err := os.MkdirAll(d.Path(p), 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, d disk, target, link string) {
	t.Helper()
	if err := os.Symlink(d.Path(target), d.Path(link)); err != nil {
		t.Fatal(err)
	}
}

func settings(t *testing.T, d disk, body string) {
	t.Helper()
	d.WriteFile(t, koreader.SettingsPath, "-- we can read Lua syntax here!\nreturn {\n"+body+"}\n")
}

// library lays out a small Library:
//
//	Dune.epub + Dune.sdr, Neuromancer.mobi, cover.png, .hidden.epub,
//	Empty/, Sci-Fi/{Foundation.pdf, Foundation.sdr/, Asimov/Robots.azw3},
//	escape -> /mnt/us/koreader, inner -> Sci-Fi
func library(t *testing.T, d disk) {
	t.Helper()
	d.WriteFile(t, root+"/Dune.epub", strings.Repeat("d", 1800))
	d.WriteFile(t, root+"/Dune.sdr/metadata.epub.lua", "return {}")
	d.WriteFile(t, root+"/Neuromancer.mobi", "nnn")
	d.WriteFile(t, root+"/cover.png", "png")
	d.WriteFile(t, root+"/.hidden.epub", "h")
	mkdir(t, d, root+"/Empty")
	d.WriteFile(t, root+"/Sci-Fi/Foundation.pdf", "ffff")
	d.WriteFile(t, root+"/Sci-Fi/Foundation.sdr/metadata.pdf.lua", "return {}")
	d.WriteFile(t, root+"/Sci-Fi/Asimov/Robots.azw3", "r")
	d.WriteFile(t, "/mnt/us/secret.txt", "outside")
	symlink(t, d, "/mnt/us/koreader", root+"/escape")
	symlink(t, d, root+"/Sci-Fi", root+"/inner")
	settings(t, d, `    ["lastfile"] = "/mnt/us/documents/Sci-Fi/Foundation.pdf",`+"\n")
}

func names(f Folder) []string {
	var out []string
	for _, e := range f.Entries {
		out = append(out, e.Name)
	}
	return out
}

func entry(t *testing.T, f Folder, name string) Entry {
	t.Helper()
	for _, e := range f.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("%q not listed in %v", name, names(f))
	return Entry{}
}

func TestListShowsFoldersThenFilesAndHidesSidecars(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		f, err := New(fsys, root).List("")
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Join(names(f), ","), "Empty,Sci-Fi,cover.png,Dune.epub,Neuromancer.mobi"; got != want {
			t.Fatalf("entries = %s, want %s", got, want)
		}
		if f.Path != "" || f.Books != 2 || f.Folders != 2 || f.Others != 1 {
			t.Fatalf("folder = %+v", f)
		}

		dune := entry(t, f, "Dune.epub")
		if dune.Kind != KindBook || dune.Format != "EPUB" || dune.Size != 1800 || !dune.HasSidecar || dune.Path != "Dune.epub" || dune.OpenNow {
			t.Fatalf("Dune = %+v", dune)
		}
		if n := entry(t, f, "Neuromancer.mobi"); n.Kind != KindBook || n.HasSidecar {
			t.Fatalf("Neuromancer = %+v", n)
		}
		if c := entry(t, f, "cover.png"); c.Kind != KindOther || c.Format != "PNG" {
			t.Fatalf("cover = %+v", c)
		}
		if s := entry(t, f, "Sci-Fi"); s.Kind != KindFolder || s.Items != 2 || s.Subfolders != 1 || s.Path != "Sci-Fi" {
			t.Fatalf("Sci-Fi = %+v", s)
		}
		if e := entry(t, f, "Empty"); e.Items != 0 || e.Subfolders != 0 {
			t.Fatalf("Empty = %+v", e)
		}
	})
}

func TestListSubfolderFlagsTheOpenBook(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		f, err := New(fsys, root).List("Sci-Fi/")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(names(f), ","); got != "Asimov,Foundation.pdf" {
			t.Fatalf("entries = %s", got)
		}
		fd := entry(t, f, "Foundation.pdf")
		if !fd.OpenNow || fd.Path != "Sci-Fi/Foundation.pdf" || !fd.HasSidecar || fd.Format != "PDF" {
			t.Fatalf("Foundation = %+v", fd)
		}
		if f.Path != "Sci-Fi" || f.Books != 1 || f.Folders != 1 {
			t.Fatalf("folder = %+v", f)
		}
		deep, err := New(fsys, root).List("Sci-Fi/Asimov")
		if err != nil {
			t.Fatal(err)
		}
		if r := entry(t, deep, "Robots.azw3"); r.Kind != KindBook || r.Path != "Sci-Fi/Asimov/Robots.azw3" {
			t.Fatalf("Robots = %+v", r)
		}
	})
}

func TestNothingOutsideTheRootIsReachable(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		for _, p := range []string{
			"..",
			"../",
			"../koreader",
			"Sci-Fi/../../koreader",
			"Sci-Fi/..",
			"/mnt/us/../..",
			"/mnt/us/documents",
			"/etc",
			`..\koreader`,
			`Sci-Fi\..\..`,
			"Sci-Fi\x00",
			"escape",
			"escape/settings",
		} {
			if _, err := lib.List(p); !errors.Is(err, ErrOutside) {
				t.Errorf("List(%q): err = %v, want ErrOutside", p, err)
			}
			if _, err := lib.Resolve(p); !errors.Is(err, ErrOutside) {
				t.Errorf("Resolve(%q): err = %v, want ErrOutside", p, err)
			}
		}
	})
}

// Confinement is by where a path really is: a link that stays inside the
// Library is followed, one that leaves it is refused.
func TestLinkInsideTheRootResolves(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		got, err := lib.Resolve("inner/Foundation.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(got, "/documents/Sci-Fi/Foundation.pdf") {
			t.Fatalf("Resolve = %q", got)
		}
		if p, err := lib.Resolve("Sci-Fi/./New Folder"); err != nil || !strings.HasSuffix(p, "/documents/Sci-Fi/New Folder") {
			t.Fatalf("a name that does not exist yet: %q, %v", p, err)
		}
	})
}

func TestListRefusesMissingAndNonFolders(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		lib := New(fsys, root)
		if _, err := lib.List("Nope"); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing: err = %v", err)
		}
		if _, err := lib.List("Dune.epub"); !errors.Is(err, ErrNotFolder) {
			t.Errorf("file: err = %v", err)
		}
	})
}

func TestOpenNow(t *testing.T) {
	cases := map[string]struct {
		settings string // "" writes no settings file at all
		want     string
	}{
		"present":        {`["lastfile"] = "/mnt/us/documents/Sci-Fi/Foundation.pdf",`, "Sci-Fi/Foundation.pdf"},
		"missing file":   {`["lastfile"] = "/mnt/us/documents/Gone.epub",`, ""},
		"outside root":   {`["lastfile"] = "/mnt/us/secret.txt",`, ""},
		"climbs out":     {`["lastfile"] = "/mnt/us/documents/../secret.txt",`, ""},
		"relative":       {`["lastfile"] = "Sci-Fi/Foundation.pdf",`, ""},
		"not a string":   {`["lastfile"] = 42,`, ""},
		"unparsable":     {"\x00\xff{{{ [\"lastfile\"] = \"/mnt/us/documents/Dune.epub", ""},
		"no lastfile":    {`["home_dir"] = "/mnt/us",`, ""},
		"settings gone":  {"", ""},
		"is the sidecar": {`["lastfile"] = "/mnt/us/documents/Dune.sdr",`, ""},
	}
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		for name, c := range cases {
			if c.settings == "" {
				if err := os.Remove(d.Path(koreader.SettingsPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Fatal(err)
				}
			} else {
				settings(t, d, c.settings+"\n")
			}
			lib := New(fsys, root)
			st, err := lib.Stats()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if st.OpenNow != c.want {
				t.Errorf("%s: OpenNow = %q, want %q", name, st.OpenNow, c.want)
			}
			f, err := lib.List("Sci-Fi")
			if err != nil {
				t.Fatalf("%s: listing broke: %v", name, err)
			}
			if got := entry(t, f, "Foundation.pdf").OpenNow; got != (c.want == "Sci-Fi/Foundation.pdf") {
				t.Errorf("%s: Foundation flagged %v", name, got)
			}
		}
	})
}

func TestStats(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		library(t, d)
		st, err := New(fsys, root+"/").Stats()
		if err != nil {
			t.Fatal(err)
		}
		if st.Root != root || st.FreeBytes == 0 || st.TotalBytes < st.FreeBytes || st.MetadataMode != koreader.MetadataDoc {
			t.Fatalf("stats = %+v", st)
		}
		settings(t, d, `["document_metadata_folder"] = "hash",`+"\n")
		if st, _ := New(fsys, root).Stats(); st.MetadataMode != koreader.MetadataHash {
			t.Fatalf("metadata mode = %q", st.MetadataMode)
		}
	})
}

func TestFakeDeviceReportsItsFreeSpace(t *testing.T) {
	fake := fakedevice.New(t, fakedevice.Passwordless())
	dev, err := device.Dial(context.Background(), device.Config{Addr: fake.Addr(), User: "root", HostKey: fake.HostKey()})
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	st, err := New(dev, root).Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.TotalBytes != 1<<30 || st.FreeBytes != 3<<28 {
		t.Fatalf("free %d of %d", st.FreeBytes, st.TotalBytes)
	}
}

func TestMissingRoot(t *testing.T) {
	backends(t, func(t *testing.T, fsys FS, d disk) {
		lib := New(fsys, "/mnt/us/nothing")
		if _, err := lib.List(""); !errors.Is(err, ErrNotFound) {
			t.Errorf("List: %v", err)
		}
		if _, err := lib.Stats(); !errors.Is(err, ErrNotFound) {
			t.Errorf("Stats: %v", err)
		}
	})
}

func TestBookFormat(t *testing.T) {
	cases := map[string]string{
		"a.epub": "EPUB", "A.EPUB": "EPUB", "a.fb2.zip": "FB2.ZIP", "a.pdf": "PDF",
		"a.djvu": "DJVU", "a.cbz": "CBZ", "a.azw3": "AZW3", "a.kfx": "KFX", "a.txt": "TXT",
		"a.zip": "", "a.png": "", "README": "", "a.epub.part": "", ".epub": "",
	}
	for name, want := range cases {
		if got, _ := BookFormat(name); got != want {
			t.Errorf("BookFormat(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSidecarName(t *testing.T) {
	cases := map[string]string{
		"Dune.epub": "Dune.sdr", "x.fb2.zip": "x.fb2.sdr", "My Book.v2.pdf": "My Book.v2.sdr", "README": "README.sdr",
	}
	for book, want := range cases {
		if got := SidecarName(book); got != want {
			t.Errorf("SidecarName(%q) = %q, want %q", book, got, want)
		}
	}
}

// dirFS serves Device paths from a local directory, mapped the same way the
// fake Device maps them, with links written as host paths.
type dirFS struct {
	root string
	free atomic.Uint64
}

func newDirFS(t *testing.T) *dirFS {
	d := &dirFS{root: t.TempDir()}
	d.free.Store(fakedevice.DefaultFree)
	mkdir(t, d, root)
	return d
}

func (d *dirFS) Path(p string) string {
	return filepath.Join(d.root, filepath.FromSlash(path.Clean("/"+p)))
}

func (d *dirFS) WriteFile(t testing.TB, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(d.Path(p)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.Path(p), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (d *dirFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(d.Path(p))
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, fi)
	}
	return out, nil
}

func (d *dirFS) Stat(p string) (fs.FileInfo, error) { return os.Stat(d.Path(p)) }

func (d *dirFS) ReadFile(p string, max int64) ([]byte, error) {
	b, err := os.ReadFile(d.Path(p))
	if err == nil && int64(len(b)) > max {
		return nil, device.ErrTooLarge
	}
	return b, err
}

func (d *dirFS) FreeSpace(p string) (free, total uint64, err error) {
	if _, err := os.Stat(d.Path(p)); err != nil {
		return 0, 0, err
	}
	return d.free.Load(), fakedevice.TotalSpace, nil
}

func (d *dirFS) SetFreeSpace(free uint64) { d.free.Store(free) }

func (d *dirFS) Create(p string, src io.Reader) (int64, error) {
	f, err := os.Create(d.Path(p))
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func (d *dirFS) Open(p string) (io.ReadCloser, error) { return os.Open(d.Path(p)) }

func (d *dirFS) Mkdir(p string) error         { return os.Mkdir(d.Path(p), 0o755) }
func (d *dirFS) Rename(from, to string) error { return os.Rename(d.Path(from), d.Path(to)) }
func (d *dirFS) Remove(p string) error        { return os.Remove(d.Path(p)) }

func (d *dirFS) RealPath(p string) (string, error) {
	p = path.Clean("/" + p)
	base, err := filepath.EvalSymlinks(d.root)
	if err != nil {
		return "", err
	}
	host, err := filepath.EvalSymlinks(d.Path(p))
	if errors.Is(err, fs.ErrNotExist) {
		if _, lerr := os.Lstat(d.Path(p)); lerr == nil {
			return "", err
		}
		parent, perr := filepath.EvalSymlinks(d.Path(path.Dir(p)))
		if perr != nil {
			return "", perr
		}
		host, err = filepath.Join(parent, path.Base(p)), nil
	}
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, host)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", os.ErrPermission
	}
	return path.Join("/", filepath.ToSlash(rel)), nil
}
