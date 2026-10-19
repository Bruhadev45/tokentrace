package stats

import (
	"strings"
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 8, 31, 15, 30, 0, 0, time.Local)
	tests := []struct {
		name    string
		in      string
		want    time.Time
		wantErr bool
	}{
		{"empty means no bound", "", time.Time{}, false},
		{"hours", "24h", now.Add(-24 * time.Hour), false},
		{"minutes", "30m", now.Add(-30 * time.Minute), false},
		{"compound duration", "1h30m", now.Add(-90 * time.Minute), false},
		{"days", "7d", now.Add(-7 * 24 * time.Hour), false},
		{"zero days", "0d", now, false},
		{"date", "2026-08-01", time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local), false},
		{"surrounding whitespace", " 24h ", now.Add(-24 * time.Hour), false},
		{"garbage", "yesterday", time.Time{}, true},
		{"negative duration", "-5h", time.Time{}, true},
		{"negative days", "-3d", time.Time{}, true},
		{"bad day count", "xd", time.Time{}, true},
		{"bad date", "2026-13-45", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSince(tt.in, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSince(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("ParseSince(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseUntil(t *testing.T) {
	now := time.Date(2026, 8, 31, 15, 30, 0, 0, time.Local)
	got, err := ParseUntil("2026-08-02", now)
	if err != nil || !got.Equal(time.Date(2026, 8, 2, 0, 0, 0, 0, time.Local)) {
		t.Errorf("ParseUntil(date) = %v, %v", got, err)
	}
	got, err = ParseUntil("24h", now)
	if err != nil || !got.Equal(now.Add(-24*time.Hour)) {
		t.Errorf("ParseUntil(24h) = %v, %v", got, err)
	}
	if got, err := ParseUntil("", now); err != nil || !got.IsZero() {
		t.Errorf("ParseUntil(\"\") = %v, %v, want zero time", got, err)
	}
	if _, err := ParseUntil("soon", now); err == nil || !strings.Contains(err.Error(), "invalid --until") {
		t.Errorf("ParseUntil(soon) error = %v, want it to name --until", err)
	}
}
