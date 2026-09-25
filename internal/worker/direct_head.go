package worker

import (
	"bytes"
	"io"

	"github.com/valyala/fasthttp"
)

// responseHead is the part of a response header the direct path acts on.
// length follows fasthttp: -1 is chunked and -2 is read-until-close.
type responseHead struct {
	status int
	length int
	gzip   bool // Content-Encoding is exactly gzip (ignoring case).
	close  bool // Connection lists close.
}

// tokenByte marks RFC 9110 tchar bytes, valid in header field names.
// valueByte marks bytes allowed in a field value: VCHAR, SP, HTAB and obs-text.
var tokenByte, valueByte [256]bool

func init() {
	for c := 0; c < 256; c++ {
		valueByte[c] = c == '\t' || (c >= ' ' && c != 0x7f)
		tokenByte[c] = c > ' ' && c < 0x7f && !bytes.ContainsRune([]byte(`"(),/:;<=>?@[\]{}`), rune(c))
	}
}

// parseHead handles the common response form without copying: an HTTP/1.1
// final, non-redirect status, CRLF lines, and framing by one Content-Length or
// exactly "chunked". It returns the header length including the blank line, or
// ok=false for anything else, in which case the caller uses fasthttp's full
// parser on the same unconsumed bytes. Only fields that change framing,
// decoding or reuse are interpreted; every field is still checked for valid
// name and value bytes.
func parseHead(b []byte) (head responseHead, size int, ok bool) {
	// "HTTP/1.1 200\r\n" is the shortest status line.
	line := bytes.IndexByte(b, '\n')
	if line < 13 || !bytes.HasPrefix(b, []byte("HTTP/1.1 ")) || b[line-1] != '\r' || (line > 13 && b[12] != ' ') {
		return head, 0, false
	}
	d0, d1, d2 := b[9], b[10], b[11]
	if d0 < '2' || d0 > '5' || d1 < '0' || d1 > '9' || d2 < '0' || d2 > '9' {
		return head, 0, false
	}
	head.status = int(d0-'0')*100 + int(d1-'0')*10 + int(d2-'0')
	if isRedirect(head.status) {
		return head, 0, false // Redirects need the complete header.
	}
	for _, c := range b[12 : line-1] {
		if !valueByte[c] {
			return head, 0, false
		}
	}
	head.length = -2
	var sawLength, chunked bool
	pos := line + 1
	for {
		rest := b[pos:]
		line = bytes.IndexByte(rest, '\n')
		if line < 1 || rest[line-1] != '\r' {
			return head, 0, false // Incomplete header or a bare LF.
		}
		if line == 1 {
			pos += 2 // The blank line ends the header.
			break
		}
		field := rest[:line-1]
		pos += line + 1
		colon := bytes.IndexByte(field, ':')
		if colon < 1 {
			return head, 0, false // Also rejects obs-fold continuation lines.
		}
		name, value := field[:colon], field[colon+1:]
		for _, c := range name {
			if !tokenByte[c] {
				return head, 0, false
			}
		}
		for _, c := range value {
			if !valueByte[c] {
				return head, 0, false
			}
		}
		for len(value) > 0 && (value[0] == ' ' || value[0] == '\t') {
			value = value[1:]
		}
		for len(value) > 0 && (value[len(value)-1] == ' ' || value[len(value)-1] == '\t') {
			value = value[:len(value)-1]
		}
		// Most fields are none of these; the length check skips the comparison.
		switch len(name) {
		case len("Content-Length"):
			if !bytes.EqualFold(name, []byte("Content-Length")) {
				continue
			}
			if sawLength || len(value) == 0 || len(value) > 18 {
				return head, 0, false
			}
			sawLength = true
			n := 0
			for _, c := range value {
				if c < '0' || c > '9' {
					return head, 0, false
				}
				n = n*10 + int(c-'0')
			}
			head.length = n
		case len("Transfer-Encoding"):
			if !bytes.EqualFold(name, []byte("Transfer-Encoding")) {
				continue
			}
			if chunked || !bytes.EqualFold(value, []byte("chunked")) {
				return head, 0, false
			}
			chunked = true
		case len("Content-Encoding"):
			if bytes.EqualFold(name, []byte("Content-Encoding")) {
				head.gzip = bytes.EqualFold(value, []byte("gzip"))
			}
		case len("Connection"):
			if !bytes.EqualFold(name, []byte("Connection")) {
				continue
			}
			// Match the full parser: the last field wins and only an exact
			// "close" closes. Token lists and other values take the slow path.
			switch {
			case string(value) == "close":
				head.close = true
			case bytes.EqualFold(value, []byte("keep-alive")):
				head.close = false
			default:
				return head, 0, false
			}
		case len("Trailer"):
			if bytes.EqualFold(name, []byte("Trailer")) {
				return head, 0, false // The full parser validates declared trailers.
			}
		}
	}
	if chunked {
		if sawLength {
			return head, 0, false
		}
		head.length = -1
	} else if !sawLength {
		return head, 0, false // Read-until-close responses are rare; keep them on fasthttp.
	}
	return head, pos, true
}

// fastHead parses the next response header from already buffered bytes.
// It reads from the connection only when nothing is buffered, exactly as the
// full parser would, and never consumes bytes unless it succeeds. done=false
// means the full parser must read this header.
func (l *directLane) fastHead() (done bool, err error) {
	if l.reader.Buffered() == 0 {
		if _, err := l.reader.Peek(1); err != nil {
			// Report the first-byte error as fasthttp's ResponseHeader.Read
			// does, so timeouts and the idle-socket retry behave the same.
			if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
				return true, fasthttp.ErrTimeout
			}
			return true, io.EOF
		}
	}
	buffered, _ := l.reader.Peek(l.reader.Buffered())
	head, size, ok := parseHead(buffered)
	if !ok {
		return false, nil
	}
	l.reader.Discard(size)
	l.head = head
	return true, nil
}

// fastEmptyTrailer consumes the empty trailer section that ends nearly every
// chunked body, avoiding the full trailer parser.
func (l *directLane) fastEmptyTrailer() bool {
	b, err := l.reader.Peek(2)
	if err != nil || b[0] != '\r' || b[1] != '\n' {
		return false
	}
	_, err = l.reader.Discard(2)
	return err == nil
}
