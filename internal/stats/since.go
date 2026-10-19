package stats

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseSince parses a --since value relative to now. Accepted forms:
//
//	""            -> zero time (no lower bound)
//	"30m", "24h"  -> any time.ParseDuration value, looking back from now
//	"7d"          -> whole days looking back from now
//	"2026-08-01"  -> that local calendar date at midnight
func ParseSince(s string, now time.Time) (time.Time, error) {
	return parseBound("--since", s, now)
}

// ParseUntil parses an --until value, the exclusive upper bound. It takes
// the same forms as ParseSince, so "--since 2026-08-01 --until 2026-08-02"
// selects exactly that one day and "--until 24h" stops a day ago.
func ParseUntil(s string, now time.Time) (time.Time, error) {
	return parseBound("--until", s, now)
}

func parseBound(flag, s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err == nil && days >= 0 {
			return now.Add(-time.Duration(days) * 24 * time.Hour), nil
		}
		return time.Time{}, fmt.Errorf("invalid %s %q (want e.g. 24h, 7d or 2026-08-01)", flag, s)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid %s %q (want e.g. 24h, 7d or 2026-08-01)", flag, s)
	}
	if d < 0 {
		return time.Time{}, fmt.Errorf("invalid %s %q: negative duration", flag, s)
	}
	return now.Add(-d), nil
}
