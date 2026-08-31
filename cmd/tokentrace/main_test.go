package main

import (
	"bytes"
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

func TestStatsCommandRendersSeededRecords(t *testing.T) {
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
	store.Close()

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
