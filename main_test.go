package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func hostOf(srv *httptest.Server) string { return srv.Listener.Addr().String() }

func TestEmberAtRecognisesARunningDashboard(t *testing.T) {
	ember := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"0.1.0","state":"paired"}`))
	}))
	defer ember.Close()
	if !emberAt(hostOf(ember)) {
		t.Error("did not recognise ember")
	}

	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>some other app</html>`))
	}))
	defer other.Close()
	if emberAt(hostOf(other)) {
		t.Error("took another program for ember")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	free := ln.Addr().String()
	ln.Close()
	if emberAt(free) {
		t.Error("took a closed port for ember")
	}
}
