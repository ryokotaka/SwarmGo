package worker

import (
	"bufio"
	"bytes"
	"testing"

	"github.com/valyala/fasthttp"
)

// checkHeadMatchesFastHTTP requires every header accepted by the fast path to
// be read identically by fasthttp's full parser, including the bytes consumed.
func checkHeadMatchesFastHTTP(t *testing.T, input []byte) (accepted bool) {
	t.Helper()
	head, size, ok := parseHead(input)
	if !ok {
		return false
	}
	var full fasthttp.ResponseHeader
	reader := bufio.NewReaderSize(bytes.NewReader(input), len(input)+16)
	if err := full.Read(reader); err != nil {
		t.Fatalf("fast path accepted a header the full parser rejects (%v): %q", err, input)
	}
	want := responseHead{
		status: full.StatusCode(), length: full.ContentLength(),
		gzip:  bytes.EqualFold(full.Peek("Content-Encoding"), []byte("gzip")),
		close: full.ConnectionClose(),
	}
	if head != want || size != len(input)-reader.Buffered() {
		t.Fatalf("fast path read %+v (%d bytes), full parser %+v (%d bytes): %q",
			head, size, want, len(input)-reader.Buffered(), input)
	}
	return true
}

func TestParseHeadMatchesFullParser(t *testing.T) {
	for _, tc := range []struct {
		response string
		fast     bool
	}{
		{"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello", true},
		{"HTTP/1.1 200\r\nContent-Length: 0\r\n\r\n", true},
		{"HTTP/1.1 404 Not Found\r\ncontent-length:  3 \r\nConnection: close\r\n\r\nabc", true},
		{"HTTP/1.1 200 OK\r\nConnection: close\r\nConnection: Keep-Alive\r\nContent-Length: 0\r\n\r\n", true},
		{"HTTP/1.1 500 Internal Server Error\r\nTransfer-Encoding: Chunked\r\nContent-Encoding: GZIP\r\n\r\n0\r\n\r\n", true},
		{"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n", true},
		{"HTTP/1.1 200 OK\r\nX-Empty:\r\nContent-Length: 1\r\n\r\nx", true},
		// Everything below is left to the full parser.
		{"HTTP/1.0 200 OK\r\nContent-Length: 1\r\n\r\nx", false},
		{"HTTP/1.1 100 Continue\r\n\r\n", false},
		{"HTTP/1.1 302 Found\r\nLocation: /x\r\nContent-Length: 0\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 1\r\n\r\nx", false},
		{"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip, chunked\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\nContent-Length: -1\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\nX-Folded: a\r\n b\r\nContent-Length: 0\r\n\r\n", false},
		{"HTTP/1.1 200 OK\nContent-Length: 0\n\n", false},
		{"HTTP/1.1 200 OK\r\nBad Name: x\r\nContent-Length: 0\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\nX-Control: a\x00b\r\nContent-Length: 0\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\nConnection: keep-alive, close\r\nContent-Length: 0\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\nTrailer: X-Sum\r\nTransfer-Encoding: chunked\r\n\r\n", false},
		{"HTTP/1.1 2000 OK\r\nContent-Length: 0\r\n\r\n", false},
		{"HTTP/1.1 200 OK\r\nContent-Length: 1", false}, // Incomplete header.
	} {
		if got := checkHeadMatchesFastHTTP(t, []byte(tc.response)); got != tc.fast {
			t.Errorf("fast path accepted=%v, want %v: %q", got, tc.fast, tc.response)
		}
	}
}

func FuzzParseHead(f *testing.F) {
	f.Add([]byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"))
	f.Add([]byte("HTTP/1.1 503 Service Unavailable\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n"))
	f.Add([]byte("HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nContent-Length: 0\r\n\r\n"))
	f.Add([]byte("HTTP/1.1 404 Not Found\r\nX-A: b\r\nConnection: keep-alive\r\nContent-Length: 12\r\n\r\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		checkHeadMatchesFastHTTP(t, input)
	})
}
