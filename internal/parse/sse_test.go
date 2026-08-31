package parse

import "testing"

const anthropicSSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[],"usage":{"input_tokens":25,"cache_creation_input_tokens":50,"cache_read_input_tokens":100,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello, \"usage\" fans"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}
`

const openAISSEWithUsage = `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini-2024-07-18","choices":[{"index":0,"delta":{"role":"assistant","content":""}}],"usage":null}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini-2024-07-18","choices":[{"index":0,"delta":{"content":"Hello"}}],"usage":null}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini-2024-07-18","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o-mini-2024-07-18","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":25,"total_tokens":125,"prompt_tokens_details":{"cached_tokens":10}}}

data: [DONE]
`

const openAISSENoUsage = `data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"content":"Hi"}}]}

data: {"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`

func TestParseAnthropicStream(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Usage
	}{
		{
			name: "full stream",
			body: anthropicSSE,
			want: Usage{
				InputTokens: 25, OutputTokens: 15, CachedTokens: 100,
				CacheCreationTokens: 50, Model: "claude-sonnet-4-5-20250929", Known: true,
			},
		},
		{
			name: "message_start only (aborted stream)",
			body: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-haiku-4-5\",\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n",
			want: Usage{InputTokens: 12, OutputTokens: 1, Model: "claude-haiku-4-5", Known: true},
		},
		{
			name: "no usage anywhere",
			body: "event: ping\ndata: {\"type\":\"ping\"}\n",
			want: Usage{},
		},
		{
			name: "empty body",
			body: "",
			want: Usage{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseAnthropicStream([]byte(tt.body)); got != tt.want {
				t.Errorf("ParseAnthropicStream() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseOpenAIStream(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Usage
	}{
		{
			name: "final chunk carries usage (include_usage set)",
			body: openAISSEWithUsage,
			want: Usage{InputTokens: 90, OutputTokens: 25, CachedTokens: 10, Model: "gpt-4o-mini-2024-07-18", Known: true},
		},
		{
			name: "no usage chunk (include_usage not set)",
			body: openAISSENoUsage,
			want: Usage{Model: "gpt-4o-2024-08-06", Known: false},
		},
		{
			name: "empty body",
			body: "",
			want: Usage{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseOpenAIStream([]byte(tt.body)); got != tt.want {
				t.Errorf("ParseOpenAIStream() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestSSEAccumulatorIncremental proves parsing is correct no matter how the
// stream is sliced into writes, as happens on a real network.
func TestSSEAccumulatorIncremental(t *testing.T) {
	want := Usage{
		InputTokens: 25, OutputTokens: 15, CachedTokens: 100,
		CacheCreationTokens: 50, Model: "claude-sonnet-4-5-20250929", Known: true,
	}
	for _, chunkSize := range []int{1, 3, 7, 64, 1024} {
		a := NewSSEAccumulator(Anthropic)
		body := []byte(anthropicSSE)
		for i := 0; i < len(body); i += chunkSize {
			end := min(i+chunkSize, len(body))
			if n, err := a.Write(body[i:end]); err != nil || n != end-i {
				t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, end-i)
			}
		}
		a.Flush()
		if got := a.Usage(); got != want {
			t.Errorf("chunkSize %d: Usage() = %+v, want %+v", chunkSize, got, want)
		}
	}
}

func TestSSEAccumulatorSniffsProvider(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Provider
	}{
		{"anthropic", anthropicSSE, Anthropic},
		{"openai", openAISSEWithUsage, OpenAI},
		{"undetectable", "data: {\"x\":1}\n", Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewSSEAccumulator(Unknown)
			_, _ = a.Write([]byte(tt.body))
			a.Flush()
			if got := a.Provider(); got != tt.want {
				t.Errorf("Provider() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSSEAccumulatorCRLF(t *testing.T) {
	body := "data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":9,\"output_tokens\":1}}}\r\n\r\n"
	a := NewSSEAccumulator(Anthropic)
	_, _ = a.Write([]byte(body))
	a.Flush()
	got := a.Usage()
	if !got.Known || got.InputTokens != 9 || got.Model != "claude-sonnet-4-5" {
		t.Errorf("CRLF stream parsed as %+v", got)
	}
}

func TestSSEAccumulatorFlushHandlesUnterminatedLine(t *testing.T) {
	// Final data line without a trailing newline must still be consumed.
	body := "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}"
	a := NewSSEAccumulator(Anthropic)
	_, _ = a.Write([]byte(body))
	a.Flush()
	if got := a.Usage(); !got.Known || got.OutputTokens != 4 {
		t.Errorf("unterminated line parsed as %+v", got)
	}
}

func TestSSEAccumulatorIgnoresGarbage(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"non-json data", "data: hello world\n"},
		{"comment lines", ": keep-alive\n\n"},
		{"event only", "event: message_stop\n"},
		{"done marker", "data: [DONE]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewSSEAccumulator(OpenAI)
			_, _ = a.Write([]byte(tt.body))
			a.Flush()
			if got := a.Usage(); got.Known {
				t.Errorf("garbage input produced usage %+v", got)
			}
		})
	}
}
