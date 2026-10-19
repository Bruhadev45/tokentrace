package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileIsDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if cfg.Defaults != (Defaults{Port: DefaultPort, Project: DefaultProject}) {
		t.Errorf("Defaults = %+v", cfg.Defaults)
	}
	if cfg.Upstreams["anthropic"] != "https://api.anthropic.com" || cfg.Upstreams["openai"] != "https://api.openai.com" {
		t.Errorf("built-in aliases missing: %v", cfg.Upstreams)
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	src := `[upstreams]
gateway = "https://gw.example.com/llm"
openai = "http://127.0.0.1:9000"

[defaults]
upstream = "gateway"
port = 5000
project = "checkout-bot"
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	want := Defaults{Upstream: "gateway", Port: 5000, Project: "checkout-bot"}
	if cfg.Defaults != want {
		t.Errorf("Defaults = %+v, want %+v", cfg.Defaults, want)
	}
	if cfg.Upstreams["openai"] != "http://127.0.0.1:9000" {
		t.Errorf("file should override a built-in alias, got %q", cfg.Upstreams["openai"])
	}
	if cfg.Upstreams["anthropic"] != "https://api.anthropic.com" {
		t.Errorf("untouched built-in alias lost: %q", cfg.Upstreams["anthropic"])
	}
}

func TestLoadReportsPathAndLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("[defaults]\nport = \"x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %v, want the path and line number", err)
	}
}

func TestDefaultIsNotShared(t *testing.T) {
	a := Default()
	a.Upstreams["anthropic"] = "https://changed.example.com"
	if Default().Upstreams["anthropic"] != "https://api.anthropic.com" {
		t.Fatal("mutating one Default() leaked into the built-in table")
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"root key", "port = 1", `key "port" must be inside`},
		{"unknown table", "[upstream]\na = \"https://x.example\"", "unknown table [upstream]"},
		{"non-string alias", "[upstreams]\na = 1", "must be a string URL"},
		{"relative alias URL", "[upstreams]\na = \"api.example.com\"", "absolute URL"},
		{"unknown default", "[defaults]\nprot = 1", `unknown key "prot"`},
		{"port as string", "[defaults]\nport = \"4141\"", "port must be an integer"},
		{"port out of range", "[defaults]\nport = 70000", "port must be an integer"},
		{"empty project", "[defaults]\nproject = \"\"", "project must be a non-empty string"},
		{"numeric upstream", "[defaults]\nupstream = 3", "upstream must be a non-empty string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.src)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse(%q) error = %v, want substring %q", tt.src, err, tt.want)
			}
		})
	}
}

func TestResolveUpstream(t *testing.T) {
	cfg, err := Parse("[upstreams]\ngateway = \"https://gw.example.com/llm\"\n")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		in, want, wantErr string
	}{
		{in: "anthropic", want: "https://api.anthropic.com"},
		{in: "gateway", want: "https://gw.example.com/llm"},
		{in: "http://localhost:11434", want: "http://localhost:11434"},
		{in: "mistral", wantErr: "unknown upstream \"mistral\": use an alias (anthropic, gateway, openai"},
		{in: "api.openai.com", wantErr: "absolute URL"},
		{in: "://nohost", wantErr: "parse upstream"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			u, err := cfg.ResolveUpstream(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ResolveUpstream(%q) error = %v, want substring %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveUpstream(%q) error = %v", tt.in, err)
			}
			if u.String() != tt.want {
				t.Errorf("ResolveUpstream(%q) = %s, want %s", tt.in, u, tt.want)
			}
		})
	}
}
