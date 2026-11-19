// Package safehttp restricts remote requests to explicit HTTPS origins.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const MaxDownload int64 = 200 * 1024 * 1024

type Client struct {
	HTTP               *http.Client
	Hosts              []string
	Username, Password string
}

func New(hosts ...string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 2 * time.Minute}, Hosts: hosts}
}
func (c *Client) validate(u *url.URL) error {
	if u.Scheme != "https" || u.User != nil {
		return fmt.Errorf("HTTPS without URL credentials required")
	}
	for _, h := range c.Hosts {
		if strings.EqualFold(u.Host, h) || ((u.Port() == "" || u.Port() == "443") && strings.EqualFold(u.Hostname(), h)) {
			return nil
		}
	}
	return fmt.Errorf("untrusted source domain: %s", u.Hostname())
}
func (c *Client) Get(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Do validates every destination before sending a request, including redirects.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if err := c.validate(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "bookget/0.1")
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	client := *c.HTTP
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if err := c.validate(r.URL); err != nil {
			return err
		}
		if r.URL.Host != via[0].URL.Host {
			r.Header.Del("Authorization")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		var networkErr *url.Error
		if errors.As(err, &networkErr) {
			return nil, fmt.Errorf("request to %s failed: %w", req.URL.Host, networkErr.Err)
		}
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, req.URL.Host)
	}
	return resp, nil
}
func CopyLimited(w io.Writer, r io.Reader, limit int64) error {
	n, err := io.Copy(w, io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("response exceeds %d bytes", limit)
	}
	return nil
}

// ValidateURL applies the same source-origin policy without making a request.
func (c *Client) ValidateURL(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return err
	}
	return c.validate(u)
}
