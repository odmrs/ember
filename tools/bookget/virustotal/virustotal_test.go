package virustotal

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bookget/safehttp"
)

func TestHashOnly(t *testing.T) {
	for _, tt := range []struct {
		name    string
		code    int
		body    string
		wantErr bool
		message string
	}{
		{"unknown", 404, "", false, "unknown to VT"}, {"clean", 200, `{"data":{"attributes":{"last_analysis_stats":{"malicious":0}}}}`, false, "0 malicious"}, {"malicious", 200, `{"data":{"attributes":{"last_analysis_stats":{"malicious":1}}}}`, true, ""}, {"rate limit", 429, "", true, ""}, {"malformed", 200, `{}`, true, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte("test file")
			digest := fmt.Sprintf("%x", sha256.Sum256(data))
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v3/files/"+digest || r.ContentLength > 0 || r.Header.Get("x-apikey") != "secret" {
					t.Errorf("not a hash-only request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tt.code)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			c := safehttp.New(u.Host)
			c.HTTP = srv.Client()
			vt := &Client{c, srv.URL + "/api/v3/files/", "secret"}
			p := filepath.Join(t.TempDir(), "book")
			if err := os.WriteFile(p, data, 0600); err != nil {
				t.Fatal(err)
			}
			message, err := vt.Check(context.Background(), p)
			if (err != nil) != tt.wantErr || !strings.Contains(message, tt.message) {
				t.Fatalf("%q %v", message, err)
			}
		})
	}
}
