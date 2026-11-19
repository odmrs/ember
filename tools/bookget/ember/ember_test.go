package ember

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSend(t *testing.T) {
	for _, tt := range []struct {
		name, state        string
		upload             int
		wantErr, notLinked bool
	}{
		{"linked", "paired", 200, false, false}, {"unpaired", "unpaired", 200, true, true}, {"disconnected", "paired", 503, true, true}, {"conflict", "paired", 409, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			uploaded := false
			var base string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/status" {
					fmt.Fprintf(w, `{"state":%q}`, tt.state)
					return
				}
				uploaded = true
				if r.URL.Path != "/api/library/upload" || r.Method != "POST" || r.URL.Query().Get("folder") != "" || r.URL.Query().Get("name") != "Title - Author.epub" || r.Header.Get("Origin") != base || r.Header.Get("Content-Type") != "application/octet-stream" || r.ContentLength != 4 {
					t.Errorf("wrong request %+v", r)
				}
				data, _ := io.ReadAll(r.Body)
				if string(data) != "book" {
					t.Error("wrong bytes")
				}
				w.WriteHeader(tt.upload)
			}))
			defer srv.Close()
			base = srv.URL
			p := filepath.Join(t.TempDir(), "Title - Author.epub")
			if err := os.WriteFile(p, []byte("book"), 0600); err != nil {
				t.Fatal(err)
			}
			c := &Client{srv.URL, srv.Client()}
			err := c.Send(context.Background(), p)
			if (err != nil) != tt.wantErr || errors.Is(err, ErrNotLinked) != tt.notLinked {
				t.Fatal(err)
			}
			if tt.state == "unpaired" && uploaded {
				t.Fatal("uploaded while unpaired")
			}
		})
	}
}
