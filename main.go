// Command ember serves a local dashboard for managing books on a KOReader
// Kindle over SSH.
package main

import (
	"context"
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
	"github.com/odmrs/ember/internal/server"
)

var version = "0.1.0-dev"

func main() {
	port := flag.Int("port", 8737, "local port for the dashboard (0 picks a free one)")
	noOpen := flag.Bool("no-open", false, "do not open the browser on start")
	flag.Parse()

	if err := run(*port, !*noOpen); err != nil {
		fmt.Fprintln(os.Stderr, "ember:", err)
		os.Exit(1)
	}
}

func run(port int, open bool) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("cannot listen on 127.0.0.1:%d (already running? try -port 0): %w", port, err)
	}
	hostport := ln.Addr().String()
	srv := &http.Server{
		Handler:           server.New(hostport, version),
		ReadHeaderTimeout: 10 * time.Second,
	}

	url := "http://" + hostport + "/"
	log.Printf("ember %s running at %s (Ctrl+C to stop)", version, url)
	if open {
		if err := browser.Open(url); err != nil {
			log.Printf("could not open the browser, open %s yourself", url)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
