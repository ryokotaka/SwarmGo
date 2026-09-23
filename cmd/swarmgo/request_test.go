package main

import (
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryokotaka/SwarmGo/internal/worker"
)

func parseRequestFlags(t *testing.T, args ...string) (*requestFlags, error) {
	t.Helper()
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	request := addRequestFlags(flags)
	return request, flags.Parse(args)
}

func TestRequestFlagsLoadBodyAndHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	const body = "{\"message\":\"hello\"}\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	flags, err := parseRequestFlags(t, "-method", "POST", "-body-file", path, "-header", "content-type: text/plain", "-header", "Content-Type: application/json", "-header", "X-Test: a:b")
	if err != nil {
		t.Fatal(err)
	}
	options, err := flags.load()
	if err != nil {
		t.Fatal(err)
	}
	if options.Method != http.MethodPost || string(options.Body) != body || options.Headers["Content-Type"] != "application/json" || options.Headers["X-Test"] != "a:b" || len(options.Headers) != 2 {
		t.Fatalf("unexpected options: %+v", options)
	}
}

func TestRequestFlagsDefaultsToGET(t *testing.T) {
	flags, err := parseRequestFlags(t)
	if err != nil {
		t.Fatal(err)
	}
	options, err := flags.load()
	if err != nil || options.Method != http.MethodGet || len(options.Body) != 0 || len(options.Headers) != 0 {
		t.Fatalf("changed GET defaults: %+v, %v", options, err)
	}
}

func TestRequestFlagsRejectInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"-method", "BAD METHOD"},
		{"-header", "missing-colon"},
		{"-header", "Bad Header: value"},
		{"-header", "X-Test: value\r\ninjected: true"},
		{"-header", "Content-Length: 123"},
		{"-body-file", filepath.Join(t.TempDir(), "missing.json")},
		{"-body-file", t.TempDir()},
	} {
		flags, err := parseRequestFlags(t, args...)
		if err == nil {
			_, err = flags.load()
		}
		if err == nil {
			t.Errorf("accepted invalid flags %q", args)
		}
	}
}

func TestRequestFlagsRejectOversizedBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", worker.MaxRequestBodyBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	flags, err := parseRequestFlags(t, "-body-file", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flags.load(); err == nil {
		t.Fatal("accepted body larger than the protocol limit")
	}
}
