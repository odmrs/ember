package virustotal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"bookget/safehttp"
)

type Client struct {
	HTTP      *safehttp.Client
	Base, Key string
}

func New(key string) *Client {
	return &Client{safehttp.New("www.virustotal.com"), "https://www.virustotal.com/api/v3/files/", key}
}
func (c *Client) Check(ctx context.Context, filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	_ = f.Close()
	if err != nil {
		return "", err
	}
	// Separate client so API keys cannot follow redirects to other origins.
	requestURL := c.Base + hex.EncodeToString(h.Sum(nil))
	client := *c.HTTP.HTTP
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("VirusTotal redirects refused") }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", err
	}
	if err = c.HTTP.ValidateURL(requestURL); err != nil {
		return "", err
	}
	req.Header.Set("x-apikey", c.Key)
	req.Header.Set("User-Agent", "bookget/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 404 {
		return "unknown to VT", nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("VirusTotal HTTP %d; verification incomplete", resp.StatusCode)
	}
	var data struct {
		Data struct {
			Attributes struct {
				Stats map[string]int `json:"last_analysis_stats"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return "", err
	}
	stats := data.Data.Attributes.Stats
	malicious, ok := stats["malicious"]
	if stats == nil || !ok || malicious < 0 {
		return "", fmt.Errorf("VirusTotal response missing analysis stats")
	}
	if stats["malicious"] >= 1 {
		return "", fmt.Errorf("VirusTotal: %d malicious detections", stats["malicious"])
	}
	return "VT: 0 malicious detections (not a safety guarantee)", nil
}
