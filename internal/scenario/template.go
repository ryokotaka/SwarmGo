package scenario

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

// WriteFunc serializes one request exactly as the sender writes it on the wire
// (request line, headers and body). Compile calls it once per request with
// markers in place of variables, and splits the result at the markers, so the
// filled bytes equal what WriteFunc would produce for the same values.
type WriteFunc func(method, target string, headers map[string]string, body []byte) ([]byte, error)

// Position is this worker's place among the run's workers. Sequential rows and
// seq are interleaved: worker i of n takes i, i+n, i+2n, ...
type Position struct{ Worker, Workers int }

// Scenario is a compiled Spec, shared read-only by every lane of one worker.
type Scenario struct {
	Templates []*Template
	tables    []*Table
	cum       []int // Cumulative weights.
	total     int
	pos       Position
	rows      []atomic.Uint64 // Sequential position per table.
	seq       atomic.Uint64
}

// Template is one request split into fixed bytes and slots.
type Template struct {
	Spec        RequestSpec
	head, body  []seg
	bodyDynamic bool
	tables      []int // Tables this request reads a row from.
}

type encoding int

const (
	encRaw encoding = iota
	encPath
	encQuery
)

type seg struct {
	lit           []byte // Fixed bytes; empty for a slot.
	slot          partKind
	contentLength bool
	table         int
	values        []string // A column slot's cells, already trimmed and encoded, by row.
	lo, hi        int64
}

// A column's cells are fixed, so each is encoded once at compile time for the
// place it is used: percent-encoded in a path or query, trimmed at the edges
// of a header value as net/http trims header values. (A header value whose
// edge cell is empty next to literal whitespace keeps that whitespace, which
// net/http would also trim; the value means the same either way.)
type valueKey struct {
	table, col       int
	enc              encoding
	trimLeft, trimRt bool
}

// Compile builds templates for every request of spec.
func Compile(spec *Spec, pos Position, write WriteFunc) (*Scenario, error) {
	if pos.Workers < 1 || pos.Worker < 0 || pos.Worker >= pos.Workers {
		return nil, fmt.Errorf("invalid worker position %d of %d", pos.Worker, pos.Workers)
	}
	s := &Scenario{pos: pos}
	names := make([]string, 0, len(spec.Data))
	for name := range spec.Data {
		names = append(names, name)
	}
	sort.Strings(names)
	tableIndex := map[string]int{}
	for i, name := range names {
		tableIndex[name] = i
		s.tables = append(s.tables, spec.Data[name])
	}
	s.rows = make([]atomic.Uint64, len(s.tables))
	nonce, err := markerNonce(spec)
	if err != nil {
		return nil, err
	}
	cache := map[valueKey][]string{}
	for _, r := range spec.Requests {
		t, err := compileOne(r, spec.Target, s.tables, tableIndex, nonce, cache, write)
		if err != nil {
			return nil, fmt.Errorf("request %q: %w", r.Name, err)
		}
		s.Templates = append(s.Templates, t)
		s.total += r.Weight
		s.cum = append(s.cum, s.total)
	}
	return s, nil
}

// markerNonce picks a random string that appears nowhere in the spec, so a
// marker can only come from a slot.
func markerNonce(spec *Spec) (string, error) {
	var all strings.Builder
	all.WriteString(spec.Target)
	for _, r := range spec.Requests {
		all.WriteString(r.Path + r.Body + r.Method)
		for k, v := range r.Headers {
			all.WriteString(k + v)
		}
	}
	for i := 0; i < 8; i++ {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		n := "sgv" + hex.EncodeToString(b)
		if !strings.Contains(all.String(), n) {
			return n, nil
		}
	}
	return "", errors.New("could not choose a slot marker")
}

type pending struct {
	marker string
	seg    seg
}

