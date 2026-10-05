// Package pairing finds a Kindle running KOReader's SSH server on the local
// network, surveys it and installs ember's key on it.
package pairing

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/profile"
	"golang.org/x/crypto/ssh"
)

const (
	scanWorkers   = 128
	connectWait   = 800 * time.Millisecond
	bannerWait    = 2 * time.Second
	handshakeWait = 8 * time.Second
)

// Candidate is a host that answers like KOReader's SSH server.
type Candidate struct {
	Addr        string `json:"address"` // "host:port"
	Banner      string `json:"banner"`
	HostKey     string `json:"hostKey"` // authorized_keys format
	Fingerprint string `json:"fingerprint"`
	// Passwordless reports whether "Login without password" let ember in to
	// read Survey. When it did not, Survey is nil.
	Passwordless bool            `json:"passwordless"`
	Survey       *profile.Survey `json:"survey,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// LocalTargets lists "host:port" for every address of the /24 around each
// IPv4 address of the machine's active, non-loopback, non-tunnel interfaces.
func LocalTargets(ports []int) ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var addrs []net.Addr
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		a, err := ifc.Addrs()
		if err != nil {
			continue
		}
		addrs = append(addrs, a...)
	}
	return subnetTargets(addrs, ports), nil
}

// subnetTargets expands each IPv4 address to its /24, whatever the
// interface's own mask, and pairs every host with ports. Loopback and
// link-local addresses are skipped.
func subnetTargets(addrs []net.Addr, ports []int) []string {
	seen := map[netip.Prefix]bool{}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipnet.IP.To4())
		if !ok || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			continue
		}
		prefix := netip.PrefixFrom(ip, 24).Masked()
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		base := prefix.Addr().As4()
		for host := 1; host < 255; host++ {
			base[3] = byte(host)
			for _, port := range ports {
				out = append(out, net.JoinHostPort(netip.AddrFrom4(base).String(), strconv.Itoa(port)))
			}
		}
	}
	return out
}

// Find checks every target for a dropbear SSH banner, then reads the host
// key of each match and, if passwordless login is open, surveys it. Nothing
// is written to any host.
func Find(ctx context.Context, targets []string) []Candidate {
	banners := make([]string, len(targets))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(scanWorkers, len(targets)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if b, err := readBanner(ctx, targets[i]); err == nil && isDropbear(b) {
					banners[i] = b
				}
			}
		}()
	}
	for i := range targets {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	var found []Candidate
	for i, b := range banners {
		if b != "" && ctx.Err() == nil {
			found = append(found, inspect(ctx, targets[i], b))
		}
	}
	return found
}

func isDropbear(banner string) bool {
	return strings.HasPrefix(banner, "SSH-2.0-dropbear")
}

// readBanner returns the SSH identification line a server sends first,
// skipping any lines before it as RFC 4253 allows.
func readBanner(ctx context.Context, addr string) (string, error) {
	dctx, cancel := context.WithTimeout(ctx, connectWait)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(bannerWait))
	r := bufio.NewReaderSize(conn, 256)
	for range 5 {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return "", err
		}
		if s := strings.TrimRight(string(line), "\r\n"); strings.HasPrefix(s, "SSH-") {
			return s, nil
		}
	}
	return "", net.UnknownNetworkError("no SSH banner")
}

func inspect(ctx context.Context, addr, banner string) Candidate {
	c := Candidate{Addr: addr, Banner: banner}
	key, err := device.HostKey(ctx, addr, handshakeWait)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	c.HostKey = profile.AuthorizedKey(key)
	c.Fingerprint = ssh.FingerprintSHA256(key)

	d, err := device.Dial(ctx, device.Config{Addr: addr, User: profile.DefaultUser, HostKey: key, Timeout: handshakeWait})
	if err != nil {
		if !errors.Is(err, device.ErrAuthRejected) {
			c.Error = err.Error()
		}
		return c
	}
	defer d.Close()
	c.Passwordless = true
	s, err := Survey(d, profile.DefaultLibraryRoot)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	c.Survey = &s
	return c
}
