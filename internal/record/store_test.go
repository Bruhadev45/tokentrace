package record

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleRecord(project string, tokens int64) Record {
	return Record{
		Time:         time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		Project:      project,
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-5-20250929",
		Path:         "/v1/messages",
		Status:       200,
		LatencyMS:    812,
		InputTokens:  tokens,
		OutputTokens: tokens / 2,
		CachedTokens: tokens * 2,
		CostUSD:      0.0123,
		Priced:       true,
	}
}

func TestDir(t *testing.T) {
	tests := []struct {
		name   string
		envDir string
		want   func(t *testing.T, dir string)
	}{
		{
			name:   "env override wins",
			envDir: "/tmp/custom-tokentrace",
			want: func(t *testing.T, dir string) {
				if dir != "/tmp/custom-tokentrace" {
					t.Errorf("Dir() = %q, want env override", dir)
				}
			},
		},
		{
			name:   "defaults to home dot dir",
			envDir: "",
			want: func(t *testing.T, dir string) {
				if !strings.HasSuffix(dir, ".tokentrace") {
					t.Errorf("Dir() = %q, want ~/.tokentrace suffix", dir)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvDir, tt.envDir)
			dir, err := Dir()
			if err != nil {
				t.Fatalf("Dir() error = %v", err)
			}
			tt.want(t, dir)
		})
	}
}

func TestStoreAppendScanRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	want := []Record{sampleRecord("a", 100), sampleRecord("b", 200), sampleRecord("c", 300)}
	for _, r := range want {
		if err := store.Append(r); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var got []Record
	skipped, err := Scan(filepath.Join(dir, FileName), func(r Record) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if skipped != 0 {
		t.Errorf("Scan() skipped = %d, want 0", skipped)
	}
	if len(got) != len(want) {
		t.Fatalf("Scan() yielded %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Time.Equal(want[i].Time) {
			t.Errorf("record %d Time = %v, want %v", i, got[i].Time, want[i].Time)
		}
		got[i].Time = want[i].Time // normalize wall/monotonic representation
		if got[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestScanSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	content := `{"ts":"2026-08-30T12:00:00Z","project":"ok1","provider":"openai","path":"/x","status":200,"latency_ms":1,"input_tokens":1,"output_tokens":1,"cached_tokens":0,"cost_usd":0,"priced":false}
{this line is garbage
{"ts":"2026-08-30T12:00:01Z","project":"ok2","provider":"openai","path":"/x","status":200,"latency_ms":1,"input_tokens":1,"output_tokens":1,"cached_tokens":0,"cost_usd":0,"priced":false}
{"truncated`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var projects []string
	skipped, err := Scan(path, func(r Record) error {
		projects = append(projects, r.Project)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if skipped != 2 {
		t.Errorf("Scan() skipped = %d, want 2", skipped)
	}
	if len(projects) != 2 || projects[0] != "ok1" || projects[1] != "ok2" {
		t.Errorf("Scan() projects = %v, want [ok1 ok2]", projects)
	}
}

func TestScanErrStopEndsEarlyWithoutError(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := store.Append(sampleRecord("p", int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	count := 0
	_, err = Scan(filepath.Join(dir, FileName), func(Record) error {
		count++
		if count == 2 {
			return ErrStop
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v, want nil after ErrStop", err)
	}
	if count != 2 {
		t.Errorf("callback ran %d times, want 2", count)
	}
}

func TestScanPropagatesCallbackError(t *testing.T) {
	dir := t.TempDir()
	store, _ := Open(dir)
	_ = store.Append(sampleRecord("p", 1))
	store.Close()

	boom := errors.New("boom")
	_, err := Scan(filepath.Join(dir, FileName), func(Record) error { return boom })
	if !errors.Is(err, boom) {
		t.Errorf("Scan() error = %v, want %v", err, boom)
	}
}

func TestScanMissingFile(t *testing.T) {
	_, err := Scan(filepath.Join(t.TempDir(), "nope.jsonl"), func(Record) error { return nil })
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Scan() error = %v, want os.ErrNotExist", err)
	}
}

func TestOpenValidation(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error("Open(\"\") succeeded, want error")
	}
}
