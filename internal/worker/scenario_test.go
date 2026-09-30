package worker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/scenario"
)

var onlyWorker = scenario.Position{Worker: 0, Workers: 1}

// A filled scenario request must be the exact bytes the direct path sends for
// the same request given with -url, -method, -header and -body-file.
func TestScenarioBytesEqualDirectWire(t *testing.T) {
	spec := &scenario.Spec{
		Target: "http://user:pw@127.0.0.1:8080",
		Requests: []scenario.RequestSpec{
			{Name: "get", Weight: 1, Method: "GET", Path: "/items/{{users.id}}?q={{users.name}}", Headers: map[string]string{"X-User": "{{users.name}}"}},
			{Name: "post", Weight: 1, Method: "POST", Path: "/orders", Headers: map[string]string{"Content-Type": "application/json", "Accept-Encoding": "br"}, Body: `{"user":"{{users.id}}"}`},
		},
		Data: map[string]*scenario.Table{"users": {Columns: []string{"id", "name"}, Rows: [][]string{{"7", "a b/c"}}, Order: "sequential"}},
	}
	r := NewMyRunner()
	plan, err := r.scenarioPlan(spec, onlyWorker)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		url     string
		options RequestOptions
	}{
		{"http://user:pw@127.0.0.1:8080/items/7?q=a+b%2Fc", RequestOptions{Method: "GET", Headers: map[string]string{"X-User": "a b/c"}}},
		{"http://user:pw@127.0.0.1:8080/orders", RequestOptions{Method: "POST", Headers: map[string]string{"Content-Type": "application/json", "Accept-Encoding": "br"}, Body: []byte(`{"user":"7"}`)}},
	}
	lane := plan.scenario.NewLane(1)
	for i, w := range want {
		template, err := requestTemplate(w.url, w.options)
		if err != nil {
			t.Fatal(err)
		}
		direct, err := r.directPlan(template)
		if err != nil || direct == nil {
			t.Fatalf("direct plan: %v", err)
		}
		got := lane.Fill(i)
		if !bytes.Equal(got, direct.reqs[0].wire) {
			t.Errorf("%s:\nscenario %q\ndirect   %q", spec.Requests[i].Name, got, direct.reqs[0].wire)
		}
		if plan.reqs[i].gzip != direct.reqs[0].gzip || plan.reqs[i].replayable != direct.reqs[0].replayable || plan.reqs[i].closeAfter != direct.reqs[0].closeAfter {
			t.Errorf("%s: request flags %+v, direct %+v", spec.Requests[i].Name, plan.reqs[i], direct.reqs[0])
		}
	}
}

type seenRequest struct{ method, uri, body string }

func recordingServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, func() []seenRequest) {
	var mu sync.Mutex
	var seen []seenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, seenRequest{r.Method, r.RequestURI, string(body)})
		mu.Unlock()
		if handle != nil {
			handle(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, func() []seenRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]seenRequest(nil), seen...)
	}
}

