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

// Captured from POST /v1/responses with "stream": true — the Responses API
// emits one response.* event per stage and always ends with a terminal
// event carrying the full response object, usage included.
const openAIResponsesSSE = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_01","object":"response","created_at":1757404800,"status":"in_progress","model":"gpt-4.1-2025-04-14","output":[],"usage":null}}

event: response.in_progress
data: {"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_01","object":"response","status":"in_progress","model":"gpt-4.1-2025-04-14","output":[],"usage":null}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"msg_01","type":"message","status":"in_progress","role":"assistant","content":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":3,"item_id":"msg_01","output_index":0,"content_index":0,"delta":"Hello, \"usage\" fans"}

event: response.output_text.done
data: {"type":"response.output_text.done","sequence_number":4,"item_id":"msg_01","output_index":0,"content_index":0,"text":"Hello, \"usage\" fans"}

event: response.completed
data: {"type":"response.completed","sequence_number":5,"response":{"id":"resp_01","object":"response","status":"completed","model":"gpt-4.1-2025-04-14","output":[{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello, \"usage\" fans"}]}],"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":64},"output_tokens":48,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":168}}}
`

// A reasoning run that hit max_output_tokens: the terminal event is
// response.incomplete, and reasoning tokens are already folded into
// output_tokens by the API.
const openAIResponsesSSEIncomplete = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_02","object":"response","status":"in_progress","model":"gpt-5-mini","usage":null}}

event: response.incomplete
data: {"type":"response.incomplete","sequence_number":7,"response":{"id":"resp_02","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"model":"gpt-5-mini","usage":{"input_tokens":31,"input_tokens_details":{"cached_tokens":0},"output_tokens":512,"output_tokens_details":{"reasoning_tokens":448},"total_tokens":543}}}
`

// The upstream failed mid-run: response.failed still reports what was
// spent getting there.
const openAIResponsesSSEFailed = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_03","object":"response","status":"in_progress","model":"gpt-4.1-nano","usage":null}}

event: response.failed
data: {"type":"response.failed","sequence_number":4,"response":{"id":"resp_03","object":"response","status":"failed","error":{"code":"server_error","message":"upstream failure"},"model":"gpt-4.1-nano","usage":{"input_tokens":18,"output_tokens":0,"total_tokens":18}}}
`

// Client hung up after the first event: model known, tokens are not.
const openAIResponsesSSEAborted = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_04","object":"response","status":"in_progress","model":"gpt-4.1-mini","usage":null}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_04","output_index":0,"content_index":0,"delta":"partial"}
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

func TestParseOpenAIResponsesStream(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Usage
	}{
		{
			name: "response.completed carries usage",
			body: openAIResponsesSSE,
			// input_tokens includes the cached share, so it is split out.
			want: Usage{InputTokens: 56, OutputTokens: 48, CachedTokens: 64, Model: "gpt-4.1-2025-04-14", Known: true},
		},
		{
			name: "response.incomplete counts reasoning tokens as output",
			body: openAIResponsesSSEIncomplete,
			want: Usage{InputTokens: 31, OutputTokens: 512, Model: "gpt-5-mini", Known: true},
		},
		{
			name: "response.failed still reports what was spent",
			body: openAIResponsesSSEFailed,
			want: Usage{InputTokens: 18, Model: "gpt-4.1-nano", Known: true},
		},
		{
			name: "aborted before the terminal event",
			body: openAIResponsesSSEAborted,
			want: Usage{Model: "gpt-4.1-mini", Known: false},
		},
		{
			name: "terminal event alone",
			body: "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"model\":\"gpt-4.1\",\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"total_tokens\":10}}}\n",
			want: Usage{InputTokens: 7, OutputTokens: 3, Model: "gpt-4.1", Known: true},
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

// TestParseOpenAIResponsesStreamSniffed pins the dialect collision: the
// Responses API reports input_tokens/output_tokens just like Anthropic, so
// a stream from an ambiguous host must still be read as OpenAI.
func TestParseOpenAIResponsesStreamSniffed(t *testing.T) {
	a := NewSSEAccumulator(Unknown)
	_, _ = a.Write([]byte(openAIResponsesSSE))
	a.Flush()
	want := Usage{InputTokens: 56, OutputTokens: 48, CachedTokens: 64, Model: "gpt-4.1-2025-04-14", Known: true}
	if got := a.Usage(); got != want {
		t.Errorf("Usage() = %+v, want %+v", got, want)
	}
	if got := a.Provider(); got != OpenAI {
		t.Errorf("Provider() = %v, want %v", got, OpenAI)
	}
}

// TestSSEAccumulatorIncremental proves parsing is correct no matter how the
// stream is sliced into writes, as happens on a real network.
func TestSSEAccumulatorIncremental(t *testing.T) {
	streams := []struct {
		name     string
		provider Provider
		body     string
		want     Usage
	}{
		{
			name: "anthropic", provider: Anthropic, body: anthropicSSE,
			want: Usage{
				InputTokens: 25, OutputTokens: 15, CachedTokens: 100,
				CacheCreationTokens: 50, Model: "claude-sonnet-4-5-20250929", Known: true,
			},
		},
		{
			name: "openai responses", provider: OpenAI, body: openAIResponsesSSE,
			want: Usage{InputTokens: 56, OutputTokens: 48, CachedTokens: 64, Model: "gpt-4.1-2025-04-14", Known: true},
		},
	}
	for _, st := range streams {
		t.Run(st.name, func(t *testing.T) {
			for _, chunkSize := range []int{1, 3, 7, 64, 1024} {
				a := NewSSEAccumulator(st.provider)
				body := []byte(st.body)
				for i := 0; i < len(body); i += chunkSize {
					end := min(i+chunkSize, len(body))
					if n, err := a.Write(body[i:end]); err != nil || n != end-i {
						t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, end-i)
					}
				}
				a.Flush()
				if got := a.Usage(); got != st.want {
					t.Errorf("chunkSize %d: Usage() = %+v, want %+v", chunkSize, got, st.want)
				}
			}
		})
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
		{"openai responses", openAIResponsesSSE, OpenAI},
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
