// Command ember serves a local dashboard for managing books on a KOReader
// Kindle over SSH.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/odmrs/ember/internal/browser"
	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/pairing"
	"github.com/odmrs/ember/internal/profile"
	"github.com/odmrs/ember/internal/server"
)

var version = "0.1.0-dev"

func main() {
	port := flag.Int("port", 8737, "local port for the dashboard (0 picks a free one)")
	noOpen := flag.Bool("no-open", false, "do not open the browser on start")
	configDir := flag.String("config", "", "profile directory (default: ember in the user config dir)")
	quitOnClose := flag.Bool("quit-on-close", false, "exit a few seconds after the last dashboard tab closes")
	flag.Parse()

	if err := run(*port, !*noOpen, *configDir, *quitOnClose); err != nil {
		fmt.Fprintln(os.Stderr, "ember:", err)
		os.Exit(1)
	}
}

func run(port int, open bool, configDir string, quitOnClose bool) error {
	if configDir == "" {
		dir, err := profile.DefaultDir()
		if err != nil {
			return fmt.Errorf("cannot find the user config dir: %w", err)
		}
		configDir = dir
	}
	store, err := profile.Open(configDir)
	if err != nil {
		return err
	}
	flow := pairing.NewFlow(pairing.Config{Store: store})
	conn := connector.New(connector.Config{Store: store})
	defer conn.Close()

	hostport := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ln, err := net.Listen("tcp", hostport)
	if err != nil {
		// Launching ember twice shows the dashboard that is already up.
		if open && emberAt(hostport) {
			return browser.Open("http://" + hostport + "/")
		}
		return fmt.Errorf("cannot listen on %s (in use by another program; try -port 0): %w", hostport, err)
	}
	hostport = ln.Addr().String()
	srv := &http.Server{
		Handler:           server.New(hostport, version, flow, conn),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Open event streams never go idle; ending the Sessions lets Shutdown
	// finish.
	srv.RegisterOnShutdown(conn.Close)

	url := "http://" + hostport + "/"
	log.Printf("ember %s running at %s (Ctrl+C to stop)", version, url)
	if open {
		if err := browser.Open(url); err != nil {
			log.Printf("could not open the browser, open %s yourself", url)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if quitOnClose {
		conn.OnSessions(watchTabs(quitIfNoTabOpens, quitAfterLastTab, stop).sessions)
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// emberAt reports whether an ember dashboard already answers on hostport.
func emberAt(hostport string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	res, err := client.Get("http://" + hostport + "/api/status")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var status struct{ Version string }
	return res.StatusCode == http.StatusOK && json.NewDecoder(res.Body).Decode(&status) == nil && status.Version != ""
}
