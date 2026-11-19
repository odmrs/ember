package safehttp

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPolicy(t *testing.T) {
	for _, address := range []string{"http://good.test/book", "https://evil.test/book", "https://good.test.evil.test/book", "https://u:p@good.test/book", "https://good.test:444/book"} {
		c := New("good.test")
		if _, err := c.Get(context.Background(), address); err == nil {
			t.Fatal("allowed", address)
		}
	}
}
func TestRedirect(t *testing.T) {
	for _, target := range []string{"https://evil.test/book", "http://127.0.0.1/book"} {
		t.Run(target, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("User-Agent") != "bookget/0.1" {
					t.Error("UA")
				}
				http.Redirect(w, r, target, 302)
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			c := New(u.Host)
			c.HTTP = srv.Client()
			if _, err := c.Get(context.Background(), srv.URL); err == nil {
				t.Fatal("redirect allowed")
			}
		})
	}
}
func TestCopyLimit(t *testing.T) {
	var b bytes.Buffer
	if err := CopyLimited(&b, strings.NewReader("12345"), 4); err == nil {
		t.Fatal("accepted oversized")
	}
	b.Reset()
	if err := CopyLimited(&b, strings.NewReader("1234"), 4); err != nil {
		t.Fatal(err)
	}
}

func TestSameSourceRedirect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", 302)
			return
		}
		_, _ = w.Write([]byte("book"))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := New(u.Host)
	c.HTTP = srv.Client()
	resp, err := c.Get(context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestDownloadOrigins(t *testing.T) {
	for _, address := range []string{"http://example.com/file", "https://127.0.0.1/file", "https://[::1]/file", "https://host.local/file", "https://host.internal/file", "https://localhost/file", "https://user:pass@example.com/file", "https://example.com:444/file"} {
		u, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = NewPublic(u); err == nil {
			t.Fatal("untrusted download origin accepted", address)
		}
	}
}

func TestPrivateAndReservedAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.1.1", "::1", "fc00::1", "2001:db8::1", "198.18.0.1"} {
		if publicAddress(net.ParseIP(address)) {
			t.Fatal("nonpublic IP accepted", address)
		}
	}
	if !publicAddress(net.ParseIP("1.1.1.1")) {
		t.Fatal("public IP rejected")
	}
}
