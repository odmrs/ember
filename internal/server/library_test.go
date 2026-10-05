package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/library"
	"github.com/odmrs/ember/internal/pairing"
	"github.com/odmrs/ember/internal/profile"
)

// linkedServer pairs a Profile with fake, opens a Session and waits until
// the Connector holds the link.
func linkedServer(t *testing.T, fake *fakedevice.Device) http.Handler {
	t.Helper()
	store := pairedStore(t, fake)
	noLAN := func([]int) ([]string, error) { return nil, nil }
	conn := connector.New(connector.Config{Store: store, Targets: noLAN})
	t.Cleanup(conn.Close)
	_, end := conn.Session()
	t.Cleanup(end)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := conn.Device(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never linked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return New(self, "test", pairing.NewFlow(pairing.Config{Store: store, Targets: noLAN}), conn)
}

func pairedStore(t *testing.T, fake *fakedevice.Device) *profile.Store {
	t.Helper()
	store, err := profile.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pub, err := store.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	fake.WriteFile(t, fakedevice.AuthorizedKeys, pub+"\n")
	host, portStr, _ := net.SplitHostPort(fake.Addr())
	port, _ := strconv.Atoi(portStr)
	p := profile.New(host, port)
	p.HostKey = profile.AuthorizedKey(fake.HostKey())
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	return store
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct{ Error string }
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return e.Error
}

func TestLibraryNeedsALink(t *testing.T) {
	fake := fakedevice.New(t)
	store := pairedStore(t, fake)
	conn := connector.New(connector.Config{Store: store, Targets: func([]int) ([]string, error) { return nil, nil }})
	t.Cleanup(conn.Close)
	h := New(self, "test", pairing.NewFlow(pairing.Config{Store: store}), conn)
	for _, p := range []string{"/api/library/folder?path=", "/api/library/stats"} {
		rec := call(h, "GET", p, self, "", "")
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec.Body.Bytes()) != "not_linked" {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}

	rec := do(t, "GET", "/api/library/folder", self, "")
	if rec.Code != http.StatusConflict || errorCode(t, rec.Body.Bytes()) != "not_paired" {
		t.Errorf("unpaired: %d %s", rec.Code, rec.Body)
	}
}

func TestLibraryFolderAndStats(t *testing.T) {
	fake := fakedevice.New(t)
	fake.WriteFile(t, fakedevice.Documents+"/Sci-Fi/Dune.epub", "dune")
	fake.WriteFile(t, fakedevice.Documents+"/Sci-Fi/Dune.sdr/metadata.epub.lua", "return {}")
	fake.WriteFile(t, fakedevice.Settings, `return { ["lastfile"] = "/mnt/us/documents/Sci-Fi/Dune.epub" }`)
	h := linkedServer(t, fake)

	rec := call(h, "GET", "/api/library/folder?path=Sci-Fi", self, "", "", "Sec-Fetch-Site", "same-origin")
	if rec.Code != http.StatusOK {
		t.Fatalf("folder: %d %s", rec.Code, rec.Body)
	}
	var f library.Folder
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if f.Path != "Sci-Fi" || len(f.Entries) != 1 || f.Books != 1 {
		t.Fatalf("folder = %+v", f)
	}
	if e := f.Entries[0]; e.Name != "Dune.epub" || !e.OpenNow || !e.HasSidecar || e.Format != "EPUB" {
		t.Fatalf("entry = %+v", e)
	}

	rec = call(h, "GET", "/api/library/stats", self, "", "")
	var st library.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body)
	}
	if st.Root != fakedevice.Documents || st.FreeBytes == 0 || st.OpenNow != "Sci-Fi/Dune.epub" || st.MetadataMode != "doc" {
		t.Fatalf("stats = %+v", st)
	}
}

