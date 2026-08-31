// Package pricing maps model names to USD prices per million tokens and
// estimates per-request cost.
//
// The table is a static snapshot of published list prices: estimates for
// budgeting, not a substitute for the provider's bill. Models are matched
// by longest name prefix so dated releases ("claude-sonnet-4-5-20250929",
// "gpt-4o-mini-2024-07-18") resolve without listing every revision.
package pricing

import (
	"sort"
	"strings"
)

// Price is USD per million tokens (MTok).
type Price struct {
	Input      float64 // non-cached input tokens
	Output     float64 // output tokens
	CachedRead float64 // cached input tokens read (0 = bill at Input rate)
	CacheWrite float64 // cache-creation tokens (0 = bill at Input rate)
}

// table maps model-name prefixes to prices. Longest matching prefix wins,
// so "gpt-4o-mini" beats "gpt-4o" beats "gpt-4".
var table = map[string]Price{
	// Anthropic
	"claude-opus-4":     {Input: 15, Output: 75, CachedRead: 1.50, CacheWrite: 18.75},
	"claude-sonnet-4":   {Input: 3, Output: 15, CachedRead: 0.30, CacheWrite: 3.75},
	"claude-haiku-4":    {Input: 1, Output: 5, CachedRead: 0.10, CacheWrite: 1.25},
	"claude-3-7-sonnet": {Input: 3, Output: 15, CachedRead: 0.30, CacheWrite: 3.75},
	"claude-3-5-sonnet": {Input: 3, Output: 15, CachedRead: 0.30, CacheWrite: 3.75},
	"claude-3-5-haiku":  {Input: 0.80, Output: 4, CachedRead: 0.08, CacheWrite: 1},
	"claude-3-opus":     {Input: 15, Output: 75, CachedRead: 1.50, CacheWrite: 18.75},
	"claude-3-haiku":    {Input: 0.25, Output: 1.25, CachedRead: 0.03, CacheWrite: 0.30},

	// OpenAI
	"gpt-5-nano":    {Input: 0.05, Output: 0.40, CachedRead: 0.005},
	"gpt-5-mini":    {Input: 0.25, Output: 2, CachedRead: 0.025},
	"gpt-5":         {Input: 1.25, Output: 10, CachedRead: 0.125},
	"gpt-4.1-nano":  {Input: 0.10, Output: 0.40, CachedRead: 0.025},
	"gpt-4.1-mini":  {Input: 0.40, Output: 1.60, CachedRead: 0.10},
	"gpt-4.1":       {Input: 2, Output: 8, CachedRead: 0.50},
	"gpt-4o-mini":   {Input: 0.15, Output: 0.60, CachedRead: 0.075},
	"gpt-4o":        {Input: 2.50, Output: 10, CachedRead: 1.25},
	"gpt-4-turbo":   {Input: 10, Output: 30},
	"gpt-4":         {Input: 30, Output: 60},
	"gpt-3.5-turbo": {Input: 0.50, Output: 1.50},
	"o1-mini":       {Input: 1.10, Output: 4.40, CachedRead: 0.55},
	"o1":            {Input: 15, Output: 60, CachedRead: 7.50},
	"o3-mini":       {Input: 1.10, Output: 4.40, CachedRead: 0.55},
	"o3":            {Input: 2, Output: 8, CachedRead: 0.50},
	"o4-mini":       {Input: 1.10, Output: 4.40, CachedRead: 0.275},
}

// Lookup returns the price for the longest table prefix matching model
// (case-insensitive). ok is false when no prefix matches.
func Lookup(model string) (Price, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return Price{}, false
	}
	best := ""
	for prefix := range table {
		if strings.HasPrefix(m, prefix) && len(prefix) > len(best) {
			best = prefix
		}
	}
	if best == "" {
		return Price{}, false
	}
	return table[best], true
}

// Cost estimates the USD cost of one request. ok is false — and the cost
// zero — when the model has no price in the table ("no price" marker).
func Cost(model string, input, output, cachedRead, cacheWrite int64) (cost float64, ok bool) {
	p, ok := Lookup(model)
	if !ok {
		return 0, false
	}
	cr, cw := p.CachedRead, p.CacheWrite
	if cr == 0 {
		cr = p.Input
	}
	if cw == 0 {
		cw = p.Input
	}
	const mtok = 1e6
	cost = float64(input)*p.Input/mtok +
		float64(output)*p.Output/mtok +
		float64(cachedRead)*cr/mtok +
		float64(cacheWrite)*cw/mtok
	return cost, true
}

// Entry is one displayable price-table row.
type Entry struct {
	Prefix string
	Price  Price
}

// Entries returns the table sorted by prefix, for `tokentrace prices`.
func Entries() []Entry {
	out := make([]Entry, 0, len(table))
	for prefix, price := range table {
		out = append(out, Entry{Prefix: prefix, Price: price})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
	return out
}
