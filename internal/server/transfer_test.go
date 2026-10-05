package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/library"
)

// liveServer serves a linked handler on a real loopback socket, for what a
// recorder cannot show: a body streamed in, a client going away.
func liveServer(t *testing.T, fake *fakedevice.Device) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(linkedHandler(t, fake, ln.Addr().String()))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

// gen produces n bytes of a fixed pattern. At byte at it calls hook once;
// a hook error ends the body there, as a browser tab closing would.
type gen struct {
	n, off, at int64
	hook       func() error
	fired      bool
}

func (g *gen) Read(p []byte) (int, error) {
	if g.off >= g.n {
		return 0, io.EOF
	}
	if g.hook != nil && !g.fired && g.off >= g.at {
		g.fired = true
		if err := g.hook(); err != nil {
			return 0, err
		}
	}
	end := min(g.n, g.off+int64(len(p)))
	if g.hook != nil && !g.fired {
		end = min(end, g.at)
	}
	for i := g.off; i < end; i++ {
		p[i-g.off] = byte(i*13 + i>>9)
	}
	k := int(end - g.off)
	g.off = end
	return k, nil
}

func digest(r io.Reader) [32]byte {
	h := sha256.New()
	_, _ = io.Copy(h, r)
	return [32]byte(h.Sum(nil))
}

func uploadRequest(base, query string, body io.Reader, size int64) *http.Request {
	req, _ := http.NewRequest("POST", base+"/api/library/upload?"+query, body)
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Origin", base)
	return req
}

// waitFor polls cond for up to 10s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
	}
}

// partsIn lists the Device paths of the upload parts in dir.
func partsIn(t *testing.T, fake *fakedevice.Device, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(fake.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if library.IsPart(e.Name()) {
			out = append(out, dir+"/"+e.Name())
		}
	}
	return out
}

// onePart is the Device path of the only part in dir, or "".
func onePart(t *testing.T, fake *fakedevice.Device, dir string) string {
	t.Helper()
	if p := partsIn(t, fake, dir); len(p) == 1 {
		return p[0]
	}
	return ""
}

func fileSize(fake *fakedevice.Device, p string) int64 {
	fi, err := os.Stat(fake.Path(p))
	if err != nil {
		return -1
	}
	return fi.Size()
}

// A large upload reaches the Device while the browser is still sending it,
// and the server's heap never holds more than a small part of it.
func TestUploadStreamsToTheDevice(t *testing.T) {
	fake := fakedevice.New(t)
	fake.WriteFile(t, fakedevice.Documents+"/Sci-Fi/.keep", "")
	base := liveServer(t, fake)
	const n = 64 << 20
	sci := fakedevice.Documents + "/Sci-Fi"

	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	baseline := ms.HeapAlloc
	go func() {
		defer close(sampled)
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak.Load() {
				peak.Store(ms.HeapAlloc)
			}
		}
	}()

	src := &gen{n: n, at: n - 4<<20, hook: func() error {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if part := onePart(t, fake, sci); part != "" && fileSize(fake, part) >= 16<<20 {
				return nil
			}
		}
		return errors.New("60 MiB sent and the Device has less than 16 MiB of it")
	}}
	res, err := http.DefaultClient.Do(uploadRequest(base, "folder=Sci-Fi&name=Saga%2002.cbz&view=Sci-Fi", src, n))
	close(stop)
	<-sampled
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	if !strings.Contains(string(body), `"path":"Sci-Fi/Saga 02.cbz"`) || !strings.Contains(string(body), `"name":"Saga 02.cbz"`) {
		t.Fatalf("answer %s", body)
	}
	f, err := os.Open(fake.Path(fakedevice.Documents + "/Sci-Fi/Saga 02.cbz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if digest(f) != digest(&gen{n: n}) || len(partsIn(t, fake, sci)) != 0 {
		t.Fatal("the file on the Device is not what was sent")
	}
	grew := int64(peak.Load()) - int64(baseline)
	t.Logf("heap grew by %.1f MiB at most", float64(grew)/(1<<20))
	if grew > 24<<20 {
		t.Fatalf("heap grew by %d MiB for a %d MiB upload", grew>>20, n>>20)
	}
}

