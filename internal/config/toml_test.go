package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseTOML(t *testing.T) {
	src := "# leading comment\r\n" +
		"[upstreams]\n" +
		"  gateway = \"https://gw.example.com\"   # trailing comment\n" +
		"raw-lit = 'C:\\path\\no\\escapes'\n" +
		"\n" +
		"[ defaults ]\n" +
		"port = 8080\n" +
		"neg = -12\n" +
		"plus = +7\n" +
		"zero = 0\n" +
		"esc = \"tab\\there \\\"q\\\" \\\\ \\u00e9 \\U0001F600\"\n" +
		"hash = \"a # not a comment\"\n"
	got, err := parseTOML(src)
	if err != nil {
		t.Fatalf("parseTOML error = %v", err)
	}
	want := document{
		"": {},
		"upstreams": {
			"gateway": "https://gw.example.com",
			"raw-lit": `C:\path\no\escapes`,
		},
		"defaults": {
			"port": int64(8080), "neg": int64(-12), "plus": int64(7), "zero": int64(0),
			"esc":  "tab\there \"q\" \\ é 😀",
			"hash": "a # not a comment",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTOML =\n%#v\nwant\n%#v", got, want)
	}
}

func TestParseTOMLErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"no equals", "[a]\njust words", "line 2: expected key = value"},
		{"missing value", "k =", "missing value"},
		{"dotted key", "a.b = 1", "unsupported key"},
		{"quoted key", `"k" = 1`, "unsupported key"},
		{"dotted table", "[a.b]", "unsupported table name"},
		{"array of tables", "[[a]]", "arrays of tables"},
		{"unterminated header", "[a", "unterminated table header"},
		{"junk after header", "[a] x", "unexpected text"},
		{"duplicate table", "[a]\n[a]", "line 2: table [a] defined twice"},
		{"duplicate key", "k = 1\nk = 2", "key \"k\" defined twice"},
		{"unterminated basic", `k = "abc`, "unterminated string"},
		{"unterminated literal", "k = 'abc", "unterminated string"},
		{"dangling backslash", `k = "abc\`, "unterminated string"},
		{"bad escape", `k = "\q"`, `invalid escape \q`},
		{"short unicode", `k = "\u12"`, "short unicode escape"},
		{"surrogate escape", `k = "\uD800"`, "invalid unicode escape"},
		{"control char", "k = \"a\x01b\"", "control character"},
		{"multi-line", `k = """x"""`, "multi-line strings"},
		{"boolean", "k = true", "unsupported value"},
		{"float", "k = 1.5", "unsupported value"},
		{"array", "k = [1]", "unsupported value"},
		{"double sign", "k = --1", "unsupported value"},
		{"leading zero", "k = 007", "leading zero"},
		{"overflow", "k = 99999999999999999999", "out of range"},
		{"junk after value", `k = "a" b`, "unexpected text"},
		{"invalid utf8", "k = \"\xff\"", "not valid UTF-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTOML(tt.src)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseTOML(%q) error = %v, want substring %q", tt.src, err, tt.want)
			}
		})
	}
}

// encodeTOML writes a document back out in the same subset, so the fuzz
// test can check that whatever parses also round-trips.
func encodeTOML(doc document) string {
	var b strings.Builder
	tables := make([]string, 0, len(doc))
	for name := range doc {
		tables = append(tables, name)
	}
	sort.Strings(tables) // "" (the root) sorts first, as it must
	for _, name := range tables {
		if name != "" {
			fmt.Fprintf(&b, "[%s]\n", name)
		}
		for key, v := range doc[name] {
			switch v := v.(type) {
			case string:
				fmt.Fprintf(&b, "%s = %s\n", key, quoteBasic(v))
			case int64:
				fmt.Fprintf(&b, "%s = %d\n", key, v)
			}
		}
	}
	return b.String()
}

func quoteBasic(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func FuzzParseTOML(f *testing.F) {
	seeds := []string{
		"",
		"[upstreams]\ngw = \"https://gw.example.com\" # c\n",
		"[defaults]\nport = 4141\nproject = 'lit'\n",
		"k = \"\\u00e9\\t\\\"\"\n[t]\nn = -3\r\n",
		"[a]\n[a]\n",
		"k = \"unterminated",
		"[[x]]",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		doc, err := parseTOML(src) // must never panic
		if err != nil {
			return
		}
		again, err := parseTOML(encodeTOML(doc))
		if err != nil {
			t.Fatalf("re-encoded document does not parse: %v\nsource %q\nencoded %q", err, src, encodeTOML(doc))
		}
		if !reflect.DeepEqual(doc, again) {
			t.Fatalf("round trip changed the document:\n%#v\n%#v", doc, again)
		}
	})
}
