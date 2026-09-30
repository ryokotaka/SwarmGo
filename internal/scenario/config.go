// Package scenario turns a YAML description of several weighted requests into
// templates that a worker fills per request without parsing or allocating.
//
// Load reads the file, its CSV sources and environment values on the machine
// that owns the run and returns a Spec: plain data, safe to send to workers.
// Compile turns a Spec into templates on each worker.
package scenario

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxDataBytes caps the CSV data one run carries to its workers.
const MaxDataBytes = 16 << 20

// Spec is a loaded scenario: environment values are already substituted and
// CSV sources are read into rows, so a worker needs nothing but the Spec.
type Spec struct {
	Target   string            `json:"target"`
	Requests []RequestSpec     `json:"requests"`
	Data     map[string]*Table `json:"data,omitempty"`
}

// RequestSpec is one named request. Path, header values and Body may contain
// {{...}} variables; request headers already include the shared ones.
type RequestSpec struct {
	Name    string            `json:"name"`
	Weight  int               `json:"weight"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// Table is one CSV source. Order is "sequential" or "random".
type Table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	Order   string     `json:"order"`
}

// LoadSettings are the optional load: values; command-line flags override them.
type LoadSettings struct {
	Requests    int `yaml:"requests"`
	Concurrency int `yaml:"concurrency"`
	Rate        int `yaml:"rate"`
}

type fileConfig struct {
	Target   string                `yaml:"target"`
	Headers  map[string]string     `yaml:"headers"`
	Data     map[string]fileSource `yaml:"data"`
	Requests []fileRequest         `yaml:"requests"`
	Load     LoadSettings          `yaml:"load"`
}

type fileSource struct {
	CSV   string `yaml:"csv"`
	Order string `yaml:"order"`
}

type fileRequest struct {
	Name     string            `yaml:"name"`
	Weight   *int              `yaml:"weight"`
	Method   string            `yaml:"method"`
	Path     string            `yaml:"path"`
	Headers  map[string]string `yaml:"headers"`
	Body     string            `yaml:"body"`
	BodyFile string            `yaml:"body_file"`
}

// Load reads a scenario file. Relative CSV and body_file paths are relative to
// the file. getenv resolves {{env.NAME}}; a nil getenv uses os.LookupEnv.
// maxBody is the largest request body the caller accepts.
func Load(path string, getenv func(string) (string, bool), maxBody int) (*Spec, LoadSettings, error) {
	if getenv == nil {
		getenv = os.LookupEnv
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, LoadSettings{}, err
	}
	var f fileConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // A typo must fail, not silently change a test.
	if err := dec.Decode(&f); err != nil {
		return nil, LoadSettings{}, fmt.Errorf("%s: %w", path, err)
	}
	spec, err := build(f, filepath.Dir(path), getenv, maxBody)
	if err != nil {
		return nil, LoadSettings{}, fmt.Errorf("%s: %w", path, err)
	}
	if f.Load.Requests < 0 || f.Load.Concurrency < 0 || f.Load.Rate < 0 {
		return nil, LoadSettings{}, fmt.Errorf("%s: load values must not be negative", path)
	}
	return spec, f.Load, nil
}

func build(f fileConfig, dir string, getenv func(string) (string, bool), maxBody int) (*Spec, error) {
	target, err := url.Parse(f.Target)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, fmt.Errorf("target must be an absolute http or https URL, got %q", f.Target)
	}
	if (target.Path != "" && target.Path != "/") || target.RawQuery != "" || target.Fragment != "" {
		return nil, fmt.Errorf("target must be an origin (scheme, host and port); put paths in requests")
	}
	spec := &Spec{Target: target.Scheme + "://" + target.Host, Data: map[string]*Table{}}
	total := 0
	for name, src := range f.Data {
		if !validName(name) || name == "env" || name == "random" || name == "seq" {
			return nil, fmt.Errorf("data %q: names use letters, digits and _, and may not be env, random or seq", name)
		}
		table, size, err := readCSV(resolve(dir, src.CSV))
		if err != nil {
			return nil, fmt.Errorf("data %q: %w", name, err)
		}
		if total += size; total > MaxDataBytes {
			return nil, fmt.Errorf("CSV data exceeds %d bytes", MaxDataBytes)
		}
		switch src.Order {
		case "", "sequential":
			table.Order = "sequential"
		case "random":
			table.Order = "random"
		default:
			return nil, fmt.Errorf("data %q: order must be sequential or random", name)
		}
		spec.Data[name] = table
	}
	if len(f.Requests) == 0 {
		return nil, errors.New("requests: at least one request is required")
	}
	seen := map[string]bool{}
	for i, r := range f.Requests {
		where := fmt.Sprintf("requests[%d]", i)
		if r.Name == "" {
			return nil, fmt.Errorf("%s: name is required", where)
		}
		where = fmt.Sprintf("request %q", r.Name)
		if seen[r.Name] {
			return nil, fmt.Errorf("%s: duplicate name", where)
		}
		seen[r.Name] = true
		weight := 1
		if r.Weight != nil {
			weight = *r.Weight
		}
		if weight < 1 {
			return nil, fmt.Errorf("%s: weight must be at least 1", where)
		}
		method := r.Method
		if method == "" {
			method = http.MethodGet
		}
		if !strings.HasPrefix(r.Path, "/") {
			return nil, fmt.Errorf("%s: path must start with /", where)
		}
		body := r.Body
		if r.BodyFile != "" {
			if body != "" {
				return nil, fmt.Errorf("%s: use body or body_file, not both", where)
			}
			b, err := readLimited(resolve(dir, r.BodyFile), maxBody)
			if err != nil {
				return nil, fmt.Errorf("%s: body_file: %w", where, err)
			}
			body = string(b)
		}
		headers := map[string]string{}
		for k, v := range f.Headers {
			headers[http.CanonicalHeaderKey(k)] = v
		}
		for k, v := range r.Headers {
			headers[http.CanonicalHeaderKey(k)] = v
		}
		req := RequestSpec{Name: r.Name, Weight: weight, Method: method, Path: r.Path, Headers: headers, Body: body}
		if err := substituteEnv(&req, getenv); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		spec.Requests = append(spec.Requests, req)
	}
	if err := spec.Validate(maxBody); err != nil {
		return nil, err
	}
	return spec, nil
}

// Validate checks everything that can be checked before traffic: variable
// references, header safety of the values that can reach a header, and the
// largest possible body. Workers run it again on the Spec they receive.
func (s *Spec) Validate(maxBody int) error {
	if len(s.Requests) == 0 {
		return errors.New("requests: at least one request is required")
	}
	for name, t := range s.Data {
		if len(t.Rows) == 0 {
			return fmt.Errorf("data %q: no rows", name)
		}
		for i, row := range t.Rows {
			if len(row) != len(t.Columns) {
				return fmt.Errorf("data %q: row %d has %d cells; want %d", name, i+2, len(row), len(t.Columns))
			}
		}
	}
	for _, r := range s.Requests {
		where := fmt.Sprintf("request %q", r.Name)
		if _, err := http.NewRequest(r.Method, "http://localhost", nil); err != nil {
			return fmt.Errorf("%s: invalid method: %w", where, err)
		}
		switch r.Method {
		case http.MethodHead, http.MethodConnect:
			return fmt.Errorf("%s: %s is not supported in a scenario", where, r.Method)
		}
		for k := range r.Headers {
			if strings.EqualFold(k, "Expect") || strings.EqualFold(k, "Upgrade") || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
				return fmt.Errorf("%s: header %s is not supported in a scenario", where, k)
			}
		}
		pathParts, err := parse(r.Path)
		if err != nil {
			return fmt.Errorf("%s: path: %w", where, err)
		}
		if err := s.checkRefs(pathParts, false); err != nil {
			return fmt.Errorf("%s: path: %w", where, err)
		}
		for k, v := range r.Headers {
			parts, err := parse(v)
			if err != nil {
				return fmt.Errorf("%s: header %s: %w", where, k, err)
			}
			if err := s.checkRefs(parts, true); err != nil {
				return fmt.Errorf("%s: header %s: %w", where, k, err)
			}
		}
		bodyParts, err := parse(r.Body)
		if err != nil {
			return fmt.Errorf("%s: body: %w", where, err)
		}
		if err := s.checkRefs(bodyParts, false); err != nil {
			return fmt.Errorf("%s: body: %w", where, err)
		}
		if n := s.maxLen(bodyParts); n > maxBody {
			return fmt.Errorf("%s: body can reach %d bytes; the limit is %d", where, n, maxBody)
		}
	}
	return nil
}

// checkRefs resolves every variable. A value that can reach a header must not
// hold CR or LF, which would let data inject headers.
func (s *Spec) checkRefs(parts []part, header bool) error {
	for _, p := range parts {
		switch p.kind {
		case partColumn:
			t := s.Data[p.source]
			if t == nil {
				return fmt.Errorf("unknown data source %q", p.source)
			}
			col := index(t.Columns, p.column)
			if col < 0 {
				return fmt.Errorf("data %q has no column %q", p.source, p.column)
			}
			if header {
				for _, row := range t.Rows {
					if strings.ContainsAny(row[col], "\r\n") {
						return fmt.Errorf("column %s.%s holds a line break and cannot be used in a header", p.source, p.column)
					}
				}
			}
		case partEnv:
			return fmt.Errorf("{{env.%s}} was not resolved", p.source)
		}
	}
	return nil
}

func (s *Spec) maxLen(parts []part) int {
	n := 0
	for _, p := range parts {
		switch p.kind {
		case partLiteral:
			n += len(p.text)
		case partColumn:
			t := s.Data[p.source]
			col := index(t.Columns, p.column)
			longest := 0
			for _, row := range t.Rows {
				longest = max(longest, len(row[col]))
			}
			n += longest
		case partUUID:
			n += 36
		default:
			n += 20 // The longest decimal int64.
		}
	}
	return n
}

func substituteEnv(r *RequestSpec, getenv func(string) (string, bool)) error {
	var err error
	sub := func(s string) string {
		out, e := replaceEnv(s, getenv)
		if e != nil && err == nil {
			err = e
		}
		return out
	}
	r.Path = sub(r.Path)
	r.Body = sub(r.Body)
	for k, v := range r.Headers {
		r.Headers[k] = sub(v)
	}
	return err
}

func readCSV(path string) (*Table, int, error) {
	raw, err := readLimited(path, MaxDataBytes)
	if err != nil {
		return nil, 0, err
	}
	rd := csv.NewReader(bytes.NewReader(raw))
	records, err := rd.ReadAll() // Enforces the same number of cells per row.
	if err != nil {
		return nil, 0, err
	}
	if len(records) < 2 {
		return nil, 0, errors.New("needs a header row and at least one data row")
	}
	for _, c := range records[0] {
		if !validName(c) {
			return nil, 0, fmt.Errorf("column %q: names use letters, digits and _", c)
		}
	}
	return &Table{Columns: records[0], Rows: records[1:]}, len(raw), nil
}

func readLimited(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("exceeds %d bytes", limit)
	}
	return b, nil
}

func resolve(dir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func index(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
