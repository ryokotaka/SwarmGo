package scenario

import (
	"fmt"
	"strconv"
	"strings"
)

type partKind int

const (
	partLiteral partKind = iota
	partColumn           // {{source.column}}
	partEnv              // {{env.NAME}}, resolved by Load
	partRandInt          // {{random.int(lo,hi)}}
	partUUID             // {{random.uuid}}
	partSeq              // {{seq}}
)

type part struct {
	kind           partKind
	text           string // Literal text.
	source, column string // Column source and name; for env, source is the name.
	lo, hi         int64
}

// parse splits s into literal text and variables. "{{{{" is a literal "{{".
func parse(s string) ([]part, error) {
	var parts []part
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, part{kind: partLiteral, text: lit.String()})
			lit.Reset()
		}
	}
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			lit.WriteString(s)
			break
		}
		lit.WriteString(s[:i])
		s = s[i:]
		if strings.HasPrefix(s, "{{{{") {
			lit.WriteString("{{")
			s = s[4:]
			continue
		}
		end := strings.Index(s, "}}")
		if end < 0 {
			return nil, fmt.Errorf("unclosed {{ in %q", s)
		}
		p, err := parseVar(strings.TrimSpace(s[2:end]))
		if err != nil {
			return nil, err
		}
		flush()
		parts = append(parts, p)
		s = s[end+2:]
	}
	flush()
	return parts, nil
}

func parseVar(expr string) (part, error) {
	switch {
	case expr == "seq":
		return part{kind: partSeq}, nil
	case expr == "random.uuid":
		return part{kind: partUUID}, nil
	case strings.HasPrefix(expr, "random.int(") && strings.HasSuffix(expr, ")"):
		lo, hi, ok := strings.Cut(expr[len("random.int("):len(expr)-1], ",")
		a, errA := strconv.ParseInt(strings.TrimSpace(lo), 10, 64)
		b, errB := strconv.ParseInt(strings.TrimSpace(hi), 10, 64)
		if !ok || errA != nil || errB != nil || a > b || b-a+1 <= 0 {
			return part{}, fmt.Errorf("{{%s}}: want random.int(low,high) with low <= high and a range that fits int64", expr)
		}
		return part{kind: partRandInt, lo: a, hi: b}, nil
	}
	source, column, ok := strings.Cut(expr, ".")
	if !ok || !validName(source) || !validName(column) {
		return part{}, fmt.Errorf("{{%s}}: want env.NAME, source.column, random.int(a,b), random.uuid or seq", expr)
	}
	if source == "env" {
		return part{kind: partEnv, source: column}, nil
	}
	if source == "random" {
		return part{}, fmt.Errorf("{{%s}}: unknown random function", expr)
	}
	return part{kind: partColumn, source: source, column: column}, nil
}

// replaceEnv substitutes {{env.NAME}} and leaves every other variable in place.
// An environment value is escaped so that a "{{" inside it stays literal.
func replaceEnv(s string, getenv func(string) (string, bool)) (string, error) {
	parts, err := parse(s)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		switch p.kind {
		case partLiteral:
			b.WriteString(strings.ReplaceAll(p.text, "{{", "{{{{"))
		case partEnv:
			v, ok := getenv(p.source)
			if !ok {
				return "", fmt.Errorf("environment variable %s is not set", p.source)
			}
			b.WriteString(strings.ReplaceAll(v, "{{", "{{{{"))
		default:
			b.WriteString("{{" + p.expr() + "}}")
		}
	}
	return b.String(), nil
}

func (p part) expr() string {
	switch p.kind {
	case partColumn:
		return p.source + "." + p.column
	case partRandInt:
		return fmt.Sprintf("random.int(%d,%d)", p.lo, p.hi)
	case partUUID:
		return "random.uuid"
	case partSeq:
		return "seq"
	}
	return "env." + p.source
}
