package worker

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
	"golang.org/x/net/idna"
)

// directTransport retains idle connections between runs. During a run each
// connection belongs to one lane; there is no shared pool on the request path.
// RoundTrip keeps custom clients and redirects on the standard transport.
type directTransport struct {
	base *http.Transport
	mu   sync.Mutex
	idle []idleConnection
}

type idleConnection struct {
	conn   net.Conn
	reader *bufio.Reader
	key    string
	since  time.Time
}

func (t *directTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req)
}

func (t *directTransport) CloseIdleConnections() {
	t.mu.Lock()
	idle := t.idle
	t.idle = nil
	t.mu.Unlock()
	for _, c := range idle {
		c.conn.Close()
	}
	t.base.CloseIdleConnections()
}

func (t *directTransport) take(key string) idleConnection {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.idle) > 0 {
		i := len(t.idle) - 1
		c := t.idle[i]
		t.idle[i] = idleConnection{}
		t.idle = t.idle[:i]
		if c.key == key && time.Since(c.since) < t.base.IdleConnTimeout {
			return c
		}
		c.conn.Close()
	}
	return idleConnection{}
}

func (t *directTransport) put(c idleConnection) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.idle) >= t.base.MaxIdleConns {
		c.conn.Close()
		return
	}
	c.since = time.Now()
	t.idle = append(t.idle, c)
}

type directPlan struct {
	transport      *directTransport
	template       *http.Request
	wire           []byte
	address, key   string
	tlsConfig      *tls.Config
	timeout        time.Duration
	gzip           bool
	replayable     bool // Idempotent: may be resent when a reused socket was closed.
	closeAfter     bool // The request itself asks to close the connection.
	maxHeaderBytes int
	fallback       *http.Client
}

func (r *MyRunner) directPlan(template *http.Request) (*directPlan, error) {
	t, ok := r.MyClient.Transport.(*directTransport)
	if !ok || r.MyClient.Jar != nil || r.MyClient.CheckRedirect != nil ||
		template.Method == http.MethodHead || template.Method == http.MethodConnect || template.Header.Get("Upgrade") != "" ||
		template.Header.Get("Expect") != "" {
		return nil, nil
	}
	req := template.Clone(context.Background())
	if template.GetBody != nil {
		var err error
		req.Body, err = template.GetBody()
		if err != nil {
			return nil, err
		}
		defer req.Body.Close()
	}
	if req.URL.User != nil && req.Header.Get("Authorization") == "" {
		password, _ := req.URL.User.Password()
		req.SetBasicAuth(req.URL.User.Username(), password)
	}
	automaticGzip := req.Header.Get("Accept-Encoding") == "" && req.Header.Get("Range") == "" && req.Method != http.MethodHead && !t.base.DisableCompression
	if automaticGzip {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	var wire bytes.Buffer
	if err := req.Write(&wire); err != nil {
		return nil, err
	}
	host := req.URL.Hostname()
	if net.ParseIP(strings.Split(host, "%")[0]) == nil {
		var err error
		host, err = idna.Lookup.ToASCII(host)
		if err != nil {
			return nil, err
		}
	}
	port := req.URL.Port()
	if port == "" {
		port = "80"
		if req.URL.Scheme == "https" {
			port = "443"
		}
	}
	p := &directPlan{transport: t, template: template, wire: wire.Bytes(), address: net.JoinHostPort(host, port), timeout: r.MyClient.Timeout, gzip: automaticGzip}
	p.key = req.URL.Scheme + "://" + p.address
	// Decided once per run instead of once per request.
	switch template.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		p.replayable = true
	}
	p.closeAfter = template.Close || strings.EqualFold(template.Header.Get("Connection"), "close")
	p.maxHeaderBytes = int(t.base.MaxResponseHeaderBytes)
	if p.maxHeaderBytes <= 0 {
		p.maxHeaderBytes = 10 << 20
	}
	if req.URL.Scheme == "https" {
		p.tlsConfig = t.base.TLSClientConfig.Clone()
		if p.tlsConfig.ServerName == "" {
			p.tlsConfig.ServerName = host
		}
		p.tlsConfig.NextProtos = []string{"http/1.1"}
	}
	c := *r.MyClient
	c.Transport = t.base
	p.fallback = &c
	return p, nil
}

// A run cancellation callback may close the socket, but only the lane touches
// its reader/parser. The lock is used when a connection changes, not per request.
type directLane struct {
	standard bool
	plan     *directPlan
	ctx      context.Context
	conn     net.Conn
	reader   *bufio.Reader
	header   fasthttp.ResponseHeader // Only valid when the full parser read the last header.
	head     responseHead
	unzip    *gzip.Reader
	mu       sync.Mutex
	watched  net.Conn
	canceled bool
	stop     func() bool
}

