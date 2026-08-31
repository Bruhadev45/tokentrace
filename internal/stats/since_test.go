package stats

import (
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
