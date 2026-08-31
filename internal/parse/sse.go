package parse

import (
	"bytes"
	"encoding/json"
)

// maxSSELine bounds the partial-line buffer; usage payloads are tiny, and a
// pathological line beyond this is discarded rather than buffered forever.
const maxSSELine = 1 << 20

// SSEAccumulator incrementally consumes a server-sent-events response body
// and accumulates token usage. Feed it the raw stream via Write as bytes
// pass through the proxy: it keeps only the current line and the running
// totals in memory, never the streamed content, so memory use is O(1) no
// matter how long the stream runs.
//
// Semantics per provider:
//   - Anthropic: message_start carries the model, input tokens and cache
//     read/creation tokens; message_delta carries the (cumulative) output
//     tokens. Later values overwrite earlier ones.
//   - OpenAI: chunks carry the model; the final chunk carries a usage
//     object only when the client set stream_options.include_usage. If no
//     usage ever appears, Usage().Known stays false.
type SSEAccumulator struct {
	provider Provider
	line     []byte
	u        Usage
}

// NewSSEAccumulator returns an accumulator for provider p. Pass Unknown to
// let the accumulator sniff the dialect from the first data payload.
func NewSSEAccumulator(p Provider) *SSEAccumulator {
	if p == "" {
		p = Unknown
	}
	return &SSEAccumulator{provider: p}
}

// Write implements io.Writer. It never returns an error.
func (a *SSEAccumulator) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if len(a.line)+len(p) <= maxSSELine {
				a.line = append(a.line, p...)
			} else {
				a.line = a.line[:0] // oversized line: drop it, keep going
			}
			break
		}
		line := p[:i]
		if len(a.line) > 0 {
			line = append(a.line, line...)
		}
		a.consumeLine(line)
		a.line = a.line[:0]
		p = p[i+1:]
	}
	return n, nil
}

// Flush consumes a trailing line that was not newline-terminated. Call it
// once the stream has ended, before reading Usage.
func (a *SSEAccumulator) Flush() {
	if len(a.line) > 0 {
		a.consumeLine(a.line)
		a.line = a.line[:0]
	}
}

// Usage returns the usage accumulated so far.
func (a *SSEAccumulator) Usage() Usage { return a.u }

// Provider returns the configured or sniffed provider.
func (a *SSEAccumulator) Provider() Provider { return a.provider }

func (a *SSEAccumulator) consumeLine(line []byte) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return // event:/id:/retry:/comment/blank lines carry no usage
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return
	}
	if a.provider == Unknown {
		a.provider = DetectProviderFromBody(data)
	}
	switch a.provider {
	case Anthropic:
		a.consumeAnthropic(data)
	case OpenAI:
		a.consumeOpenAI(data)
	}
}

func (a *SSEAccumulator) consumeAnthropic(data []byte) {
	// Cheap pre-filter: only message_start / message_delta matter and both
	// contain a usage object. content_block_delta text cannot fool the
	// type switch below even if it mentions "usage".
	if !bytes.Contains(data, []byte(`"usage"`)) {
		return
	}
	var ev struct {
		Type    string `json:"type"`
		Message *struct {
			Model string          `json:"model"`
			Usage *anthropicUsage `json:"usage"`
		} `json:"message"`
		Usage *anthropicUsage `json:"usage"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	switch ev.Type {
	case "message_start":
		if ev.Message == nil {
			return
		}
		if ev.Message.Model != "" {
			a.u.Model = ev.Message.Model
		}
		if mu := ev.Message.Usage; mu != nil {
			a.u.Known = true
			a.u.InputTokens = mu.InputTokens
			a.u.CachedTokens = mu.CacheReadInputTokens
			a.u.CacheCreationTokens = mu.CacheCreationInputTokens
			if mu.OutputTokens > 0 {
				a.u.OutputTokens = mu.OutputTokens
			}
		}
	case "message_delta":
		if du := ev.Usage; du != nil {
			a.u.Known = true
			if du.OutputTokens > 0 {
				a.u.OutputTokens = du.OutputTokens
			}
			// Some servers restate the input side in the final delta.
			if du.InputTokens > 0 {
				a.u.InputTokens = du.InputTokens
			}
			if du.CacheReadInputTokens > 0 {
				a.u.CachedTokens = du.CacheReadInputTokens
			}
			if du.CacheCreationInputTokens > 0 {
				a.u.CacheCreationTokens = du.CacheCreationInputTokens
			}
		}
	}
}

func (a *SSEAccumulator) consumeOpenAI(data []byte) {
	var chunk struct {
		Model string       `json:"model"`
		Usage *openAIUsage `json:"usage"`
	}
	if json.Unmarshal(data, &chunk) != nil {
		return
	}
	if chunk.Model != "" {
		a.u.Model = chunk.Model
	}
	if chunk.Usage == nil {
		return // ordinary delta chunk ("usage": null)
	}
	got := chunk.Usage.toUsage(a.u.Model)
	got.Model = a.u.Model
	a.u = got
}

// ParseOpenAIStream extracts usage from a complete OpenAI SSE body. When
// the client did not set stream_options.include_usage there is no usage
// chunk; the result then has Known == false (tokens unknown, model still
// recovered from the chunks when present).
func ParseOpenAIStream(body []byte) Usage {
	return parseStream(OpenAI, body)
}

// ParseAnthropicStream extracts usage from a complete Anthropic SSE body.
func ParseAnthropicStream(body []byte) Usage {
	return parseStream(Anthropic, body)
}

func parseStream(p Provider, body []byte) Usage {
	a := NewSSEAccumulator(p)
	_, _ = a.Write(body)
	a.Flush()
	return a.Usage()
}