// A browser that cancels the upload closes the connection mid-body; the
// part it had sent is deleted.
func TestCancelledUploadLeavesNothing(t *testing.T) {
	fake := fakedevice.New(t)
	doc := fakedevice.Documents
	sentinel := doc + "/.ember-0123456789abcdef.part"
	fake.WriteFile(t, sentinel, "x")
	base := liveServer(t, fake)
	// Let the clean-up of the new link run first, so it cannot be what
	// removes the part below.
	waitFor(t, "the clean-up", func() bool { return fileSize(fake, sentinel) == -1 })
	src := &gen{n: 8 << 20, at: 2 << 20, hook: func() error {
		waitFor(t, "the part to appear", func() bool {
			part := onePart(t, fake, doc)
			return part != "" && fileSize(fake, part) > 0
		})
		return errors.New("tab closed")
	}}
	if res, err := http.DefaultClient.Do(uploadRequest(base, "folder=&name=Gone.epub", src, 8<<20)); err == nil {
		res.Body.Close()
		t.Fatalf("the upload went through: %d", res.StatusCode)
	}
	waitFor(t, "the part to go", func() bool { return len(partsIn(t, fake, doc)) == 0 })
	if fileSize(fake, fakedevice.Documents+"/Gone.epub") != -1 {
		t.Fatal("a cut upload was renamed into place")
	}
}

func upload(h http.Handler, query, body string, headers ...string) *httptest.ResponseRecorder {
	return call(h, "POST", "/api/library/upload?"+query, self, origin, body, append([]string{"Content-Type", "application/octet-stream"}, headers...)...)
}

func TestUploadOverTheAPI(t *testing.T) {
	fake, h := opLibrary(t)
	doc := fakedevice.Documents
	fake.WriteFile(t, doc+"/Dune.sdr/metadata.epub.lua", "return { percent_finished = 0.5 }")

	rec := upload(h, "folder=Sci-Fi&name=Hyperion.epub&view=Sci-Fi", "hyperion")
	if a := answer(t, rec); rec.Code != http.StatusOK || a.Path != "Sci-Fi/Hyperion.epub" || fileNames(a.Folder) != "Foundation.pdf,Hyperion.epub" {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if fake.ReadFile(t, doc+"/Sci-Fi/Hyperion.epub") != "hyperion" {
		t.Fatal("content")
	}

	rec = upload(h, "folder=&name=dune.epub", "new dune")
	if a := answer(t, rec); rec.Code != http.StatusConflict || a.Error != "collision" || fake.ReadFile(t, doc+"/Dune.epub") != "dune" {
		t.Fatalf("collision: %d %s", rec.Code, rec.Body)
	}
	rec = upload(h, "folder=&name=dune.epub&replace=1&view=", "new dune")
	if a := answer(t, rec); rec.Code != http.StatusOK || a.Path != "Dune.epub" || !a.Folder.Entries[1].HasSidecar {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body)
	}
	if fake.ReadFile(t, doc+"/Dune.epub") != "new dune" || fake.ReadFile(t, doc+"/Dune.sdr/metadata.epub.lua") != "return { percent_finished = 0.5 }" {
		t.Fatal("replace lost the content or the progress")
	}

	rec = upload(h, "folder=Sci-Fi&name=Foundation.pdf&replace=1", "f2")
	if answer(t, rec).Error != "open_now_needs_confirm" {
		t.Fatalf("open book: %d %s", rec.Code, rec.Body)
	}
	if rec = upload(h, "folder=Sci-Fi&name=Foundation.pdf&replace=1&confirmOpen=1", "f2"); rec.Code != http.StatusOK {
		t.Fatalf("open book confirmed: %d %s", rec.Code, rec.Body)
	}
}