func compileOne(r RequestSpec, target string, tables []*Table, tableIndex map[string]int, nonce string, cache map[valueKey][]string, write WriteFunc) (*Template, error) {
	var slots []pending
	used := map[int]bool{}
	build := func(s string, enc encoding, header bool) (string, error) {
		parts, err := parse(s)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for i, p := range parts {
			if p.kind == partLiteral {
				b.WriteString(p.text)
				continue
			}
			g := seg{slot: p.kind, lo: p.lo, hi: p.hi}
			if p.kind == partColumn {
				ti, ok := tableIndex[p.source]
				if !ok {
					return "", fmt.Errorf("unknown data source %q", p.source)
				}
				k := valueKey{table: ti, col: index(tables[ti].Columns, p.column), enc: enc, trimLeft: header && i == 0, trimRt: header && i == len(parts)-1}
				if k.col < 0 {
					return "", fmt.Errorf("data %q has no column %q", p.source, p.column)
				}
				if cache[k] == nil {
					cache[k] = encodeColumn(tables[ti], k)
				}
				g.table, g.values = ti, cache[k]
				used[ti] = true
			}
			m := fmt.Sprintf("%s%04dz", nonce, len(slots))
			slots = append(slots, pending{marker: m, seg: g})
			b.WriteString(m)
		}
		return b.String(), nil
	}
	// A value is encoded for where it lands: the path before the first ?, the
	// query after it.
	rawPath, rawQuery, hasQuery := strings.Cut(r.Path, "?")
	path, err := build(rawPath, encPath, false)
	if err != nil {
		return nil, fmt.Errorf("path: %w", err)
	}
	if hasQuery {
		q, err := build(rawQuery, encQuery, false)
		if err != nil {
			return nil, fmt.Errorf("path: %w", err)
		}
		path += "?" + q
	}
	headers := make(map[string]string, len(r.Headers))
	for k, v := range r.Headers {
		if headers[k], err = build(v, encRaw, true); err != nil {
			return nil, fmt.Errorf("header %s: %w", k, err)
		}
	}
	bodySlots := len(slots)
	body, err := build(r.Body, encRaw, false)
	if err != nil {
		return nil, fmt.Errorf("body: %w", err)
	}
	dynamicBody := len(slots) > bodySlots
	wire, err := write(r.Method, target+path, headers, []byte(body))
	if err != nil {
		return nil, err
	}
	split := bytes.Index(wire, []byte("\r\n\r\n"))
	if split < 0 {
		return nil, errors.New("request has no header terminator")
	}
	headBytes, bodyBytes := wire[:split+4], wire[split+4:]
	t := &Template{Spec: r, bodyDynamic: dynamicBody}
	for ti := range used {
		t.tables = append(t.tables, ti)
	}
	sort.Ints(t.tables)
	if dynamicBody {
		// The body's length changes per request: Content-Length becomes a slot.
		cl := []byte("\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n")
		at := bytes.Index(headBytes, cl)
		if at < 0 || !bytes.Equal(bodyBytes, []byte(body)) {
			return nil, errors.New("a body with variables must be sent with Content-Length")
		}
		head, err := splitMarkers(headBytes[:at+len("\r\nContent-Length: ")], slots)
		if err != nil {
			return nil, err
		}
		tail, err := splitMarkers(headBytes[at+len(cl)-2:], slots)
		if err != nil {
			return nil, err
		}
		t.head = append(append(head, seg{contentLength: true}), tail...)
	} else if t.head, err = splitMarkers(headBytes, slots); err != nil {
		return nil, err
	}
	if t.body, err = splitMarkers(bodyBytes, slots); err != nil {
		return nil, err
	}
	for _, p := range slots {
		if bytes.Count(wire, []byte(p.marker)) != 1 {
			return nil, errors.New("a variable was changed by request serialization")
		}
	}
	return t, nil
}

func splitMarkers(b []byte, slots []pending) ([]seg, error) {
	var out []seg
	for len(b) > 0 {
		first, which := -1, -1
		for i, p := range slots {
			if at := bytes.Index(b, []byte(p.marker)); at >= 0 && (first < 0 || at < first) {
				first, which = at, i
			}
		}
		if first < 0 {
			out = append(out, seg{lit: bytes.Clone(b)})
			break
		}
		if first > 0 {
			out = append(out, seg{lit: bytes.Clone(b[:first])})
		}
		out = append(out, slots[which].seg)
		b = b[first+len(slots[which].marker):]
	}
	return out, nil
}

