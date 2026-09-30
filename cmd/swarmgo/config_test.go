package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/scenario"
)

// writeScenario writes a config whose "order" request reads each users row
// once, in order, and a static "health" request.
func writeScenario(t *testing.T, target string, rows int) string {
	t.Helper()
	dir := t.TempDir()
	var csv strings.Builder
	csv.WriteString("id\n")
	for i := range rows {
		fmt.Fprintf(&csv, "%d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "users.csv"), []byte(csv.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := "target: " + target + `
data:
  users: {csv: users.csv, order: sequential}
requests:
  - name: order
    weight: 1
    method: POST
    path: /orders
    headers: {Content-Type: text/plain}
    body: "user={{users.id}}"
  - name: health
    weight: 1
    path: /health
load:
  requests: 7
  concurrency: 3
`
	path := filepath.Join(dir, "run.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunConfigFlagsConflictAndOverride(t *testing.T) {
	path := writeScenario(t, "http://127.0.0.1:8080", 3)
	for _, args := range [][]string{
		{"-config", path, "-url", "http://127.0.0.1:9"},
		{"-config", path, "-method", "POST"},
		{"-config", path, "-header", "X-A: b"},
		{"-print", "2"},
	} {
		if _, err := parseRunOptions(args, io.Discard); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
	options, err := parseRunOptions([]string{"-config", path}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.command.TotalRequests != 7 || options.command.Concurrency != 3 || options.command.TargetUrl != "http://127.0.0.1:8080" {
		t.Errorf("load settings not applied: %+v", options.command)
	}
	var spec scenario.Spec
	if err := json.Unmarshal(options.command.Scenario, &spec); err != nil || len(spec.Requests) != 2 || len(spec.Data["users"].Rows) != 3 {
		t.Errorf("encoded scenario %+v, %v", spec, err)
	}
	options, err = parseRunOptions([]string{"-config", path, "-n", "11", "-c", "2"}, io.Discard)
	if err != nil || options.command.TotalRequests != 11 || options.command.Concurrency != 2 {
		t.Errorf("flags must override load: %+v, %v", options.command, err)
	}
}

func TestPrintShowsRequestsWithoutSending(t *testing.T) {
	var hits sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Store(r.URL.Path, true) }))
	defer server.Close()
	path := writeScenario(t, server.URL, 3)
	var out bytes.Buffer
	stdout = &out
	defer func() { stdout = os.Stdout }()
	if code := runCommandContext(t.Context(), []string{"-config", path, "-print", "6"}, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if code := resilienceCommandContext(t.Context(), []string{"-config", path, "-print", "2"}, io.Discard); code != 0 {
		t.Fatalf("resilience exit %d", code)
	}
	got := out.String()
	if strings.Count(got, "### ") != 8 || !strings.Contains(got, "POST /orders HTTP/1.1\r\n") || !strings.Contains(got, "user=0") {
		t.Fatalf("printed:\n%s", got)
	}
	hits.Range(func(k, _ any) bool { t.Errorf("-print sent %v", k); return true })
}

// Two workers share the sequential rows: each is sent once, and the report
// counts every request by name.
func TestRunScenarioAcrossWorkers(t *testing.T) {
	var mu sync.Mutex
	users := map[string]int{}
	health := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/orders":
			users[string(body)]++
		case r.Method == "GET" && r.URL.Path == "/health":
			health++
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer server.Close()
	path := writeScenario(t, server.URL, 1000)
	options, err := parseRunOptions([]string{"-config", path, "-workers", "2", "-n", "200", "-c", "4"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	options.workerTimeout, options.timeout = time.Second, 5*time.Second
	addr, outcome, _ := startRunTest(t, options)
	startRunWorker(t, addr)
	startRunWorker(t, addr)
	result := runResult(t, outcome)
	report := result.report
	if report.Method != "scenario" || report.Config != path || len(report.ByRequest) != 2 {
		t.Fatalf("report: %+v", report)
	}
	order, check := report.ByRequest[0], report.ByRequest[1]
	if order.Name != "order" || order.Failed != 0 || check.Name != "health" || check.Failed != check.Completed || check.StatusCodes[503] != check.Completed {
		t.Errorf("by request: %+v", report.ByRequest)
	}
	if order.Completed+check.Completed != 400 || report.Requests.Failed != check.Completed {
		t.Errorf("totals: %+v, by request %+v", report.Requests, report.ByRequest)
	}
	mu.Lock()
	defer mu.Unlock()
	if int64(len(users)) != order.Completed || int64(health) != check.Completed {
		t.Errorf("server saw %d distinct users and %d health checks; report %+v", len(users), health, report.ByRequest)
	}
	for user, n := range users {
		if n != 1 {
			t.Errorf("%s sent %d times", user, n)
		}
	}
	for _, w := range report.Workers {
		if len(w.ByRequest) != 2 || w.ByRequest[0].LatencyUS == nil || w.ByRequest[1].LatencyUS != nil {
			t.Errorf("worker %s by request: %+v", w.ID, w.ByRequest)
		}
	}
}

func TestResilienceConfigReplacesLoadRequest(t *testing.T) {
	path := writeScenario(t, "http://127.0.0.1:8080", 3)
	c, _, err := parseResilienceOptions([]string{"-config", path, "-probe-url", "http://127.0.0.1:8080/health"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Scenario == nil || c.URL != "http://127.0.0.1:8080/" || c.Concurrency != 3 || c.ProbeURL != "http://127.0.0.1:8080/health" {
		t.Errorf("config: %+v", c)
	}
	if _, _, err := parseResilienceOptions([]string{"-config", path, "-body-file", path}, io.Discard); err == nil {
		t.Error("-body-file with -config: want an error")
	}
}