func TestUploadRefusals(t *testing.T) {
	fake, h := opLibrary(t)
	cases := []struct {
		query  string
		status int
		code   string
	}{
		{"folder=&name=a:b.epub", http.StatusBadRequest, "invalid_name"},
		{"folder=&name=..%2Fx.epub", http.StatusBadRequest, "invalid_name"},
		{"folder=&name=.ember-0123456789abcdef.part", http.StatusBadRequest, "invalid_name"},
		{"folder=..%2Fkoreader&name=x.epub", http.StatusBadRequest, "outside_library"},
		{"folder=%2Fmnt%2Fus&name=x.epub", http.StatusBadRequest, "outside_library"},
		{"folder=..%5Ckoreader&name=x.epub", http.StatusBadRequest, "outside_library"},
		{"folder=Dune.sdr&name=x.epub", http.StatusNotFound, "not_found"},
		{"folder=Dune.epub&name=x.epub", http.StatusConflict, "not_folder"},
		{"folder=Nope&name=x.epub", http.StatusNotFound, "not_found"},
		{"folder=&name=Sci-Fi&replace=1", http.StatusConflict, "collision"},
	}
	for _, c := range cases {
		rec := upload(h, c.query, "data")
		if a := answer(t, rec); rec.Code != c.status || a.Error != c.code {
			t.Errorf("%s: %d %s", c.query, rec.Code, rec.Body)
		}
	}
	if fake.ReadFile(t, "/mnt/us/koreader/x.epub") != "" || fake.ReadFile(t, "/mnt/us/x.epub") != "" {
		t.Fatal("wrote outside the library")
	}

	fake.SetFreeSpace(1 << 20)
	rec := upload(h, "folder=&name=Big.epub", "more than nothing")
	var full struct {
		Error      string
		Need, Free uint64
	}
	if err := decodeInto(rec, &full); err != nil || rec.Code != http.StatusInsufficientStorage || full.Error != "no_space" || full.Free != 1<<20 || full.Need != 17+library.UploadMargin {
		t.Fatalf("no space: %d %s", rec.Code, rec.Body)
	}

	if rec := call(h, "POST", "/api/library/upload?folder=&name=x.epub", self, origin, "x", "Content-Type", "text/plain"); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", rec.Code)
	}
	if rec := call(h, "POST", "/api/library/upload?folder=&name=x.epub", self, "http://evil.example", "x", "Content-Type", "application/octet-stream"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin: %d", rec.Code)
	}
	if rec := call(h, "GET", "/api/library/upload?folder=&name=x.epub", self, "", ""); rec.Code < 400 {
		t.Errorf("GET: %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/api/library/upload?folder=&name=x.epub", strings.NewReader("x"))
	req.Host, req.ContentLength = self, -1
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/octet-stream")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusLengthRequired {
		t.Errorf("no length: %d", rec.Code)
	}
	if onDisk(t, fake, fakedevice.Documents+"/x.epub") {
		t.Fatal("refused uploads were written")
	}
}

func decodeInto(rec *httptest.ResponseRecorder, v any) error {
	return json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(v)
}

func TestUploadCheckOverTheAPI(t *testing.T) {
	_, h := opLibrary(t)
	rec := get(t, h, "/api/library/upload/check?folder=&name=DUNE.epub&size=10")
	var a library.Arrival
	if err := decodeInto(rec, &a); err != nil || rec.Code != http.StatusOK || a.Existing != "Dune.epub" || !a.Replaceable || !a.Book || !a.Fits {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/api/library/upload/check?folder=&name=notes.zip&size=1"); !strings.Contains(rec.Body.String(), `"book":false`) {
		t.Errorf("other: %s", rec.Body)
	}
	for _, q := range []string{"folder=&name=x&size=-1", "folder=&name=x&size=", "folder=&name=x&size=1e3"} {
		if rec := get(t, h, "/api/library/upload/check?"+q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	if rec := get(t, h, "/api/library/upload/check?folder=..&name=x&size=1"); rec.Code != http.StatusBadRequest {
		t.Errorf("outside: %d", rec.Code)
	}
}

func TestDownloadOverTheAPI(t *testing.T) {
	fake, h := opLibrary(t)
	doc := fakedevice.Documents
	fake.WriteFile(t, doc+"/Sci-Fi/Ação & Aventura.epub", "aventura")
	fake.WriteFile(t, "/mnt/us/secret.txt", "secret")

	rec := get(t, h, "/api/library/download?path="+url.QueryEscape("Sci-Fi/Ação & Aventura.epub"))
	hd := rec.Header()
	if rec.Code != http.StatusOK || rec.Body.String() != "aventura" || hd.Get("Content-Length") != "8" || hd.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download: %d %v %q", rec.Code, hd, rec.Body)
	}
	if got, want := hd.Get("Content-Disposition"), "attachment; filename*=utf-8''A%C3%A7%C3%A3o%20&%20Aventura.epub"; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	if got := get(t, h, "/api/library/download?path=Dune.epub").Header().Get("Content-Disposition"); got != `attachment; filename=Dune.epub` {
		t.Errorf("Content-Disposition = %q", got)
	}

	cases := map[string]struct {
		status int
		code   string
	}{
		"../secret.txt":                       {http.StatusBadRequest, "outside_library"},
		"%2Fmnt%2Fus%2Fsecret.txt":            {http.StatusBadRequest, "outside_library"},
		"..%5Csecret.txt":                     {http.StatusBadRequest, "outside_library"},
		"Sci-Fi%2F..%2F..%2Fkoreader%2Fx.lua": {http.StatusBadRequest, "outside_library"},
		"Sci-Fi":                              {http.StatusConflict, "not_file"},
		"":                                    {http.StatusBadRequest, "library_root"},
		"Dune.sdr%2Fmetadata.epub.lua":        {http.StatusNotFound, "not_found"},
		"Nope.epub":                           {http.StatusNotFound, "not_found"},
	}
	for q, c := range cases {
		rec := get(t, h, "/api/library/download?path="+q)
		if rec.Code != c.status || errorCode(t, rec.Body.Bytes()) != c.code || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
	if rec := call(h, "GET", "/api/library/download?path=Dune.epub", self, "", "", "Sec-Fetch-Site", "cross-site"); rec.Code != http.StatusForbidden || rec.Body.String() == "dune" {
		t.Errorf("cross-site: %d", rec.Code)
	}
}

// Parts left by uploads a dropped link cut short are removed as soon as the
// next link is made, wherever they are in the Library; nothing else is.
func TestLeftoverPartsAreCleanedOnLink(t *testing.T) {
	fake := fakedevice.New(t)
	doc := fakedevice.Documents
	const a, b = "/.ember-0123456789abcdef.part", "/.ember-fedcba9876543210.part"
	gone := []string{doc + a, doc + "/Sci-Fi/Deep" + b}
	kept := []string{doc + "/Lost.epub.part", doc + "/Dune.sdr" + a, "/mnt/us" + a}
	for _, p := range append(slices.Clone(gone), kept...) {
		fake.WriteFile(t, p, "x")
	}
	h := linkedServer(t, fake)
	// No request is made: the link itself starts the clean-up.
	waitFor(t, "the clean-up", func() bool { return !onDisk(t, fake, gone[0]) && !onDisk(t, fake, gone[1]) })
	for _, p := range kept {
		if !onDisk(t, fake, p) {
			t.Errorf("%s was removed", p)
		}
	}

	// Within the same link, a part is not ember's to clean up: it could be
	// an upload in progress.
	fake.WriteFile(t, doc+a, "x")
	get(t, h, "/api/library/folder?path=")
	if rec := upload(h, "folder=&name=Sync.epub", "x"); rec.Code != http.StatusOK || !onDisk(t, fake, doc+a) {
		t.Fatalf("cleaned within one link: %d %s", rec.Code, rec.Body)
	}

	fake.Stop()
	fake.Start()
	waitFor(t, "the next link", func() bool { return !onDisk(t, fake, doc+a) })
}

// blockedBody is an upload body that sends a little and then waits.
type blockedBody struct {
	sent    bool
	release chan struct{}
}

func (b *blockedBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, "start"), nil
	}
	<-b.release
	return 0, errors.New("tab closed")
}

