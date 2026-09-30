package scenario

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// writeHTTP serializes like net/http does; the worker passes its own direct-path
// writer, which the worker package tests against the same property.
func writeHTTP(method, target string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	var b bytes.Buffer
	err = req.Write(&b)
	return b.Bytes(), err
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const exampleYAML = `
target: http://api.example.test:8080
headers:
  Authorization: Bearer {{env.TOKEN}}
data:
  users:
    csv: users.csv
requests:
  - name: profile
    weight: 6
    path: /users/{{users.id}}
  - name: search
    weight: 3
    path: /search?q={{users.city}}&page={{random.int(1,20)}}
  - name: order
    method: POST
    path: /orders
    headers:
      Content-Type: application/json
    body: '{"user":"{{users.id}}","sku":{{random.int(1,5000)}},"ref":"{{seq}}","id":"{{random.uuid}}"}'
load:
  requests: 1000
  concurrency: 8
`

func loadExample(t *testing.T) *Spec {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "users.csv", "id,city\n1,Tokyo\n2,New York\n3,São Paulo\n")
	p := writeFile(t, dir, "run.yaml", exampleYAML)
	spec, load, err := Load(p, func(k string) (string, bool) { return map[string]string{"TOKEN": "t0k{{en"}[k], k == "TOKEN" }, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if load.Requests != 1000 || load.Concurrency != 8 {
		t.Fatalf("load settings: %+v", load)
	}
	return spec
}

func TestLoadResolvesEnvAndData(t *testing.T) {
	spec := loadExample(t)
	if spec.Target != "http://api.example.test:8080" || len(spec.Requests) != 3 || spec.Requests[0].Weight != 6 || spec.Requests[2].Weight != 1 {
		t.Fatalf("spec: %+v", spec)
	}
	// An environment value is inserted literally, even when it looks like a variable.
	if got := spec.Requests[0].Headers["Authorization"]; got != "Bearer t0k{{{{en" {
		t.Fatalf("Authorization = %q", got)
	}
	users := spec.Data["users"]
	if users.Order != "sequential" || len(users.Rows) != 3 || users.Rows[2][1] != "São Paulo" {
		t.Fatalf("users: %+v", users)
	}
	s, err := Compile(spec, Position{0, 1}, writeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(s.NewLane(1).Fill(0))
	if !strings.Contains(wire, "Authorization: Bearer t0k{{en\r\n") || !strings.HasPrefix(wire, "GET /users/1 HTTP/1.1\r\n") {
		t.Fatalf("wire:\n%s", wire)
	}
}

func TestLoadRejectsBrokenConfigs(t *testing.T) {
	for _, tc := range []struct{ name, yaml, csv, want string }{
		{"unknown key", "target: http://a.test\nrequests:\n  - name: a\n    path: /\n    wieght: 2\n", "", "wieght"},
		{"missing env", "target: http://a.test\nrequests:\n  - name: a\n    path: /{{env.NOPE}}\n", "", "NOPE is not set"},
		{"unknown column", "target: http://a.test\ndata:\n  u:\n    csv: u.csv\nrequests:\n  - name: a\n    path: /{{u.nope}}\n", "id\n1\n", "no column"},
		{"unknown source", "target: http://a.test\nrequests:\n  - name: a\n    path: /{{u.id}}\n", "", "unknown data source"},
		{"duplicate", "target: http://a.test\nrequests:\n  - name: a\n    path: /\n  - name: a\n    path: /\n", "", "duplicate"},
		{"weight", "target: http://a.test\nrequests:\n  - name: a\n    path: /\n    weight: 0\n", "", "weight"},
		{"header newline", "target: http://a.test\ndata:\n  u:\n    csv: u.csv\nrequests:\n  - name: a\n    path: /\n    headers:\n      X-Id: '{{u.id}}'\n", "id\n\"a\nb\"\n", "line break"},
		{"target path", "target: http://a.test/v1\nrequests:\n  - name: a\n    path: /\n", "", "origin"},
		{"relative path", "target: http://a.test\nrequests:\n  - name: a\n    path: users\n", "", "start with /"},
		{"head", "target: http://a.test\nrequests:\n  - name: a\n    method: HEAD\n    path: /\n", "", "not supported"},
		{"bad function", "target: http://a.test\nrequests:\n  - name: a\n    path: /{{random.float}}\n", "", "unknown random function"},
		{"body too large", "target: http://a.test\nrequests:\n  - name: a\n    method: POST\n    path: /\n    body: '0123456789{{seq}}'\n", "", "limit"},
		{"ragged csv", "target: http://a.test\ndata:\n  u:\n    csv: u.csv\nrequests:\n  - name: a\n    path: /{{u.id}}\n", "id,x\n1\n", "wrong number of fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.csv != "" {
				writeFile(t, dir, "u.csv", tc.csv)
			}
			_, _, err := Load(writeFile(t, dir, "run.yaml", tc.yaml), func(string) (string, bool) { return "", false }, 20)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want %q", err, tc.want)
			}
		})
	}
}

