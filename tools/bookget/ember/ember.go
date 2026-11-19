package ember

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

var ErrNotLinked = errors.New("ember is unavailable or Kindle is not linked; file saved locally")

type Client struct {
	Base string
	HTTP *http.Client
}

func New() *Client {
	base := os.Getenv("BOOKGET_EMBER_URL")
	if base == "" {
		base = "http://127.0.0.1:8737"
	}
	return &Client{base, &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("ember redirects refused") }}}
}
func (c *Client) Send(ctx context.Context, filename string) error {
	u, e := url.Parse(c.Base)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("ember must use an HTTP IPv4 loopback origin")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/api/status", nil)
	if err != nil {
		return err
	}
	client := *c.HTTP
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("ember redirects refused") }
	resp, err := client.Do(req)
	if err != nil {
		return ErrNotLinked
	}
	var status struct {
		State string `json:"state"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || status.State != "paired" {
		return ErrNotLinked
	}
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/library/upload?"+url.Values{"folder": {""}, "name": {filepath.Base(filename)}}.Encode(), f)
	if err != nil {
		return err
	}
	req.ContentLength = stat.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Origin", c.Base)
	req.Header.Set("User-Agent", "bookget/0.1")
	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 503 {
		return ErrNotLinked
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ember upload HTTP %d; file saved locally", resp.StatusCode)
	}
	return nil
}
