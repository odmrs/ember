package server

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
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
