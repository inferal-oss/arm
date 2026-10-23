package providerproto

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type node struct {
	kind   byte
	text   string
	object map[string]node
	array  []node
}
type parser struct {
	raw       string
	at, nodes int
}

func parse(raw []byte, maximum int) (node, error) {
	if len(raw) == 0 || len(raw) > maximum {
		return node{}, fmt.Errorf("provider JSON byte limit")
	}
	owned := string(raw)
	if !utf8.ValidString(owned) {
		return node{}, fmt.Errorf("invalid provider UTF-8")
	}
	p := parser{raw: owned}
	n, e := p.value(1)
	if e != nil {
		return node{}, e
	}
	p.space()
	if p.at != len(p.raw) {
		return node{}, fmt.Errorf("trailing provider JSON")
	}
	return n, nil
}
func (p *parser) space() {
	for p.at < len(p.raw) && strings.ContainsRune(" \t\r\n", rune(p.raw[p.at])) {
		p.at++
	}
}
func (p *parser) count() error {
	if p.nodes >= MaxNodes {
		return fmt.Errorf("provider JSON node limit")
	}
	p.nodes++
	return nil
}
func (p *parser) value(depth int) (node, error) {
	if depth > MaxDepth {
		return node{}, fmt.Errorf("provider JSON depth limit")
	}
	if e := p.count(); e != nil {
		return node{}, e
	}
	p.space()
	if p.at >= len(p.raw) {
		return node{}, fmt.Errorf("incomplete provider JSON")
	}
	switch p.raw[p.at] {
	case '"':
		s, e := p.string()
		return node{kind: 's', text: s}, e
	case '{':
		p.at++
		p.space()
		n := node{kind: 'o', object: map[string]node{}}
		if p.take('}') {
			return n, nil
		}
		for {
			if len(n.object) >= MaxMembers {
				return node{}, fmt.Errorf("provider JSON member limit")
			}
			if e := p.count(); e != nil {
				return node{}, e
			}
			p.space()
			k, e := p.string()
			if e != nil {
				return node{}, e
			}
			if _, ok := n.object[k]; ok {
				return node{}, fmt.Errorf("duplicate decoded provider key")
			}
			p.space()
			if !p.take(':') {
				return node{}, fmt.Errorf("provider object colon required")
			}
			v, e := p.value(depth + 1)
			if e != nil {
				return node{}, e
			}
			n.object[k] = v
			p.space()
			if p.take('}') {
				return n, nil
			}
			if !p.take(',') {
				return node{}, fmt.Errorf("provider object comma required")
			}
		}
	case '[':
		p.at++
		p.space()
		n := node{kind: 'a', array: []node{}}
		if p.take(']') {
			return n, nil
		}
		for {
			if len(n.array) >= MaxArray {
				return node{}, fmt.Errorf("provider JSON array limit")
			}
			v, e := p.value(depth + 1)
			if e != nil {
				return node{}, e
			}
			n.array = append(n.array, v)
			p.space()
			if p.take(']') {
				return n, nil
			}
			if !p.take(',') {
				return node{}, fmt.Errorf("provider array comma required")
			}
		}
	case 't', 'f', 'n':
		for _, s := range []string{"true", "false", "null"} {
			if strings.HasPrefix(p.raw[p.at:], s) {
				p.at += len(s)
				return node{kind: s[0], text: s}, nil
			}
		}
		return node{}, fmt.Errorf("invalid provider literal")
	default:
		start := p.at
		if p.take('-') && p.at >= len(p.raw) {
			return node{}, fmt.Errorf("invalid provider number")
		}
		if p.at >= len(p.raw) {
			return node{}, fmt.Errorf("invalid provider value")
		}
		if p.take('0') {
		} else {
			if p.raw[p.at] < '1' || p.raw[p.at] > '9' {
				return node{}, fmt.Errorf("invalid provider value")
			}
			for p.at < len(p.raw) && p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
				p.at++
			}
		}
		if p.take('.') {
			from := p.at
			for p.at < len(p.raw) && p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
				p.at++
			}
			if from == p.at {
				return node{}, fmt.Errorf("invalid provider fraction")
			}
		}
		if p.at < len(p.raw) && (p.raw[p.at] == 'e' || p.raw[p.at] == 'E') {
			p.at++
			if !p.take('+') {
				p.take('-')
			}
			from := p.at
			for p.at < len(p.raw) && p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
				p.at++
			}
			if from == p.at {
				return node{}, fmt.Errorf("invalid provider exponent")
			}
		}
		return node{kind: 'd', text: p.raw[start:p.at]}, nil
	}
}
func (p *parser) take(c byte) bool {
	if p.at < len(p.raw) && p.raw[p.at] == c {
		p.at++
		return true
	}
	return false
}
func (p *parser) hex4() (uint16, error) {
	if len(p.raw)-p.at < 4 {
		return 0, fmt.Errorf("short Unicode escape")
	}
	var v uint16
	for range 4 {
		c := p.raw[p.at]
		p.at++
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v += uint16(c - '0')
		case c >= 'a' && c <= 'f':
			v += uint16(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			v += uint16(c - 'A' + 10)
		default:
			return 0, fmt.Errorf("invalid Unicode escape")
		}
	}
	return v, nil
}
func (p *parser) string() (string, error) {
	if !p.take('"') {
		return "", fmt.Errorf("provider string required")
	}
	var b strings.Builder
	for p.at < len(p.raw) {
		c := p.raw[p.at]
		p.at++
		if c == '"' {
			return b.String(), nil
		}
		if c < 0x20 {
			return "", fmt.Errorf("unescaped provider control")
		}
		var s string
		if c != '\\' {
			if c < utf8.RuneSelf {
				s = string(c)
			} else {
				p.at--
				_, size := utf8.DecodeRuneInString(p.raw[p.at:])
				s = p.raw[p.at : p.at+size]
				p.at += size
			}
		} else {
			if p.at >= len(p.raw) {
				return "", fmt.Errorf("short provider escape")
			}
			e := p.raw[p.at]
			p.at++
			switch e {
			case '"', '\\', '/':
				s = string(e)
			case 'b':
				s = "\b"
			case 't':
				s = "\t"
			case 'n':
				s = "\n"
			case 'f':
				s = "\f"
			case 'r':
				s = "\r"
			case 'u':
				v, err := p.hex4()
				if err != nil {
					return "", err
				}
				r := rune(v)
				if v >= 0xd800 && v <= 0xdbff {
					if !p.take('\\') || !p.take('u') {
						return "", fmt.Errorf("unpaired provider surrogate")
					}
					low, err := p.hex4()
					if err != nil || low < 0xdc00 || low > 0xdfff {
						return "", fmt.Errorf("unpaired provider surrogate")
					}
					r = utf16.DecodeRune(r, rune(low))
				} else if v >= 0xdc00 && v <= 0xdfff {
					return "", fmt.Errorf("unpaired provider surrogate")
				}
				if r == 0 {
					return "", fmt.Errorf("provider NUL refused")
				}
				s = string(r)
			default:
				return "", fmt.Errorf("invalid provider escape")
			}
		}
		if len(s) > MaxStringBytes-b.Len() {
			return "", fmt.Errorf("provider decoded string byte limit")
		}
		b.WriteString(s)
	}
	return "", fmt.Errorf("unclosed provider string")
}
func canonical(n node) []byte { var b strings.Builder; writeNode(&b, n); return []byte(b.String()) }
func writeNode(b *strings.Builder, n node) {
	switch n.kind {
	case 's':
		writeString(b, n.text)
	case 'o':
		b.WriteByte('{')
		keys := make([]string, 0, len(n.object))
		for k := range n.object {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, k)
			b.WriteByte(':')
			writeNode(b, n.object[k])
		}
		b.WriteByte('}')
	case 'a':
		b.WriteByte('[')
		for i, v := range n.array {
			if i > 0 {
				b.WriteByte(',')
			}
			writeNode(b, v)
		}
		b.WriteByte(']')
	default:
		b.WriteString(n.text)
	}
}
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 32 {
				b.WriteString(`\u`)
				b.WriteString(fmt.Sprintf("%04x", r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
func dataNode(n node) (Data, error) {
	var walk func(node) bool
	walk = func(v node) bool {
		if v.kind == 'd' {
			return false
		}
		for _, x := range v.object {
			if !walk(x) {
				return false
			}
		}
		for _, x := range v.array {
			if !walk(x) {
				return false
			}
		}
		return true
	}
	if !walk(n) {
		return Data{}, fmt.Errorf("provider data cannot contain JSON numbers")
	}
	b := canonical(n)
	if len(b) > MaxDataBytes {
		return Data{}, fmt.Errorf("provider canonical data byte limit")
	}
	return Data{canonical: string(b)}, nil
}
func uintValue(s string) (uint64, error) {
	if s == "" || len(s) > 19 || len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("invalid provider integer")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid provider integer")
		}
	}
	v, e := strconv.ParseUint(s, 10, 63)
	if e != nil {
		return 0, fmt.Errorf("provider integer out of range")
	}
	return v, nil
}
