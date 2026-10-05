package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/odmrs/ember/internal/fakedevice"
	"github.com/odmrs/ember/internal/library"
)

const annaName = "Animal Farm -- Orwell, George -- 2021 -- AmazonClassics -- 9eb17e015e45ed6e371a245ee96e84ab -- Anna's Archive.epub"

func TestCleanNamesOverTheAPI(t *testing.T) {
	fake := fakedevice.New(t)
	doc := fakedevice.Documents
	fake.WriteFile(t, doc+"/Downloads/"+annaName, "a")
	fake.WriteFile(t, doc+"/Downloads/Animal Farm -- Orwell, George -- 2021 -- AmazonClassics -- 9eb17e015e45ed6e371a245ee96e84ab -- Anna's Archive.sdr/metadata.epub.lua", "return {}")
	fake.WriteFile(t, doc+"/Downloads/mi_historia_(Spanish_Edition).azw3", "m")
	fake.WriteFile(t, fakedevice.Settings, `return { ["lastfile"] = "/mnt/us/documents/Downloads/mi_historia_(Spanish_Edition).azw3" }`)
	h := linkedServer(t, fake)

	rec := get(t, h, "/api/library/clean?path=Downloads")
	var preview struct{ Proposals []library.Proposal }
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || rec.Code != http.StatusOK || len(preview.Proposals) != 2 {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	for _, p := range preview.Proposals {
		if !p.Changed {
			t.Errorf("unchanged: %+v", p)
		}
	}
	rec = get(t, h, "/api/library/clean?path=Downloads/"+url.PathEscape(annaName))
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || len(preview.Proposals) != 1 || preview.Proposals[0].Proposed != "Animal Farm - George Orwell.epub" {
		t.Fatalf("one Book: %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/api/library/clean?path=../x"); rec.Code != http.StatusBadRequest || errorCode(t, rec.Body.Bytes()) != "outside_library" {
		t.Errorf("outside: %d %s", rec.Code, rec.Body)
	}

	body, _ := json.Marshal(map[string]any{"view": "Downloads", "changes": []library.NameChange{
		{Path: "Downloads/" + annaName, Name: "Animal Farm - George Orwell.epub"},
		{Path: "Downloads/mi_historia_(Spanish_Edition).azw3", Name: "Mi Historia.azw3"},
	}})
	rec = post(t, h, "/api/library/rename-all", string(body))
	if a := answer(t, rec); rec.Code != http.StatusConflict || a.Error != "open_now_needs_confirm" || !onDisk(t, fake, doc+"/Downloads/"+annaName) {
		t.Fatalf("open Book, unconfirmed: %d %s", rec.Code, rec.Body)
	}
	var withConfirm map[string]any
	_ = json.Unmarshal(body, &withConfirm)
	withConfirm["confirmOpen"] = true
	body, _ = json.Marshal(withConfirm)
	rec = post(t, h, "/api/library/rename-all", string(body))
	if a := answer(t, rec); rec.Code != http.StatusOK || fileNames(a.Folder) != "Animal Farm - George Orwell.epub,Mi Historia.azw3" {
		t.Fatalf("rename-all: %d %s", rec.Code, rec.Body)
	}
	if !onDisk(t, fake, doc+"/Downloads/Animal Farm - George Orwell.sdr/metadata.epub.lua") {
		t.Error("the Sidecar did not travel")
	}
	if rec := post(t, h, "/api/library/rename-all", `{"changes":[]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("no changes: %d %s", rec.Code, rec.Body)
	}
}

func TestUploadsTakeTheCleanNameUnlessTurnedOff(t *testing.T) {
	fake := fakedevice.New(t)
	r := newSettingsRig(t, fake, true)
	check := func() library.Arrival {
		t.Helper()
		rec := call(r.h, "GET", "/api/library/upload/check?"+url.Values{"folder": {""}, "name": {annaName}, "size": {"1"}}.Encode(), self, "", "")
		var a library.Arrival
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("check: %d %s", rec.Code, rec.Body)
		}
		return a
	}
	if a := check(); a.Name != "Animal Farm - George Orwell.epub" {
		t.Errorf("default: upload lands as %q", a.Name)
	}
	if rec := get(t, r.h, "/api/status"); !json.Valid(rec.Body.Bytes()) || !containsJSON(t, rec.Body.Bytes(), "cleanUploads", true) {
		t.Errorf("status: %s", rec.Body)
	}

	code, out := r.post("/api/settings", `{"cleanUploads":false}`)
	if code != http.StatusOK || out["device"].(map[string]any)["cleanUploads"] != false || !r.profile().RawUploadNames {
		t.Fatalf("turn off: %d %v", code, out)
	}
	if a := check(); a.Name != annaName {
		t.Errorf("off: upload lands as %q", a.Name)
	}
}

func containsJSON(t *testing.T, raw []byte, key string, want any) bool {
	t.Helper()
	var s struct{ Device map[string]any }
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s.Device[key] == want
}
