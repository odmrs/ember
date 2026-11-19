package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewPublic pins downloads to the exact source-provided hostname and validates
// every resolved address at connection time, including after DNS changes.
func NewPublic(u *url.URL) (*Client, error) {
	host := u.Hostname()
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil, fmt.Errorf("untrusted download origin")
	}
	c := New(host)
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	c.HTTP.Transport = &http.Transport{TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		target, port, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(target, host) || port != "443" {
			return nil, fmt.Errorf("download connection left the trusted origin")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, target)
		if err != nil {
			return nil, err
		}
		for _, entry := range ips {
			ip := entry.IP
			if !publicAddress(ip) {
				return nil, fmt.Errorf("download origin resolved to a non-public address")
			}
		}
		var last error
		for _, entry := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(entry.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		if last == nil {
			last = fmt.Errorf("download host has no addresses")
		}
		return nil, last
	}}
	return c, nil
}

func publicAddress(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}