// FuzzFillMatchesNetHTTP checks the central property: a filled template equals
// net/http's serialization of the same request with the same values.
func FuzzFillMatchesNetHTTP(f *testing.F) {
	f.Add("Tokyo", "a b/c?d", " padded ", "x&y=z")
	f.Add("", "%", "é", "\"quote\"")
	f.Fuzz(func(t *testing.T, a, b, c, d string) {
		for _, v := range []string{a, b, c, d} {
			if strings.ContainsAny(v, "\r\n") || !utf8Valid(v) {
				t.Skip()
			}
		}
		table := &Table{Columns: []string{"a", "b", "c", "d"}, Rows: [][]string{{a, b, c, d}}, Order: "sequential"}
		spec := &Spec{Target: "http://h.test:81", Data: map[string]*Table{"t": table}, Requests: []RequestSpec{{
			Name: "r", Weight: 1, Method: "POST",
			Path:    "/x/{{t.a}}/{{t.b}}?q={{t.c}}&r={{t.d}}",
			Headers: map[string]string{"X-Mid": "pre-{{t.c}}-post", "X-Whole": "{{t.c}}", "Content-Type": "text/plain"},
			Body:    "<{{t.a}}|{{t.b}}|{{t.d}}>",
		}}}
		s, err := Compile(spec, Position{0, 1}, writeHTTP)
		if err != nil {
			t.Fatal(err)
		}
		got := s.NewLane(7).Fill(0)
		want, err := writeHTTP("POST", "http://h.test:81/x/"+url.PathEscape(a)+"/"+url.PathEscape(b)+"?q="+url.QueryEscape(c)+"&r="+url.QueryEscape(d),
			map[string]string{"X-Mid": "pre-" + c + "-post", "X-Whole": c, "Content-Type": "text/plain"},
			[]byte("<"+a+"|"+b+"|"+d+">"))
		if err != nil {
			t.Skip() // net/http itself rejects this input.
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("filled:\n%q\nnet/http:\n%q", got, want)
		}
	})
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "\uFFFD") == s }