func TestRunScenarioSendsWeightedRequestsAndCountsEach(t *testing.T) {
	server, seen := recordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	spec := &scenario.Spec{
		Target: server.URL,
		Requests: []scenario.RequestSpec{
			{Name: "browse", Weight: 3, Method: "GET", Path: "/items/{{seq}}"},
			{Name: "buy", Weight: 1, Method: "POST", Path: "/orders", Body: "user={{users.id}}"},
			{Name: "missing", Weight: 1, Method: "GET", Path: "/missing"},
		},
		Data: map[string]*scenario.Table{"users": {Columns: []string{"id"}, Rows: [][]string{{"1"}, {"2"}}, Order: "sequential"}},
	}
	const total = 2000
	sum, err := NewMyRunner().MyRunScenario(context.Background(), spec, onlyWorker, total, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.MyTotal != total || len(sum.Requests) != 3 {
		t.Fatalf("total %d, %d request summaries", sum.MyTotal, len(sum.Requests))
	}
	byName := map[string]RequestSummary{}
	counted := 0
	for _, r := range sum.Requests {
		byName[r.Name] = r
		counted += r.MyTotal
	}
	if counted != total {
		t.Fatalf("per-request totals add to %d, want %d", counted, total)
	}
	if b := byName["browse"]; b.MyTotal < 1000 || b.MyTotal > 1400 || b.MyFailed != 0 || b.MyStatusCodeCnt[200] != b.MyTotal || b.LatencyP99 <= 0 {
		t.Errorf("browse: %+v", b)
	}
	if m := byName["missing"]; m.MyFailed != m.MyTotal || m.MyStatusCodeCnt[404] != m.MyTotal || sum.MyFailed != m.MyTotal {
		t.Errorf("missing: %+v, run failed %d", m, sum.MyFailed)
	}
	if b := byName["buy"]; b.Weight != 1 || b.MySuccess != b.MyTotal {
		t.Errorf("buy: %+v", b)
	}
	var posts, gets int
	for _, s := range seen() {
		switch {
		case s.method == "POST" && s.uri == "/orders" && (s.body == "user=1" || s.body == "user=2"):
			posts++
		case s.method == "GET" && (strings.HasPrefix(s.uri, "/items/") || s.uri == "/missing") && s.body == "":
			gets++
		default:
			t.Fatalf("unexpected request %+v", s)
		}
	}
	if posts != byName["buy"].MyTotal || posts+gets != total {
		t.Errorf("server saw %d posts, %d gets", posts, gets)
	}
}

// Redirects leave the direct path; the scenario request is rebuilt from its
// filled bytes, and the lane keeps picking scenario requests afterwards.
func TestRunScenarioFollowsRedirectsWithFilledRequest(t *testing.T) {
	server, seen := recordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/old/") {
			http.Redirect(w, r, "/new/"+strings.TrimPrefix(r.URL.Path, "/old/"), http.StatusTemporaryRedirect)
		}
	})
	spec := &scenario.Spec{
		Target: server.URL,
		Requests: []scenario.RequestSpec{
			{Name: "moved", Weight: 1, Method: "PUT", Path: "/old/{{users.id}}", Headers: map[string]string{"X-Id": "{{users.id}}"}, Body: "id={{users.id}}"},
		},
		Data: map[string]*scenario.Table{"users": {Columns: []string{"id"}, Rows: [][]string{{"1"}, {"2"}, {"3"}}, Order: "sequential"}},
	}
	sum, err := NewMyRunner().MyRunScenario(context.Background(), spec, onlyWorker, 6, 1, nil)
	if err != nil || sum.MyFailed != 0 || sum.Requests[0].MySuccess != 6 {
		t.Fatalf("summary %+v, err %v", sum, err)
	}
	got := seen()
	if len(got) != 12 {
		t.Fatalf("server saw %d requests, want 12: %+v", len(got), got)
	}
	for i := 0; i < 12; i += 2 {
		id := string(rune('1' + (i/2)%3))
		if got[i] != (seenRequest{"PUT", "/old/" + id, "id=" + id}) || got[i+1] != (seenRequest{"PUT", "/new/" + id, "id=" + id}) {
			t.Errorf("hop %d: %+v, %+v", i/2, got[i], got[i+1])
		}
	}
}

func TestPacedScenarioReportsEachRequest(t *testing.T) {
	server, _ := recordingServer(t, nil)
	spec := &scenario.Spec{
		Target: server.URL,
		Requests: []scenario.RequestSpec{
			{Name: "a", Weight: 1, Method: "GET", Path: "/a"},
			{Name: "b", Weight: 1, Method: "GET", Path: "/b"},
		},
	}
	sum, err := NewMyRunner().RunPaced(context.Background(), "", PaceOptions{Rate: 400, Duration: 500 * time.Millisecond, Concurrency: 8, MaxStartDelay: time.Second, Scenario: spec, Position: onlyWorker})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Requests) != 2 || sum.Requests[0].Name != "a" || sum.Requests[1].Name != "b" {
		t.Fatalf("requests %+v", sum.Requests)
	}
	completed := sum.Requests[0].Completed + sum.Requests[1].Completed
	if completed != sum.Completed || sum.Requests[0].Completed == 0 || sum.Requests[1].Completed == 0 || sum.Requests[0].StatusCodes[200] != sum.Requests[0].Completed {
		t.Errorf("per-request %+v, run completed %d", sum.Requests, sum.Completed)
	}
}

func TestScenarioLaneDoesNotAllocate(t *testing.T) {
	plan, err := NewMyRunnerWithConcurrency(1).scenarioPlan(benchMixSpec(), onlyWorker)
	if err != nil {
		t.Fatal(err)
	}
	lane := replayLane(plan, benchResponse)
	for range 100 {
		lane.execute()
	}
	if n := testing.AllocsPerRun(2000, func() {
		if res := lane.execute(); res.MyErr != nil {
			t.Fatal(res.MyErr)
		}
	}); n > 0 {
		t.Errorf("%.1f allocations per request", n)
	}
}

func TestScenarioNeedsDirectTransport(t *testing.T) {
	r := &MyRunner{MyClient: &http.Client{}}
	spec := &scenario.Spec{Target: "http://127.0.0.1:1", Requests: []scenario.RequestSpec{{Name: "a", Weight: 1, Method: "GET", Path: "/"}}}
	if _, err := r.MyRunScenario(context.Background(), spec, onlyWorker, 1, 1, nil); err == nil {
		t.Fatal("want an error without the direct transport")
	}
}
