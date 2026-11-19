package annasarchive

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"bookget/safehttp"
)

func authenticate(ctx context.Context, c *safehttp.Client, base, key string) (*safehttp.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	transport := *c.HTTP
	transport.Jar = jar
	client := *c
	client.HTTP = &transport
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/account/", strings.NewReader(url.Values{"key": {key}}.Encode()))
	if err != nil {
		return nil, fmt.Errorf("could not construct account login")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("account login: %w", err)
	}
	_ = resp.Body.Close()
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	for _, cookie := range jar.Cookies(u) {
		if strings.HasPrefix(cookie.Name, "aa_") && cookie.Value != "" {
			return &client, nil
		}
	}
	return nil, fmt.Errorf("account login did not create a session (key rejected or site challenge)")
}
