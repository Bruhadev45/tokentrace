package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// document is a parsed TOML file: table name -> key -> value. Values are
// string or int64. The root table (keys before any header) is "".
type document map[string]map[string]any

// parseTOML reads the small TOML subset tokentrace's config needs:
// comments, [table] headers, and key = value pairs where the key is bare
// (A-Za-z0-9_-) and the value is a basic string "...", a literal string
// '...' or a decimal integer. Anything else — arrays, inline tables,
// dotted keys, floats, booleans, multi-line strings — is rejected with a
// line-numbered error rather than misread.
func parseTOML(src string) (document, error) {
	if !utf8.ValidString(src) {
		return nil, errors.New("file is not valid UTF-8")
	}
	doc := document{"": {}}
	table := ""
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if err := parseLine(doc, &table, line); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
	}
	return doc, nil
}

func parseLine(doc document, table *string, line string) error {
	if line == "" || line[0] == '#' {
		return nil
	}
	if line[0] == '[' {
		name, err := parseHeader(line)
		if err != nil {
			return err
		}
		if _, dup := doc[name]; dup {
			return fmt.Errorf("table [%s] defined twice", name)
		}
		doc[name] = map[string]any{}
		*table = name
		return nil
	}
	key, rest, ok := strings.Cut(line, "=")
	if !ok {
		return fmt.Errorf("expected key = value, got %q", line)
	}
	key = strings.TrimSpace(key)
	if !isBareKey(key) {
		return fmt.Errorf("unsupported key %q (only bare keys: letters, digits, _ and -)", key)
	}
	val, tail, err := parseValue(strings.TrimSpace(rest))
	if err != nil {
		return fmt.Errorf("key %q: %w", key, err)
	}
	if err := checkTrailing(tail); err != nil {
		return err
	}
	if _, dup := doc[*table][key]; dup {
		return fmt.Errorf("key %q defined twice", key)
	}
	doc[*table][key] = val
	return nil
}

func parseHeader(line string) (string, error) {
	if strings.HasPrefix(line, "[[") {
		return "", errors.New("arrays of tables are not supported")
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return "", fmt.Errorf("unterminated table header %q", line)
	}
	name := strings.TrimSpace(line[1:end])
	if !isBareKey(name) {
		return "", fmt.Errorf("unsupported table name %q (only bare names, no dots)", name)
	}
	return name, checkTrailing(line[end+1:])
}

// checkTrailing accepts only whitespace and an optional comment.
func checkTrailing(s string) error {
	s = strings.TrimSpace(s)
	if s != "" && s[0] != '#' {
		return fmt.Errorf("unexpected text after value: %q", s)
	}
	return nil
}

func isBareKey(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// parseValue parses one value at the start of s and returns the rest.
func parseValue(s string) (any, string, error) {
	switch {
	case s == "":
		return nil, "", errors.New("missing value")
	case strings.HasPrefix(s, `"""`), strings.HasPrefix(s, "'''"):
		return nil, "", errors.New("multi-line strings are not supported")
	case s[0] == '"':
		return parseBasicString(s[1:])
	case s[0] == '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return nil, "", errors.New("unterminated string")
		}
		return s[1 : end+1], s[end+2:], nil
	default:
		return parseInt(s)
	}
}

func parseInt(s string) (any, string, error) {
	end := strings.IndexAny(s, " \t#")
	if end < 0 {
		end = len(s)
	}
	lit := s[:end]
	digits := strings.TrimLeft(lit, "+-")
	if len(lit)-len(digits) > 1 || digits == "" || strings.Trim(digits, "0123456789") != "" {
		return nil, "", fmt.Errorf("unsupported value %q (want a quoted string or an integer)", lit)
	}
	if len(digits) > 1 && digits[0] == '0' {
		return nil, "", fmt.Errorf("integer %q has a leading zero", lit)
	}
	n, err := strconv.ParseInt(lit, 10, 64)
	if err != nil {
		return nil, "", fmt.Errorf("integer %q out of range", lit)
	}
	return n, s[end:], nil
}

var simpleEscapes = map[byte]rune{
	'b': '\b', 't': '\t', 'n': '\n', 'f': '\f', 'r': '\r', '"': '"', '\\': '\\',
}

// parseBasicString decodes a "..." string body (opening quote consumed).
func parseBasicString(s string) (any, string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			return b.String(), s[i+1:], nil
		case c == '\\':
			if i+1 >= len(s) {
				return nil, "", errors.New("unterminated string")
			}
			i++
			if r, ok := simpleEscapes[s[i]]; ok {
				b.WriteRune(r)
				continue
			}
			width := map[byte]int{'u': 4, 'U': 8}[s[i]]
			if width == 0 {
				return nil, "", fmt.Errorf("invalid escape \\%c", s[i])
			}
			r, err := parseUnicodeEscape(s[i+1:], width)
			if err != nil {
				return nil, "", err
			}
			b.WriteRune(r)
			i += width
		case c < 0x20 && c != '\t' || c == 0x7f:
			return nil, "", fmt.Errorf("control character %#x in string", c)
		default:
			b.WriteByte(c)
		}
	}
	return nil, "", errors.New("unterminated string")
}

func parseUnicodeEscape(s string, width int) (rune, error) {
	if len(s) < width {
		return 0, errors.New("short unicode escape")
	}
	n, err := strconv.ParseUint(s[:width], 16, 32)
	if err != nil || !utf8.ValidRune(rune(n)) {
		return 0, fmt.Errorf("invalid unicode escape %q", s[:width])
	}
	return rune(n), nil
}
