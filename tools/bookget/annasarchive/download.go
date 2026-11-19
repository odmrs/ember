package annasarchive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"bookget/safehttp"
	"bookget/source"
)

// browserFetch follows the site's ordinary free slow-download flow, including
// its normal waiting period. It never selects paid fast-download links.
func browserFetch(ctx context.Context, base, md5, key string, progress func(string)) (source.Download, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	d, session, err := openBrowserSession(ctx, base, key, progress)
	if err != nil {
		return source.Download{}, err
	}
	defer d.close()
	if err = d.navigate(ctx, session, base+"/md5/"+md5); err != nil {
		return source.Download{}, err
	}
	title, _ := d.evaluate(ctx, session, `document.querySelector('meta[property="og:title"]')?.content || document.querySelector('h1')?.innerText || "Book"`)
	var slow string
	shown := false
	for {
		slow, err = d.evaluate(ctx, session, `(()=>{const a=[...document.querySelectorAll('a.js-download-link[href]')].find(a=>{const u=new URL(a.href,location.href);return u.origin===location.origin&&u.pathname.startsWith('/slow_download/')});return a?.href||""})()`)
		if err != nil {
			return source.Download{}, err
		}
		if slow != "" {
			break
		}
		if !shown {
			_, _ = d.evaluate(ctx, session, `(()=>{document.querySelector('a.js-show-external-button')?.click();return "shown"})()`)
			shown = true
		}
		if err = pause(ctx); err != nil {
			return source.Download{}, fmt.Errorf("no free slow-download option became available; check the site in the browser")
		}
	}
	slowURL, err := url.Parse(slow)
	baseURL, _ := url.Parse(base)
	if err != nil || slowURL.Scheme != "https" || slowURL.Host != baseURL.Host || !strings.HasPrefix(slowURL.Path, "/slow_download/") {
		return source.Download{}, fmt.Errorf("untrusted slow-download link")
	}
	if err = d.navigate(ctx, session, slow); err != nil {
		return source.Download{}, err
	}
	address, err := waitForDownload(ctx, func(ctx context.Context) (string, error) {
		return d.evaluate(ctx, session, downloadStateScript)
	}, progress, func() {
		_, _ = d.call(ctx, "Page.bringToFront", map[string]any{}, session)
		_, _ = d.evaluate(ctx, session, `document.title="bookget — Confirme a verificação no site"; "ready"`)
	})
	if err != nil {
		return source.Download{}, err
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return source.Download{}, fmt.Errorf("download link must be HTTPS without URL credentials")
	}
	client, err := safehttp.NewPublic(u)
	if err != nil {
		return source.Download{}, err
	}
	// Transfer only cookies applicable to this one download origin. They come
	// from our dedicated profile, never the user's personal browser profile.
	raw, err := d.call(ctx, "Network.getCookies", map[string]any{"urls": []string{address}}, session)
	if err != nil {
		return source.Download{}, err
	}
	var cookies struct {
		Cookies []struct {
			Name, Value, Domain, Path string
			Secure, HTTPOnly          bool
		} `json:"cookies"`
	}
	if err = json.Unmarshal(raw, &cookies); err != nil {
		return source.Download{}, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return source.Download{}, err
	}
	var scoped []*http.Cookie
	for _, c := range cookies.Cookies {
		scoped = append(scoped, &http.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Secure: c.Secure, HttpOnly: c.HTTPOnly})
	}
	jar.SetCookies(u, scoped)
	client.HTTP.Jar = jar
	return source.Download{Book: source.Book{ID: "anna:" + md5, Title: strings.TrimSpace(title), Source: "Anna's Archive"}, Format: "epub", URL: address, Client: client}, nil
}