func (p *directPlan) lane(ctx context.Context) *directLane {
	l := &directLane{plan: p, ctx: ctx}
	idle := p.transport.take(p.key)
	l.conn, l.reader, l.watched = idle.conn, idle.reader, idle.conn
	l.stop = context.AfterFunc(ctx, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.canceled = true
		if l.watched != nil {
			l.watched.Close()
		}
	})
	return l
}

func (l *directLane) closeConn() {
	l.mu.Lock()
	if l.conn != nil {
		l.conn.Close()
	}
	l.watched = nil
	l.mu.Unlock()
	l.conn = nil
	if l.reader != nil {
		l.reader.Reset(nil)
	}
}

func (l *directLane) finish() {
	stopped := l.stop()
	l.mu.Lock()
	c := l.conn
	reusable := stopped && !l.canceled && l.ctx.Err() == nil
	l.watched = nil
	l.mu.Unlock()
	if c != nil {
		if reusable {
			c.SetDeadline(time.Time{})
			l.plan.transport.put(idleConnection{conn: c, reader: l.reader, key: l.plan.key})
		} else {
			c.Close()
		}
	}
}

func (l *directLane) connect(deadline time.Time) error {
	dialCtx := l.ctx
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithDeadline(dialCtx, deadline)
		defer cancel()
	}
	conn, err := (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(dialCtx, "tcp", l.plan.address)
	if err != nil {
		return err
	}
	l.mu.Lock()
	if l.canceled || dialCtx.Err() != nil {
		l.mu.Unlock()
		conn.Close()
		return context.Canceled
	}
	l.watched = conn
	l.mu.Unlock()
	if l.plan.tlsConfig != nil {
		tlsConn := tls.Client(conn, l.plan.tlsConfig)
		if err = tlsConn.HandshakeContext(dialCtx); err != nil {
			conn.Close()
			l.mu.Lock()
			l.watched = nil
			l.mu.Unlock()
			return err
		}
		conn = tlsConn
	}
	l.conn = conn
	if l.reader == nil {
		l.reader = bufio.NewReaderSize(conn, 4096)
	} else {
		l.reader.Reset(conn)
	}
	return nil
}

func (l *directLane) readHeader(trailer bool) error {
	if trailer && l.fastEmptyTrailer() {
		return nil
	}
	if !trailer {
		if done, err := l.fastHead(); done {
			return err
		}
	}
	for {
		var err error
		if trailer {
			err = l.header.ReadTrailer(l.reader)
		} else {
			err = l.header.Read(l.reader)
			if err == nil {
				l.head = responseHead{
					status: l.header.StatusCode(), length: l.header.ContentLength(),
					gzip:  bytes.EqualFold(l.header.Peek("Content-Encoding"), []byte("gzip")),
					close: l.header.ConnectionClose(),
				}
			}
		}
		// Checked only on error: errors.As moves its target to the heap, which
		// would otherwise cost an allocation on every response.
		if err == nil {
			return nil
		}
		var small *fasthttp.ErrSmallBuffer
		if !errors.As(err, &small) {
			return err
		}
		size := l.reader.Size()
		if size >= l.plan.maxHeaderBytes {
			return fmt.Errorf("response headers exceed %d bytes", l.plan.maxHeaderBytes)
		}
		// Header.Read leaves an incomplete header buffered when it needs space.
		b, _ := l.reader.Peek(l.reader.Buffered())
		prefix := bytes.Clone(b)
		l.reader = bufio.NewReaderSize(io.MultiReader(bytes.NewReader(prefix), l.conn), min(size*2, l.plan.maxHeaderBytes))
	}
}

