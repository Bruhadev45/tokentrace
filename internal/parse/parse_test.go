package parse

import "testing"

func TestDetectProviderFromHost(t *testing.T) {
	tests := []struct {
		name string
		host string
		want Provider
	}{
		{"anthropic api", "api.anthropic.com", Anthropic},
		{"openai api", "api.openai.com", OpenAI},
		{"azure openai", "myres.openai.azure.com", OpenAI},
		{"case insensitive", "API.ANTHROPIC.COM", Anthropic},
		{"localhost", "127.0.0.1:8080", Unknown},
		{"gateway", "llm.internal.example.com", Unknown},
		{"empty", "", Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectProviderFromHost(tt.host); got != tt.want {
				t.Errorf("DetectProviderFromHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestDetectProviderFromBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Provider
	}{
		{"anthropic message", `{"id":"msg_1","type":"message","usage":{"input_tokens":1}}`, Anthropic},
		{"anthropic spaced", `{"type": "message"}`, Anthropic},
		{"anthropic sse start", `{"type":"message_start","message":{}}`, Anthropic},
		{"openai completion", `{"object":"chat.completion","usage":{"prompt_tokens":1}}`, OpenAI},
		{"openai chunk", `{"object":"chat.completion.chunk"}`, OpenAI},
		{"openai responses body", `{"id":"resp_1","object":"response","usage":{"input_tokens":9}}`, OpenAI},
		{"openai responses spaced", `{"object": "response"}`, OpenAI},
		{"openai responses event", `{"type":"response.completed","response":{"usage":{"input_tokens":9}}}`, OpenAI},
		{"openai responses delta event", `{"type":"response.output_text.delta","delta":"hi"}`, OpenAI},
		{"unrecognized", `{"hello":"world"}`, Unknown},
		{"empty", ``, Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectProviderFromBody([]byte(tt.body)); got != tt.want {
				t.Errorf("DetectProviderFromBody(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestModelFromRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"present", `{"model":"gpt-4o","messages":[]}`, "gpt-4o"},
		{"absent", `{"messages":[]}`, ""},
		{"malformed", `{"model":`, ""},
		{"empty", ``, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModelFromRequest([]byte(tt.body)); got != tt.want {
				t.Errorf("ModelFromRequest(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestParseOpenAIJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    Usage
		wantErr bool
	}{
		{
			name: "usage with cached tokens",
			body: `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o-mini-2024-07-18",
				"choices":[{"index":0,"message":{"role":"assistant","content":"hi"}}],
				"usage":{"prompt_tokens":100,"completion_tokens":25,"total_tokens":125,
				"prompt_tokens_details":{"cached_tokens":10}}}`,
			want: Usage{InputTokens: 90, OutputTokens: 25, CachedTokens: 10, Model: "gpt-4o-mini-2024-07-18", Known: true},
		},
		{
			name: "usage without details",
			body: `{"model":"gpt-4.1","usage":{"prompt_tokens":40,"completion_tokens":8,"total_tokens":48}}`,
			want: Usage{InputTokens: 40, OutputTokens: 8, Model: "gpt-4.1", Known: true},
		},
		{
			name: "no usage object",
			body: `{"model":"gpt-4o","choices":[]}`,
			want: Usage{Model: "gpt-4o"},
		},
		{
			name: "cached exceeding prompt clamps at zero",
			body: `{"model":"gpt-4o","usage":{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":9}}}`,
			want: Usage{InputTokens: 0, OutputTokens: 1, CachedTokens: 9, Model: "gpt-4o", Known: true},
		},
		{
			name: "responses dialect with cached tokens",
			body: `{"id":"resp_1","object":"response","status":"completed","model":"gpt-4.1-2025-04-14",
				"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":64},
				"output_tokens":48,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":168}}`,
			want: Usage{InputTokens: 56, OutputTokens: 48, CachedTokens: 64, Model: "gpt-4.1-2025-04-14", Known: true},
		},
		{
			name: "responses dialect without usage",
			body: `{"id":"resp_2","object":"response","status":"in_progress","model":"gpt-5-mini","usage":null}`,
			want: Usage{Model: "gpt-5-mini"},
		},
		{name: "malformed json", body: `{"usage":`, wantErr: true},
		{name: "malformed responses json", body: `{"object":"response","usage":`, wantErr: true},
		{name: "empty body", body: ``, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOpenAIJSON([]byte(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseOpenAIJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseOpenAIJSON() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseAnthropicJSON(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    Usage
		wantErr bool
	}{
		{
			name: "full usage with cache fields",
			body: `{"id":"msg_1","type":"message","model":"claude-sonnet-4-5-20250929",
				"content":[{"type":"text","text":"hi"}],
				"usage":{"input_tokens":25,"output_tokens":50,
				"cache_read_input_tokens":100,"cache_creation_input_tokens":10}}`,
			want: Usage{
				InputTokens: 25, OutputTokens: 50, CachedTokens: 100,
				CacheCreationTokens: 10, Model: "claude-sonnet-4-5-20250929", Known: true,
			},
		},
		{
			name: "usage without cache fields",
			body: `{"model":"claude-3-5-haiku-20241022","usage":{"input_tokens":7,"output_tokens":3}}`,
			want: Usage{InputTokens: 7, OutputTokens: 3, Model: "claude-3-5-haiku-20241022", Known: true},
		},
		{
			name: "no usage object",
			body: `{"type":"error","error":{"type":"overloaded_error"}}`,
			want: Usage{},
		},
		{name: "malformed json", body: `not json`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAnthropicJSON([]byte(tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseAnthropicJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseAnthropicJSON() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseJSONDispatch(t *testing.T) {
	openAIBody := `{"object":"chat.completion","model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":2}}`
	anthropicBody := `{"type":"message","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":2}}`
	responsesBody := `{"object":"response","model":"gpt-4.1","usage":{"input_tokens":10,"output_tokens":2}}`
	tests := []struct {
		name         string
		hint         Provider
		body         string
		wantProvider Provider
		wantKnown    bool
	}{
		{"explicit openai", OpenAI, openAIBody, OpenAI, true},
		{"explicit anthropic", Anthropic, anthropicBody, Anthropic, true},
		{"sniffed openai", Unknown, openAIBody, OpenAI, true},
		{"sniffed anthropic", Unknown, anthropicBody, Anthropic, true},
		{"sniffed openai responses", Unknown, responsesBody, OpenAI, true},
		{"undetectable", Unknown, `{"foo":1}`, Unknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, provider, err := ParseJSON(tt.hint, []byte(tt.body))
			if err != nil {
				t.Fatalf("ParseJSON() error = %v", err)
			}
			if provider != tt.wantProvider {
				t.Errorf("provider = %v, want %v", provider, tt.wantProvider)
			}
			if got.Known != tt.wantKnown {
				t.Errorf("Known = %v, want %v", got.Known, tt.wantKnown)
			}
		})
	}
}