func TestGeneratedValuesAndContentLength(t *testing.T) {
	s, err := Compile(loadExample(t), Position{0, 1}, writeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	lane := s.NewLane(3)
	body := regexp.MustCompile(`(?s)\r\nContent-Length: (\d+)\r\n.*?\r\n\r\n(.*)$`)
	order := regexp.MustCompile(`^\{"user":"(\d)","sku":(\d+),"ref":"(\d+)","id":"([0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})"\}$`)
	uuids := map[string]bool{}
	for i := 0; i < 2000; i++ {
		m := body.FindSubmatch(lane.Fill(2))
		if m == nil {
			t.Fatal("no Content-Length")
		}
		if n, _ := strconv.Atoi(string(m[1])); n != len(m[2]) {
			t.Fatalf("Content-Length %d for a %d-byte body", n, len(m[2]))
		}
		o := order.FindSubmatch(m[2])
		if o == nil {
			t.Fatalf("body %q", m[2])
		}
		if sku, _ := strconv.Atoi(string(o[2])); sku < 1 || sku > 5000 {
			t.Fatalf("sku %d out of range", sku)
		}
		if ref, _ := strconv.Atoi(string(o[3])); ref != i {
			t.Fatalf("seq %d at request %d", ref, i)
		}
		if string(o[1]) != strconv.Itoa(i%3+1) {
			t.Fatalf("row %s at request %d; want sequential rows", o[1], i)
		}
		uuids[string(o[4])] = true
	}
	if len(uuids) != 2000 {
		t.Fatalf("%d distinct UUIDs in 2000", len(uuids))
	}
}

func TestWeightsPickInProportion(t *testing.T) {
	s, err := Compile(loadExample(t), Position{0, 1}, writeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	lane := s.NewLane(11)
	const n = 1_000_000
	var counts [3]int
	for i := 0; i < n; i++ {
		idx, _ := lane.Next()
		counts[idx]++
	}
	for i, w := range []float64{0.6, 0.3, 0.1} {
		if got := float64(counts[i]) / n; got < w-0.005 || got > w+0.005 {
			t.Fatalf("request %d share %.4f; want %.2f ± 0.005", i, got, w)
		}
	}
}

func TestSequentialRowsInterleaveAcrossWorkers(t *testing.T) {
	table := &Table{Columns: []string{"id"}, Order: "sequential"}
	for i := 0; i < 10; i++ {
		table.Rows = append(table.Rows, []string{strconv.Itoa(i)})
	}
	spec := &Spec{Target: "http://h.test", Data: map[string]*Table{"t": table}, Requests: []RequestSpec{{Name: "r", Weight: 1, Method: "GET", Path: "/{{t.id}}/{{seq}}"}}}
	for w := 0; w < 3; w++ {
		s, err := Compile(spec, Position{w, 3}, writeHTTP)
		if err != nil {
			t.Fatal(err)
		}
		lane := s.NewLane(1)
		for k := 0; k < 5; k++ {
			want := fmt.Sprintf("GET /%d/%d HTTP/1.1", (w+3*k)%10, w+3*k)
			if got := string(lane.Fill(0)); !strings.HasPrefix(got, want) {
				t.Fatalf("worker %d request %d: %q; want %q", w, k, got[:strings.Index(got, "\r")], want)
			}
		}
	}
}

func TestRandomRowsCoverTable(t *testing.T) {
	table := &Table{Columns: []string{"id"}, Order: "random"}
	for i := 0; i < 20; i++ {
		table.Rows = append(table.Rows, []string{strconv.Itoa(i)})
	}
	spec := &Spec{Target: "http://h.test", Data: map[string]*Table{"t": table}, Requests: []RequestSpec{{Name: "r", Weight: 1, Method: "GET", Path: "/{{t.id}}"}}}
	s, err := Compile(spec, Position{0, 1}, writeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	lane, seen := s.NewLane(uint64(rand.Int64())), map[string]bool{}
	for i := 0; i < 2000; i++ {
		seen[strings.Fields(string(lane.Fill(0)))[1]] = true
	}
	if len(seen) != 20 {
		t.Fatalf("random order visited %d of 20 rows", len(seen))
	}
}

func TestStaticRequestIsOneSegment(t *testing.T) {
	spec := &Spec{Target: "http://h.test", Requests: []RequestSpec{{Name: "r", Weight: 1, Method: "POST", Path: "/work", Body: "payload", Headers: map[string]string{"Content-Type": "application/json"}}}}
	s, err := Compile(spec, Position{0, 1}, writeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := writeHTTP("POST", "http://h.test/work", map[string]string{"Content-Type": "application/json"}, []byte("payload"))
	if got := s.NewLane(1).Fill(0); !bytes.Equal(got, want) || len(s.Templates[0].head)+len(s.Templates[0].body) != 2 {
		t.Fatalf("static request: %q (%d head, %d body segments)", got, len(s.Templates[0].head), len(s.Templates[0].body))
	}
}

func TestFillDoesNotAllocate(t *testing.T) {
	s, err := Compile(loadExample(t), Position{0, 1}, writeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	lane := s.NewLane(5)
	for i := 0; i < 100; i++ {
		lane.Next() // Grow the lane's buffers to their steady size.
	}
	if n := testing.AllocsPerRun(1000, func() { lane.Next() }); n != 0 {
		t.Fatalf("%.1f allocations per request; want 0", n)
	}
}

func BenchmarkLaneNext(b *testing.B) {
	dir := b.TempDir()
	os.WriteFile(filepath.Join(dir, "users.csv"), []byte("id,city\n1,Tokyo\n2,New York\n3,São Paulo\n"), 0o600)
	p := filepath.Join(dir, "run.yaml")
	os.WriteFile(p, []byte(exampleYAML), 0o600)
	spec, _, err := Load(p, func(string) (string, bool) { return "t", true }, 1<<20)
	if err != nil {
		b.Fatal(err)
	}
	s, err := Compile(spec, Position{0, 1}, writeHTTP)
	if err != nil {
		b.Fatal(err)
	}
	lane := s.NewLane(9)
	b.ReportAllocs()
	for b.Loop() {
		lane.Next()
	}
}
