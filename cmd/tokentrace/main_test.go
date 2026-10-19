package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bruhadev45/tokentrace/internal/config"
	"github.com/Bruhadev45/tokentrace/internal/record"
)

func TestRunDispatch(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErr    string // substring of the returned error; "" = no error
		wantStdout string // substring of stdout
	}{
		{"no args", nil, "missing command", ""},
		{"unknown command", []string{"frobnicate"}, `unknown command "frobnicate"`, ""},
		{"version", []string{"version"}, "", "tokentrace dev"},
		{"help", []string{"help"}, "", "know what every prompt costs"},
		{"prices table", []string{"prices"}, "", "claude-sonnet-4"},
		{"serve requires upstream", []string{"serve"}, "--upstream is required", ""},
		{"serve rejects relative upstream", []string{"serve", "--upstream", "api.openai.com"}, "absolute URL", ""},
		{"serve rejects bad port", []string{"serve", "--upstream", "https://api.openai.com", "--port", "70000"}, "out of range", ""},
		{"stats rejects bad by", []string{"stats", "--by", "week"}, "invalid --by", ""},
		{"stats rejects bad since", []string{"stats", "--since", "yesterday"}, "invalid --since", ""},
		{"stats rejects bad until", []string{"stats", "--until", "soon"}, "invalid --until", ""},
		{"stats rejects inverted window", []string{"stats", "--since", "2026-08-02", "--until", "2026-08-01"}, "must be later than --since", ""},
		{"serve rejects unknown alias", []string{"serve", "mistral"}, `unknown upstream "mistral"`, ""},
	}
	t.Setenv(record.EnvDir, t.TempDir()) // no stray config.toml from the real home dir
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(tt.args, &stdout, &stderr)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("run(%v) error = %v, want nil", tt.args, err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("run(%v) error = %v, want substring %q", tt.args, err, tt.wantErr)
				}
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.wantStdout)
			}
		})
	}
}

func TestStatsCommandOnEmptyDir(t *testing.T) {
	t.Setenv(record.EnvDir, t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := run([]string{"stats"}, &stdout, &stderr); err != nil {
		t.Fatalf("stats on empty dir error = %v", err)
	}
	if !strings.Contains(stdout.String(), "no records yet") {
		t.Errorf("stdout = %q, want friendly empty-state message", stdout.String())
	}
}

// seedStats writes a fixed two-record log into an isolated TOKENTRACE_DIR
// and returns the directory.
func seedStats(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(record.EnvDir, dir)
	store, err := record.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	recs := []record.Record{
		{Project: "app", Provider: "openai", Model: "gpt-4o-mini", Path: "/v1/chat/completions",
			Status: 200, LatencyMS: 120, InputTokens: 1000, OutputTokens: 100, CostUSD: 0.0002, Priced: true},
		{Project: "app", Provider: "openai", Model: "gpt-4o-mini", Path: "/v1/chat/completions",
			Status: 200, LatencyMS: 180, InputTokens: 2000, OutputTokens: 300, CachedTokens: 500,
			CostUSD: 0.0005, Priced: true},
	}
	for _, r := range recs {
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStatsCommandRendersSeededRecords(t *testing.T) {
	seedStats(t)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"stats", "--by", "model"}, &stdout, &stderr); err != nil {
		t.Fatalf("stats error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"gpt-4o-mini", "MODEL", "3,000", "$0.0007"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output missing %q:\n%s", want, out)
		}
	}
}

func TestStatsOutputFormats(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains []string
		excludes []string
	}{
		{
			name:     "json",
			args:     []string{"stats", "--json"},
			contains: []string{`"by": "model"`, `"key": "gpt-4o-mini"`, `"input_tokens": 3000`, `"cost_usd": 0.0007`, `"total"`},
			excludes: []string{"MODEL", "3,000"},
		},
		{
			name:     "csv",
			args:     []string{"stats", "--csv", "--by", "project"},
			contains: []string{"key,requests,input_tokens", "app,2,3000,400,500,0,0.000700"},
			excludes: []string{"TOTAL", "$"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seedStats(t)
			var stdout, stderr bytes.Buffer
			if err := run(tt.args, &stdout, &stderr); err != nil {
				t.Fatalf("run(%v) error = %v", tt.args, err)
			}
			out := stdout.String()
			for _, want := range tt.contains {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q:\n%s", want, out)
				}
			}
			for _, bad := range tt.excludes {
				if strings.Contains(out, bad) {
					t.Errorf("stdout should not contain %q:\n%s", bad, out)
				}
			}
		})
	}
}

