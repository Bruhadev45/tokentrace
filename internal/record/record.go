// Package record defines the usage record written for every proxied request
// and the append-only JSONL store that persists records on disk.
package record

import "time"

// Record is one proxied request's usage metadata. It is the only thing
// tokentrace ever writes to disk: no prompts, no completions, no headers.
type Record struct {
	Time     time.Time `json:"ts"`
	Project  string    `json:"project"`
	Provider string    `json:"provider"`
	Model    string    `json:"model,omitempty"`
	Path     string    `json:"path"`
	Status   int       `json:"status"`

	// LatencyMS is the wall-clock time in milliseconds from forwarding the
	// request upstream until the last response byte was relayed.
	LatencyMS int64 `json:"latency_ms"`

	// InputTokens counts non-cached input tokens. OpenAI's prompt_tokens
	// includes cached tokens, so the cached share is subtracted at parse
	// time; Anthropic's input_tokens already excludes cache reads/writes.
	// This makes Cached/(Input+Cached) a consistent cache-hit rate.
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CachedTokens        int64 `json:"cached_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`

	// Unknown is true when the response carried no parseable usage (for
	// example an OpenAI stream without stream_options.include_usage).
	Unknown bool `json:"unknown,omitempty"`

	// CostUSD is the estimated cost. Priced is false when the model was not
	// found in the price table; the cost is then recorded as zero.
	CostUSD float64 `json:"cost_usd"`
	Priced  bool    `json:"priced"`
}
