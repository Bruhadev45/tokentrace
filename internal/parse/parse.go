// Package parse extracts token usage from OpenAI- and Anthropic-compatible
// API responses, both plain JSON and SSE streams.
//
// Bodies are only ever inspected in memory to extract usage metadata;
// nothing in this package retains, logs, or writes content.
package parse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Provider identifies the response dialect.
type Provider string

// Known providers.
const (
	OpenAI    Provider = "openai"
	Anthropic Provider = "anthropic"
	Unknown   Provider = "unknown"
)

// Usage is the token usage recovered from a response.
//
// InputTokens is always the number of non-cached input tokens: OpenAI's
// prompt_tokens includes cached tokens, so the cached share is subtracted
// out; Anthropic's input_tokens already excludes cache reads and writes.
// This makes Cached/(Input+Cached) a consistent cache-hit rate for both.
type Usage struct {
	InputTokens         int64
	OutputTokens        int64
	CachedTokens        int64
	CacheCreationTokens int64
	Model               string

	// Known reports whether a usage block was actually found. When false
	// the token counts are zero and must be treated as unknown, not free.
	Known bool
}

// DetectProviderFromHost guesses the provider from an upstream hostname.
func DetectProviderFromHost(host string) Provider {
	h := strings.ToLower(host)
	switch {
	case strings.Contains(h, "anthropic"):
		return Anthropic
	case strings.Contains(h, "openai"), strings.Contains(h, "azure"):
		return OpenAI
	default:
		return Unknown
	}
}

// DetectProviderFromBody guesses the provider from a response body (or a
// single SSE data payload) when the upstream host is ambiguous, e.g. a
// gateway or a local inference server speaking one of the two dialects.
func DetectProviderFromBody(body []byte) Provider {
	switch {
	// OpenAI's Responses API spells its counts input_tokens/output_tokens
	// exactly like Anthropic does, so it has to be recognized first.
	case isResponsesBody(body):
		return OpenAI
	case bytes.Contains(body, []byte(`"input_tokens"`)),
		bytes.Contains(body, []byte(`"message_start"`)),
		bytes.Contains(body, []byte(`"type":"message"`)),
		bytes.Contains(body, []byte(`"type": "message"`)):
		return Anthropic
	case bytes.Contains(body, []byte(`"prompt_tokens"`)),
		bytes.Contains(body, []byte(`"chat.completion`)):
		return OpenAI
	default:
		return Unknown
	}
}

// ModelFromRequest pulls the "model" field out of a request body. It is the
// fallback when a response carries no model (e.g. an unusable stream).
func ModelFromRequest(body []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	return req.Model
}

// responsesMarkers identify OpenAI's Responses API: the `response` object
// returned by /v1/responses and the `response.*` type carried by every
// event of its SSE stream.
var responsesMarkers = [][]byte{
	[]byte(`"object":"response"`), []byte(`"object": "response"`),
	[]byte(`"type":"response.`), []byte(`"type": "response.`),
}

// isResponsesBody reports whether a body (or one SSE data payload) is in
// the Responses API dialect rather than chat completions.
func isResponsesBody(body []byte) bool {
	for _, m := range responsesMarkers {
		if bytes.Contains(body, m) {
			return true
		}
	}
	return false
}

// tokenDetails is the breakdown object hanging off an OpenAI usage block.
type tokenDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

// openAIResponsesUsage mirrors the usage object of the Responses API,
// which reports the same quantities as chat completions under different
// names: input/output rather than prompt/completion, with the cached share
// nested under input_tokens_details. Reasoning tokens are already included
// in output_tokens, so they need no separate accounting.
type openAIResponsesUsage struct {
	InputTokens        int64         `json:"input_tokens"`
	OutputTokens       int64         `json:"output_tokens"`
	TotalTokens        int64         `json:"total_tokens"`
	InputTokensDetails *tokenDetails `json:"input_tokens_details"`
}

