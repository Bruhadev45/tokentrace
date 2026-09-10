package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	}
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