func (l *directLane) drainBody() error {
	code := l.head.status
	if l.plan.template.Method == http.MethodHead || code == 204 || code == 304 {
		return nil
	}
	length := l.head.length
	if code == 101 {
		length = -2
	}
	// Most API responses have a known length and need no allocation or copy.
	decompress := l.plan.gzip && l.head.gzip
	if length >= 0 && !decompress {
		_, err := l.reader.Discard(length)
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	var body io.Reader = l.reader
	var limited *io.LimitedReader
	if length >= 0 {
		limited = &io.LimitedReader{R: l.reader, N: int64(length)}
		body = limited
	}
	if length == -1 {
		body = httputil.NewChunkedReader(l.reader)
	}
	source := body
	if decompress {
		var err error
		if l.unzip == nil {
			l.unzip, err = gzip.NewReader(body)
		} else {
			err = l.unzip.Reset(body)
		}
		if err != nil {
			return err
		}
		body = l.unzip
	}
	_, err := io.Copy(io.Discard, body)
	if decompress {
		l.unzip.Close()
		if err == nil {
			_, err = io.Copy(io.Discard, source)
		}
	}
	if err != nil {
		return err
	}
	if limited != nil && limited.N != 0 {
		return io.ErrUnexpectedEOF
	}
	if length == -1 {
		err = l.readHeader(true)
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
	}
	return err
}

func (l *directLane) execute() MyResult {
	if l.standard {
		return (&MyRunner{MyClient: l.plan.fallback}).executeRequest(l.ctx, l.plan.template)
	}
	return l.executeAttempt(time.Now(), true)
}

func (l *directLane) executeAttempt(start time.Time, retry bool) (result MyResult) {
	defer func() { result.MyDuration = time.Since(start) }()
	deadline, _ := l.ctx.Deadline()
	if l.plan.timeout > 0 {
		d := start.Add(l.plan.timeout)
		if deadline.IsZero() || d.Before(deadline) {
			deadline = d
		}
	}
	if err := l.ctx.Err(); err != nil {
		result.MyErr = err
		return
	}
	reused := l.conn != nil
	if l.conn == nil {
		if err := l.connect(deadline); err != nil {
			result.MyErr = err
			return
		}
	}
	err := l.conn.SetDeadline(deadline)
	if err == nil {
		remaining := l.plan.wire
		for len(remaining) > 0 {
			var n int
			n, err = l.conn.Write(remaining)
			remaining = remaining[n:]
			if err != nil {
				break
			}
			if n == 0 {
				err = io.ErrShortWrite
				break
			}
		}
	}
	if err == nil {
		for interim := 0; ; interim++ {
			err = l.readHeader(false)
			if err != nil {
				break
			}
			code := l.head.status
			if code < 100 || code >= 200 || code == 101 {
				break
			}
			if interim >= 100 {
				err = errors.New("too many informational responses")
				break
			}
		}
	}
	if err != nil {
		// A server can close an idle keep-alive socket without advertising it.
		// Only replay idempotent requests when no response header was received.
		again := retry && reused && l.plan.replayable && err == io.EOF && l.ctx.Err() == nil
		l.closeConn()
		if again {
			return l.executeAttempt(start, false)
		}
		if l.ctx.Err() != nil {
			err = l.ctx.Err()
		}
		result.MyErr = err
		return
	}
	result.MyStatusCode = l.head.status

	// parseHead leaves redirects to the full parser, so l.header is current here.
	if isRedirect(result.MyStatusCode) && len(l.header.Peek("Location")) > 0 {
		// Give Client.Do the already received response so it applies Go's
		// redirect method/auth rules without sending the original POST twice.
		header := make(http.Header)
		l.header.VisitAll(func(k, v []byte) { header.Add(string(k), string(v)) })
		response := &http.Response{StatusCode: result.MyStatusCode, Status: fmt.Sprintf("%d %s", result.MyStatusCode, http.StatusText(result.MyStatusCode)), Header: header, Body: http.NoBody}
		// Following runs in this lane use the standard pool as well, avoiding
		// a fresh direct connection for every redirected request.
		l.standard = true
		l.closeConn()
		ctx := l.ctx
		if !deadline.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, deadline)
			defer cancel()
		}
		c := *l.plan.fallback
		c.Timeout = 0 // The original request's deadline covers the whole chain.
		c.Transport = &redirectTransport{first: response, next: c.Transport}
		fallback := &MyRunner{MyClient: &c}
		return fallback.executeRequest(ctx, l.plan.template)
	}
	if err = l.drainBody(); err != nil {
		l.closeConn()
		if l.ctx.Err() != nil {
			err = l.ctx.Err()
		}
		result.MyErr = fmt.Errorf("read response body: %w", err)
		return
	}
	result.ResponseComplete = true
	if l.head.close || (l.head.length == -2 && l.plan.template.Method != http.MethodHead && result.MyStatusCode != 204 && result.MyStatusCode != 304) || result.MyStatusCode == 101 || l.plan.closeAfter {
		l.closeConn()
	}
	if result.MyStatusCode >= 400 {
		result.MyErr = fmt.Errorf("HTTP %d %s", result.MyStatusCode, http.StatusText(result.MyStatusCode))
	}
	return
}

func isRedirect(code int) bool {
	return code == 301 || code == 302 || code == 303 || code == 307 || code == 308
}

type redirectTransport struct {
	first *http.Response
	next  http.RoundTripper
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.first != nil {
		response := t.first
		t.first = nil
		response.Request = req
		if req.Body != nil {
			req.Body.Close()
		}
		return response, nil
	}
	return t.next.RoundTrip(req)
}