func TestLibraryRejectsPathsOutsideTheRoot(t *testing.T) {
	fake := fakedevice.New(t)
	fake.WriteFile(t, fakedevice.Documents+"/Dune.epub", "dune")
	h := linkedServer(t, fake)
	cases := map[string]struct {
		status int
		code   string
	}{
		"..":                    {http.StatusBadRequest, "outside_library"},
		"../koreader":           {http.StatusBadRequest, "outside_library"},
		"%2Fmnt%2Fus%2F..%2F..": {http.StatusBadRequest, "outside_library"},
		"..%5Ckoreader":         {http.StatusBadRequest, "outside_library"},
		"Nope":                  {http.StatusNotFound, "not_found"},
		"Dune.epub":             {http.StatusConflict, "not_folder"},
	}
	for q, c := range cases {
		rec := call(h, "GET", "/api/library/folder?path="+q, self, "", "")
		if rec.Code != c.status || errorCode(t, rec.Body.Bytes()) != c.code {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
}

type opAnswer struct {
	Folder   *library.Folder
	Path     string
	Warnings []library.Warning
	Error    string
	Reason   string
}

func answer(t *testing.T, rec *httptest.ResponseRecorder) opAnswer {
	t.Helper()
	var a opAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	return a
}

func fileNames(f *library.Folder) string {
	if f == nil {
		return "<nil>"
	}
	var out []string
	for _, e := range f.Entries {
		out = append(out, e.Name)
	}
	return strings.Join(out, ",")
}

func onDisk(t *testing.T, fake *fakedevice.Device, p string) bool {
	t.Helper()
	_, err := os.Lstat(fake.Path(p))
	return err == nil
}

func opLibrary(t *testing.T) (*fakedevice.Device, http.Handler) {
	t.Helper()
	fake := fakedevice.New(t)
	doc := fakedevice.Documents
	fake.WriteFile(t, doc+"/Dune.epub", "dune")
	fake.WriteFile(t, doc+"/Dune.sdr/metadata.epub.lua", "return {}")
	fake.WriteFile(t, doc+"/Sci-Fi/Foundation.pdf", "f")
	fake.WriteFile(t, doc+"/Sci-Fi/Foundation.sdr/metadata.pdf.lua", "return {}")
	fake.WriteFile(t, fakedevice.Settings, `return { ["lastfile"] = "/mnt/us/documents/Sci-Fi/Foundation.pdf" }`)
	return fake, linkedServer(t, fake)
}

func TestLibraryOperations(t *testing.T) {
	fake, h := opLibrary(t)
	doc := fakedevice.Documents

	rec := post(t, h, "/api/library/mkdir", `{"parent":"","name":"Herbert","view":""}`)
	if a := answer(t, rec); rec.Code != http.StatusOK || a.Path != "Herbert" || fileNames(a.Folder) != "Herbert,Sci-Fi,Dune.epub" {
		t.Fatalf("mkdir: %d %s", rec.Code, rec.Body)
	}
	rec = post(t, h, "/api/library/rename", `{"path":"Dune.epub","name":"Dune I.epub","view":""}`)
	if a := answer(t, rec); rec.Code != http.StatusOK || fileNames(a.Folder) != "Herbert,Sci-Fi,Dune I.epub" || a.Warnings == nil {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	rec = post(t, h, "/api/library/move", `{"paths":["Dune I.epub"],"to":"Herbert","view":"Herbert"}`)
	if a := answer(t, rec); rec.Code != http.StatusOK || fileNames(a.Folder) != "Dune I.epub" || !a.Folder.Entries[0].HasSidecar {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	rec = call(h, "GET", "/api/library/preview?path=Herbert&path=Sci-Fi", self, "", "")
	var p library.Preview
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p != (library.Preview{Books: 2, Folders: 2, OpenNow: true}) {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	rec = post(t, h, "/api/library/delete", `{"paths":["Herbert"],"view":""}`)
	if a := answer(t, rec); rec.Code != http.StatusOK || fileNames(a.Folder) != "Sci-Fi" {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if onDisk(t, fake, doc+"/Herbert") {
		t.Fatal("not deleted")
	}
	// The folder in view was deleted: the change stands, with no listing.
	rec = post(t, h, "/api/library/mkdir", `{"parent":"","name":"Gone","view":""}`)
	rec = post(t, h, "/api/library/delete", `{"paths":["Gone"],"view":"Gone"}`)
	if a := answer(t, rec); rec.Code != http.StatusOK || a.Folder != nil {
		t.Fatalf("delete in view: %d %s", rec.Code, rec.Body)
	}
}

func TestLibraryOperationErrors(t *testing.T) {
	fake, h := opLibrary(t)
	cases := []struct {
		path, body string
		status     int
		code       string
	}{
		{"/api/library/mkdir", `{"parent":"","name":"a:b"}`, http.StatusBadRequest, "invalid_name"},
		{"/api/library/mkdir", `{"parent":"","name":"sci-fi"}`, http.StatusConflict, "collision"},
		{"/api/library/mkdir", `{"parent":"../koreader","name":"x"}`, http.StatusBadRequest, "outside_library"},
		{"/api/library/rename", `{"path":"Dune.epub","name":"Dune.pdf"}`, http.StatusBadRequest, "extension_change"},
		{"/api/library/rename", `{"path":"","name":"x"}`, http.StatusBadRequest, "library_root"},
		{"/api/library/rename", `{"path":"Sci-Fi/Foundation.pdf","name":"F.pdf"}`, http.StatusConflict, "open_now_needs_confirm"},
		{"/api/library/rename", `{"path":"Nope.epub","name":"x.epub"}`, http.StatusNotFound, "not_found"},
		{"/api/library/move", `{"paths":["Sci-Fi"],"to":"Sci-Fi"}`, http.StatusConflict, "into_itself"},
		{"/api/library/move", `{"paths":["Sci-Fi"],"to":""}`, http.StatusOK, ""},
		{"/api/library/move", `{"paths":["Dune.epub"],"to":"Dune.epub"}`, http.StatusConflict, "not_folder"},
		{"/api/library/move", `{"paths":[],"to":"Sci-Fi"}`, http.StatusBadRequest, "bad_request"},
		{"/api/library/move", `{"paths":["../koreader/settings.reader.lua"],"to":""}`, http.StatusBadRequest, "outside_library"},
		{"/api/library/delete", `{"paths":["Sci-Fi"]}`, http.StatusConflict, "open_now_needs_confirm"},
		{"/api/library/delete", `{"paths":["..\\koreader"]}`, http.StatusBadRequest, "outside_library"},
		{"/api/library/delete", `{"paths":["Dune.epub"],"force":true}`, http.StatusBadRequest, "bad_request"},
	}
	for _, c := range cases {
		rec := post(t, h, c.path, c.body)
		if a := answer(t, rec); rec.Code != c.status || a.Error != c.code {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
	if a := answer(t, post(t, h, "/api/library/mkdir", `{"parent":"","name":"x."}`)); a.Reason != library.ReasonTrailing {
		t.Errorf("reason = %q", a.Reason)
	}

	fake.WriteFile(t, fakedevice.Settings, `return { ["document_metadata_folder"] = "dir" }`)
	if rec := post(t, h, "/api/library/rename", `{"path":"Dune.epub","name":"D.epub"}`); answer(t, rec).Error != "metadata_mode_unsupported" {
		t.Errorf("dir mode: %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{"/mnt/us/koreader/settings.reader.lua", fakedevice.Documents + "/Dune.epub", fakedevice.Documents + "/Sci-Fi/Foundation.pdf"} {
		if !onDisk(t, fake, p) {
			t.Errorf("%s is gone", p)
		}
	}
}

func TestLibraryConfirmOpenBook(t *testing.T) {
	fake, h := opLibrary(t)
	rec := post(t, h, "/api/library/delete", `{"paths":["Sci-Fi/Foundation.pdf"],"view":"Sci-Fi"}`)
	if answer(t, rec).Error != "open_now_needs_confirm" || !onDisk(t, fake, fakedevice.Documents+"/Sci-Fi/Foundation.pdf") {
		t.Fatalf("unconfirmed: %d %s", rec.Code, rec.Body)
	}
	rec = post(t, h, "/api/library/delete", `{"paths":["Sci-Fi/Foundation.pdf"],"confirmOpen":true,"view":"Sci-Fi"}`)
	if a := answer(t, rec); rec.Code != http.StatusOK || fileNames(a.Folder) != "" {
		t.Fatalf("confirmed: %d %s", rec.Code, rec.Body)
	}
	if onDisk(t, fake, fakedevice.Documents+"/Sci-Fi/Foundation.sdr") {
		t.Fatal("sidecar left")
	}
}

// Changes are POSTs only, behind the same-origin guard: a GET or a
// cross-origin POST changes nothing.
func TestLibraryChangesNeedSameOriginPost(t *testing.T) {
	fake, h := opLibrary(t)
	dune := fakedevice.Documents + "/Dune.epub"
	for _, p := range []string{"/api/library/delete?paths=Dune.epub", "/api/library/mkdir", "/api/library/rename", "/api/library/move"} {
		if rec := call(h, "GET", p, self, "", ""); rec.Code < 400 {
			t.Errorf("GET %s: %d", p, rec.Code)
		}
	}
	body := `{"paths":["Dune.epub"]}`
	if rec := call(h, "POST", "/api/library/delete", self, "http://evil.example", body, "Content-Type", "application/json"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin: %d", rec.Code)
	}
	if rec := call(h, "POST", "/api/library/delete", self, "", body, "Content-Type", "application/json"); rec.Code != http.StatusForbidden {
		t.Errorf("no origin: %d", rec.Code)
	}
	if rec := call(h, "POST", "/api/library/delete", self, origin, body, "Content-Type", "text/plain"); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form post: %d", rec.Code)
	}
	if !onDisk(t, fake, dune) {
		t.Fatal("deleted")
	}
}

// Two writes racing for one name: exactly one wins and no Book is lost.
func TestLibraryWritesDoNotRace(t *testing.T) {
	fake, h := opLibrary(t)
	doc := fakedevice.Documents
	for i := range 20 {
		a, b := fmt.Sprintf("a%d.epub", i), fmt.Sprintf("b%d.epub", i)
		fake.WriteFile(t, doc+"/"+a, "a")
		fake.WriteFile(t, doc+"/"+b, "b")
		target := fmt.Sprintf("same%d.epub", i)
		codes := make(chan int, 2)
		var start sync.WaitGroup
		start.Add(1)
		for _, from := range []string{a, b} {
			go func() {
				start.Wait()
				codes <- post(t, h, "/api/library/rename", `{"path":"`+from+`","name":"`+target+`"}`).Code
			}()
		}
		start.Done()
		if got := <-codes + <-codes; got != http.StatusOK+http.StatusConflict {
			t.Fatalf("round %d: codes sum %d", i, got)
		}
		left := 0
		for _, n := range []string{a, b, target} {
			if onDisk(t, fake, doc+"/"+n) {
				left++
			}
		}
		if left != 2 {
			t.Fatalf("round %d: %d of 2 books left", i, left)
		}
	}
}