// While an upload streams in, other writes still run: the write lock is
// only held for the upload's checks and its final rename.
func TestWritesGoOnWhileAnUploadStreams(t *testing.T) {
	fake, h := opLibrary(t)
	doc := fakedevice.Documents
	body := &blockedBody{release: make(chan struct{})}
	uploaded := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("POST", "/api/library/upload?folder=&name=Slow.epub", body)
		req.Host = self
		req.Header.Set("Origin", "http://"+self)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = 1 << 20
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		uploaded <- rec.Code
	}()
	waitFor(t, "the upload to stream", func() bool { return onePart(t, fake, doc) != "" })

	within := func(what string, f func() *httptest.ResponseRecorder) {
		t.Helper()
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- f() }()
		select {
		case rec := <-done:
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", what, rec.Code, rec.Body)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s waited for the upload", what)
		}
	}
	within("rename", func() *httptest.ResponseRecorder {
		return post(t, h, "/api/library/rename", `{"path":"Dune.epub","name":"Dune Messiah.epub"}`)
	})
	within("settings", func() *httptest.ResponseRecorder {
		return post(t, h, "/api/settings", `{"keepAwake":false}`)
	})
	within("forget", func() *httptest.ResponseRecorder {
		return post(t, h, "/api/settings/forget", `{"confirm":true}`)
	})
	close(body.release)
	// The link is gone with the forgotten Kindle, so the part stays for
	// the clean-up of a later link.
	if code := <-uploaded; code == http.StatusOK {
		t.Fatal("the cut upload went through")
	}
}

// Two uploads racing for one name: one lands whole, the other is refused.
func TestUploadsDoNotRace(t *testing.T) {
	fake, h := opLibrary(t)
	for i := range 10 {
		name := fmt.Sprintf("same%d.epub", i)
		bodies := []string{strings.Repeat("a", 300<<10), strings.Repeat("b", 300<<10)}
		codes := make(chan int, 2)
		for _, body := range bodies {
			go func() { codes <- upload(h, "folder=&name="+name, body).Code }()
		}
		if got := <-codes + <-codes; got != http.StatusOK+http.StatusConflict {
			t.Fatalf("round %d: codes sum %d", i, got)
		}
		if got := fake.ReadFile(t, fakedevice.Documents+"/"+name); got != bodies[0] && got != bodies[1] {
			t.Fatalf("round %d: the file mixes both uploads", i)
		}
		if len(partsIn(t, fake, fakedevice.Documents)) != 0 {
			t.Fatalf("round %d: a part was left", i)
		}
	}
}
