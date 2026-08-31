package pricing

import (
	"math"
	"testing"
)

func TestLookupLongestPrefix(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		wantInput float64
		wantOK    bool
	}{
		{"dated sonnet 4.5 release", "claude-sonnet-4-5-20250929", 3, true},
		{"dated opus release", "claude-opus-4-1-20250805", 15, true},
		{"haiku 4.5", "claude-haiku-4-5-20251001", 1, true},
		{"legacy 3.5 haiku", "claude-3-5-haiku-20241022", 0.80, true},
		{"gpt-4o-mini beats gpt-4o and gpt-4", "gpt-4o-mini-2024-07-18", 0.15, true},
		{"gpt-4o beats gpt-4", "gpt-4o-2024-08-06", 2.50, true},
		{"bare gpt-4", "gpt-4-0613", 30, true},
		{"gpt-4.1 nano", "gpt-4.1-nano-2025-04-14", 0.10, true},
		{"gpt-5", "gpt-5-2025-08-07", 1.25, true},
		{"o1-mini beats o1", "o1-mini-2024-09-12", 1.10, true},
		{"bare o1", "o1-2024-12-17", 15, true},
		{"o4-mini", "o4-mini-2025-04-16", 1.10, true},
		{"case insensitive", "GPT-4O-MINI", 0.15, true},
		{"unknown model", "llama-3.1-70b-instruct", 0, false},
		{"gemini not priced", "gemini-2.0-flash", 0, false},
		{"empty model", "", 0, false},
		{"whitespace only", "   ", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := Lookup(tt.model)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.model, ok, tt.wantOK)
			}
			if ok && p.Input != tt.wantInput {
				t.Errorf("Lookup(%q).Input = %v, want %v", tt.model, p.Input, tt.wantInput)
			}
		})
	}
}

func TestCost(t *testing.T) {
	tests := []struct {
		name                          string
		model                         string
		input, output, cached, cwrite int64
		want                          float64
		wantOK                        bool
	}{
		{
			name:  "gpt-4o-mini with cached read",
			model: "gpt-4o-mini-2024-07-18",
			input: 90, output: 25, cached: 10,
			// (90*0.15 + 25*0.60 + 10*0.075) / 1e6
			want: 29.25e-6, wantOK: true,
		},
		{
			name:  "claude sonnet with cache read and write",
			model: "claude-sonnet-4-5-20250929",
			input: 25, output: 15, cached: 100, cwrite: 50,
			// (25*3 + 15*15 + 100*0.30 + 50*3.75) / 1e6
			want: 517.5e-6, wantOK: true,
		},
		{
			name:   "cached rate falls back to input rate when unset",
			model:  "gpt-4-turbo",
			cached: 1_000_000,
			want:   10, wantOK: true,
		},
		{
			name:   "cache write rate falls back to input rate when unset",
			model:  "gpt-4o",
			cwrite: 1_000_000,
			want:   2.50, wantOK: true,
		},
		{
			name:  "zero tokens cost zero",
			model: "claude-haiku-4-5",
			want:  0, wantOK: true,
		},
		{
			name:  "unknown model marks no price",
			model: "mystery-model-9000",
			input: 1_000_000, output: 1_000_000,
			want: 0, wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Cost(tt.model, tt.input, tt.output, tt.cached, tt.cwrite)
			if ok != tt.wantOK {
				t.Fatalf("Cost() ok = %v, want %v", ok, tt.wantOK)
			}
			if math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("Cost() = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func TestEntriesSortedAndComplete(t *testing.T) {
	entries := Entries()
	if len(entries) != len(table) {
		t.Fatalf("Entries() returned %d rows, table has %d", len(entries), len(table))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Prefix >= entries[i].Prefix {
			t.Errorf("Entries() not sorted: %q before %q", entries[i-1].Prefix, entries[i].Prefix)
		}
	}
	for _, e := range entries {
		if e.Price.Input <= 0 || e.Price.Output <= 0 {
			t.Errorf("entry %q has non-positive base price %+v", e.Prefix, e.Price)
		}
	}
}