// toUsage normalizes Responses counts (input includes cached; split them).
func (ru *openAIResponsesUsage) toUsage(model string) Usage {
	var cached int64
	if ru.InputTokensDetails != nil {
		cached = ru.InputTokensDetails.CachedTokens
	}
	return Usage{
		InputTokens:  max(ru.InputTokens-cached, 0),
		OutputTokens: ru.OutputTokens,
		CachedTokens: cached,
		Model:        model,
		Known:        true,
	}
}

// openAIUsage mirrors the chat-completions usage object.
type openAIUsage struct {
	PromptTokens        int64         `json:"prompt_tokens"`
	CompletionTokens    int64         `json:"completion_tokens"`
	TotalTokens         int64         `json:"total_tokens"`
	PromptTokensDetails *tokenDetails `json:"prompt_tokens_details"`
}

// toUsage normalizes OpenAI counts (prompt includes cached; split them).
func (ou *openAIUsage) toUsage(model string) Usage {
	var cached int64
	if ou.PromptTokensDetails != nil {
		cached = ou.PromptTokensDetails.CachedTokens
	}
	return Usage{
		InputTokens:  max(ou.PromptTokens-cached, 0),
		OutputTokens: ou.CompletionTokens,
		CachedTokens: cached,
		Model:        model,
		Known:        true,
	}
}

// anthropicUsage mirrors the Anthropic usage object.
type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

func (au *anthropicUsage) toUsage(model string) Usage {
	return Usage{
		InputTokens:         au.InputTokens,
		OutputTokens:        au.OutputTokens,
		CachedTokens:        au.CacheReadInputTokens,
		CacheCreationTokens: au.CacheCreationInputTokens,
		Model:               model,
		Known:               true,
	}
}

// ParseOpenAIJSON extracts usage from a non-streaming OpenAI response
// body, in either the chat-completions or the Responses dialect. A body
// without a usage object yields Known == false, not an error.
func ParseOpenAIJSON(body []byte) (Usage, error) {
	if isResponsesBody(body) {
		return parseOpenAIResponsesJSON(body)
	}
	var resp struct {
		Model string       `json:"model"`
		Usage *openAIUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Usage{}, fmt.Errorf("decode openai response: %w", err)
	}
	if resp.Usage == nil {
		return Usage{Model: resp.Model}, nil
	}
	return resp.Usage.toUsage(resp.Model), nil
}

// parseOpenAIResponsesJSON extracts usage from a /v1/responses body.
func parseOpenAIResponsesJSON(body []byte) (Usage, error) {
	var resp struct {
		Model string                `json:"model"`
		Usage *openAIResponsesUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Usage{}, fmt.Errorf("decode openai responses body: %w", err)
	}
	if resp.Usage == nil {
		return Usage{Model: resp.Model}, nil
	}
	return resp.Usage.toUsage(resp.Model), nil
}

// ParseAnthropicJSON extracts usage from a non-streaming Anthropic response.
// A body without a usage object yields Known == false, not an error.
func ParseAnthropicJSON(body []byte) (Usage, error) {
	var resp struct {
		Model string          `json:"model"`
		Usage *anthropicUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Usage{}, fmt.Errorf("decode anthropic response: %w", err)
	}
	if resp.Usage == nil {
		return Usage{Model: resp.Model}, nil
	}
	return resp.Usage.toUsage(resp.Model), nil
}

// ParseJSON detects the provider (when p is Unknown) and dispatches to the
// matching non-streaming parser. An unrecognized dialect returns an empty
// Usage with provider Unknown and no error.
func ParseJSON(p Provider, body []byte) (Usage, Provider, error) {
	if p == Unknown {
		p = DetectProviderFromBody(body)
	}
	switch p {
	case OpenAI:
		u, err := ParseOpenAIJSON(body)
		return u, p, err
	case Anthropic:
		u, err := ParseAnthropicJSON(body)
		return u, p, err
	default:
		return Usage{}, Unknown, nil
	}
}