// Lane fills requests for one sending goroutine. It is not safe for
// concurrent use; each lane keeps its own buffers and random source.
type Lane struct {
	s         *Scenario
	rng       *mrand.Rand
	row       []int
	out, body []byte
}

// NewLane returns a lane with its own random source.
func (s *Scenario) NewLane(seed uint64) *Lane {
	return &Lane{s: s, rng: mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), row: make([]int, len(s.tables))}
}

// Next picks a request by weight and fills it. The returned bytes stay valid
// until the next call.
func (l *Lane) Next() (int, []byte) {
	i := 0
	if len(l.s.cum) > 1 {
		i = sort.SearchInts(l.s.cum, l.rng.IntN(l.s.total)+1)
	}
	return i, l.Fill(i)
}

// Fill renders template i with fresh values.
func (l *Lane) Fill(i int) []byte {
	t := l.s.Templates[i]
	for _, ti := range t.tables {
		tab := l.s.tables[ti]
		n := uint64(len(tab.Rows))
		if tab.Order == "random" {
			l.row[ti] = l.rng.IntN(len(tab.Rows))
		} else {
			k := l.s.rows[ti].Add(1) - 1
			l.row[ti] = int((uint64(l.s.pos.Worker) + k*uint64(l.s.pos.Workers)) % n)
		}
	}
	body := l.body[:0]
	if t.bodyDynamic {
		body = l.appendSegs(body, t.body)
		l.body = body
	}
	out := l.out[:0]
	for _, g := range t.head {
		if g.contentLength {
			out = strconv.AppendInt(out, int64(len(body)), 10)
		} else {
			out = l.appendSeg(out, g)
		}
	}
	if t.bodyDynamic {
		out = append(out, body...)
	} else {
		out = l.appendSegs(out, t.body)
	}
	l.out = out
	return out
}

func (l *Lane) appendSegs(dst []byte, segs []seg) []byte {
	for _, g := range segs {
		dst = l.appendSeg(dst, g)
	}
	return dst
}

func (l *Lane) appendSeg(dst []byte, g seg) []byte {
	if g.lit != nil {
		return append(dst, g.lit...)
	}
	switch g.slot {
	case partColumn:
		return append(dst, g.values[l.row[g.table]]...)
	case partRandInt:
		return strconv.AppendInt(dst, g.lo+l.rng.Int64N(g.hi-g.lo+1), 10)
	case partSeq:
		k := l.s.seq.Add(1) - 1
		return strconv.AppendUint(dst, uint64(l.s.pos.Worker)+k*uint64(l.s.pos.Workers), 10)
	case partUUID:
		return appendUUID(dst, l.rng.Uint64(), l.rng.Uint64())
	}
	return dst
}

func encodeColumn(t *Table, k valueKey) []string {
	out := make([]string, len(t.Rows))
	for i, row := range t.Rows {
		v := row[k.col]
		if k.trimLeft {
			v = strings.TrimLeft(v, " \t")
		}
		if k.trimRt {
			v = strings.TrimRight(v, " \t")
		}
		switch k.enc {
		case encPath:
			v = url.PathEscape(v)
		case encQuery:
			v = url.QueryEscape(v)
		}
		out[i] = v
	}
	return out
}

// appendUUID writes a version 4 UUID. It comes from the lane's fast random
// source, so it is unique for testing, not unpredictable.
func appendUUID(dst []byte, a, b uint64) []byte {
	var u [16]byte
	for i := 0; i < 8; i++ {
		u[i], u[8+i] = byte(a>>(8*i)), byte(b>>(8*i))
	}
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	var h [36]byte
	hex.Encode(h[0:8], u[0:4])
	h[8] = '-'
	hex.Encode(h[9:13], u[4:6])
	h[13] = '-'
	hex.Encode(h[14:18], u[6:8])
	h[18] = '-'
	hex.Encode(h[19:23], u[8:10])
	h[23] = '-'
	hex.Encode(h[24:36], u[10:16])
	return append(dst, h[:]...)
}
