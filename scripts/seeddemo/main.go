// Command seeddemo writes a small deterministic sample record log into
// TOKENTRACE_DIR so `make demo` (and the README) can show real stats
// output. It is a development helper, not part of the shipped binary.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/pricing"
	"github.com/Bruhadev45/tokentrace/internal/record"
)

type profile struct {
	project  string
	provider string
	model    string
	path     string
	inBase   int64 // typical non-cached input tokens
	outBase  int64
	cached   int64 // typical cache-read tokens (0 = no caching)
	latBase  int64 // typical latency ms
	perDay   int
}

func main() {
	dir := os.Getenv(record.EnvDir)
	if dir == "" {
		fmt.Fprintln(os.Stderr, "seeddemo: set TOKENTRACE_DIR to a throwaway directory first")
		os.Exit(1)
	}
	if err := seed(dir); err != nil {
		fmt.Fprintln(os.Stderr, "seeddemo:", err)
		os.Exit(1)
	}
}

func seed(dir string) error {
	store, err := record.Open(dir)
	if err != nil {
		return err
	}
	defer store.Close()

	profiles := []profile{
		{"checkout-bot", "anthropic", "claude-sonnet-4-5-20250929", "/v1/messages", 1400, 420, 9600, 2400, 6},
		{"checkout-bot", "anthropic", "claude-haiku-4-5-20251001", "/v1/messages", 700, 150, 5200, 900, 9},
		{"docs-search", "openai", "gpt-4o-mini-2024-07-18", "/v1/chat/completions", 2100, 260, 1024, 1200, 8},
		{"docs-search", "openai", "gpt-4.1-2025-04-14", "/v1/chat/completions", 3400, 700, 0, 3100, 3},
	}

	rng := rand.New(rand.NewSource(7)) // deterministic sample data
	now := time.Now()
	for dayOffset := 2; dayOffset >= 0; dayOffset-- {
		day := now.Add(-time.Duration(dayOffset) * 24 * time.Hour)
		for _, p := range profiles {
			for i := 0; i < p.perDay; i++ {
				if err := store.Append(sample(rng, p, day, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// sample builds one plausible record: jittered tokens and latency, cost
// computed through the real price table.
func sample(rng *rand.Rand, p profile, day time.Time, i int) record.Record {
	jitter := func(base int64) int64 {
		if base == 0 {
			return 0
		}
		return base + rng.Int63n(base/2+1) - base/4
	}
	in, out := jitter(p.inBase), jitter(p.outBase)
	cached := jitter(p.cached)
	var cacheWrite int64
	if p.cached > 0 && i == 0 {
		cached, cacheWrite = 0, p.cached // first call of the day warms the cache
	}
	cost, priced := pricing.Cost(p.model, in, out, cached, cacheWrite)
	return record.Record{
		Time:                day.Add(-time.Duration(rng.Intn(8*3600)) * time.Second),
		Project:             p.project,
		Provider:            p.provider,
		Model:               p.model,
		Path:                p.path,
		Status:              200,
		LatencyMS:           jitter(p.latBase),
		InputTokens:         in,
		OutputTokens:        out,
		CachedTokens:        cached,
		CacheCreationTokens: cacheWrite,
		CostUSD:             cost,
		Priced:              priced,
	}
}