func TestStatsOutFileWritesReport(t *testing.T) {
	seedStats(t)
	dest := filepath.Join(t.TempDir(), "report.json")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"stats", "--json", "--out", dest}, &stdout, &stderr); err != nil {
		t.Fatalf("run error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("--out should keep stdout clean, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "wrote 1 row(s) to "+dest) {
		t.Errorf("stderr = %q, want a confirmation line", stderr.String())
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read %s: %v", dest, err)
	}
	var doc struct {
		By   string `json:"by"`
		Rows []struct {
			Key      string `json:"key"`
			Requests int    `json:"requests"`
		} `json:"rows"`
		Total struct {
			Requests int `json:"requests"`
		} `json:"total"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("file is not valid JSON: %v\n%s", err, data)
	}
	if doc.By != "model" || len(doc.Rows) != 1 || doc.Rows[0].Key != "gpt-4o-mini" || doc.Total.Requests != 2 {
		t.Errorf("unexpected report: %+v", doc)
	}
}

// An empty log still produces a parseable document in the machine formats,
// so callers never have to special-case the empty state.
func TestStatsMachineFormatsOnEmptyLog(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"json", []string{"stats", "--json"}, "\"rows\": [],"},
		{"csv", []string{"stats", "--csv"}, "key,requests,input_tokens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(record.EnvDir, t.TempDir())
			var stdout, stderr bytes.Buffer
			if err := run(tt.args, &stdout, &stderr); err != nil {
				t.Fatalf("run(%v) error = %v", tt.args, err)
			}
			if !strings.Contains(stdout.String(), tt.want) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.want)
			}
			if strings.Contains(stdout.String(), "no records yet") {
				t.Errorf("machine format leaked the human empty-state message:\n%s", stdout.String())
			}
		})
	}
}

func TestStatsRejectsBothFormats(t *testing.T) {
	t.Setenv(record.EnvDir, t.TempDir())
	var stdout, stderr bytes.Buffer
	err := run([]string{"stats", "--json", "--csv"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want a mutually-exclusive error", err)
	}
}

func TestStatsOutFileUnwritable(t *testing.T) {
	seedStats(t)
	var stdout, stderr bytes.Buffer
	err := run([]string{"stats", "--json", "--out", filepath.Join(t.TempDir(), "nope", "report.json")}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "open --out") {
		t.Fatalf("error = %v, want an open --out error", err)
	}
}

func TestParseServeArgs(t *testing.T) {
	cfg, err := config.Parse(`[upstreams]
gateway = "https://gw.example.com/llm"

[defaults]
port = 5000
project = "from-config"
`)
	if err != nil {
		t.Fatal(err)
	}
	withDefault := cfg
	withDefault.Defaults.Upstream = "gateway"

	tests := []struct {
		name        string
		cfg         config.Config
		args        []string
		wantURL     string
		wantPort    int
		wantProject string
		wantErr     string
	}{
		{name: "leading alias", cfg: cfg, args: []string{"anthropic"},
			wantURL: "https://api.anthropic.com", wantPort: 5000, wantProject: "from-config"},
		{name: "alias then flags", cfg: cfg, args: []string{"openai", "--port", "4142", "--project", "cli"},
			wantURL: "https://api.openai.com", wantPort: 4142, wantProject: "cli"},
		{name: "flags then alias", cfg: cfg, args: []string{"--port", "4142", "gateway"},
			wantURL: "https://gw.example.com/llm", wantPort: 4142, wantProject: "from-config"},
		{name: "--upstream takes an alias", cfg: cfg, args: []string{"--upstream", "gateway"},
			wantURL: "https://gw.example.com/llm", wantPort: 5000, wantProject: "from-config"},
		{name: "--upstream takes a URL", cfg: cfg, args: []string{"--upstream", "http://127.0.0.1:11434"},
			wantURL: "http://127.0.0.1:11434", wantPort: 5000, wantProject: "from-config"},
		{name: "config default upstream", cfg: withDefault, args: nil,
			wantURL: "https://gw.example.com/llm", wantPort: 5000, wantProject: "from-config"},
		{name: "built-in defaults", cfg: config.Default(), args: []string{"anthropic"},
			wantURL: "https://api.anthropic.com", wantPort: 4141, wantProject: "default"},
		{name: "no upstream anywhere", cfg: cfg, args: []string{"--port", "1"}, wantErr: "--upstream is required"},
		{name: "upstream twice", cfg: cfg, args: []string{"anthropic", "--upstream", "openai"}, wantErr: "not both"},
		{name: "two positionals", cfg: cfg, args: []string{"anthropic", "openai"}, wantErr: "unexpected arguments: openai"},
		{name: "bad port", cfg: cfg, args: []string{"anthropic", "--port", "0"}, wantErr: "out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, err := parseServeArgs(tt.args, tt.cfg, &stderr)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseServeArgs(%v) error = %v", tt.args, err)
			}
			if opts.upstream.String() != tt.wantURL || opts.port != tt.wantPort || opts.project != tt.wantProject {
				t.Errorf("got %s :%d %q, want %s :%d %q",
					opts.upstream, opts.port, opts.project, tt.wantURL, tt.wantPort, tt.wantProject)
			}
		})
	}
}

func TestServeReportsBrokenConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(record.EnvDir, dir)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte("[defaults]\nprot = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"serve", "anthropic"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), `unknown key "prot" in [defaults]`) {
		t.Fatalf("error = %v, want the config parse error", err)
	}
}

func TestStatsUntilFiltersRecords(t *testing.T) {
	// seedStats records carry the zero time, so they sit before any date.
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"stats", "--json", "--until", "2000-01-01"}, `"requests": 2`},
		{[]string{"stats", "--json", "--since", "1999-01-01", "--until", "2000-01-01"}, `"rows": [],`},
	}
	for _, tt := range tests {
		seedStats(t)
		var stdout, stderr bytes.Buffer
		if err := run(tt.args, &stdout, &stderr); err != nil {
			t.Fatalf("run(%v) error = %v", tt.args, err)
		}
		if !strings.Contains(stdout.String(), tt.want) {
			t.Errorf("run(%v) stdout missing %q:\n%s", tt.args, tt.want, stdout.String())
		}
	}
}
