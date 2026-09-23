package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryokotaka/SwarmGo/internal/resilience"
)

func TestResilienceRejectsInvalidPlanBeforeTraffic(t *testing.T) {
	for _, args := range [][]string{
		{"-rate", "0"}, {"-baseline", "500ms"}, {"-max-error-rate", "NaN"},
		{"-max-error-rate", "1"}, {"-recovery", "1s", "-recovery-window", "2s"},
		{"-max-start-delay", "1s"}, {"-probe-status", "500"}, {"unexpected"},
	} {
		if _, _, err := parseResilienceOptions(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestResilienceWritesPartialResultOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "result.json")
	var log bytes.Buffer
	code := resilienceCommandContext(ctx, []string{"-url", srv.URL, "-baseline", "1s", "-spike", "1s", "-recovery", "1s", "-recovery-window", "1s", "-output", path}, &log)
	if code != 2 {
		t.Fatalf("code=%d log=%s", code, log.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report resilience.Report
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "inconclusive" || !report.Probe.Canceled || report.Probe.Started != 0 {
		t.Fatalf("report=%+v", report)
	}
}

func TestProbeOptionsAreIndependent(t *testing.T) {
	c, _, err := parseResilienceOptions([]string{"-method", "POST", "-header", "Authorization: load-secret", "-probe-header", "Authorization: ordinary-secret"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if c.ProbeRequest.Method != "GET" || c.Request.Headers["Authorization"] != "load-secret" || c.ProbeRequest.Headers["Authorization"] != "ordinary-secret" {
		t.Fatalf("requests mixed: %+v", c)
	}
}
