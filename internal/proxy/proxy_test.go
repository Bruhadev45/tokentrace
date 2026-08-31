package proxy_test

import (
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/proxy"
	"github.com/Bruhadev45/tokentrace/internal/record"
)

const (
	testAuthToken = "sk-tokentrace-e2e-secret"
	upstreamSleep = 15 * time.Millisecond
)

const openAICompletionBody = `{"id":"chatcmpl-1","object":"chat.completion","created":123,` +
	`"model":"gpt-4o-mini-2024-07-18",` +
	`"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` +
	`"usage":{"prompt_tokens":100,"completion_tokens":25,"total_tokens":125,` +
	`"prompt_tokens_details":{"cached_tokens":10}}}`

var anthropicSSEChunks = []string{
	"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5-20250929\",\"content\":[],\"usage\":{\"input_tokens\":25,\"cache_creation_input_tokens\":50,\"cache_read_input_tokens\":100,\"output_tokens\":1}}}\n\n",
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n",
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":15}}\n\n",
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
}

var openAISSENoUsageChunks = []string{
	"data: {\"id\":\"chatcmpl-2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"}}]}\n\n",
	"data: {\"id\":\"chatcmpl-2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
	"data: [DONE]\n\n",
}

// newUpstream returns a fake LLM API able to answer with (a) an OpenAI JSON
// completion, (b) an Anthropic SSE stream, and (c) an OpenAI SSE stream
// with no usage chunk. It asserts auth headers arrived intact.
func newUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	streamOut := func(w http.ResponseWriter, chunks []string) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		for _, c := range chunks {
			_, _ = io.WriteString(w, c)
			fl.Flush()
			time.Sleep(2 * time.Millisecond)
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(upstreamSleep) // guarantee measurable latency
		switch r.URL.Path {
		case "/v1/chat/completions":
			if got := r.Header.Get("Authorization"); got != "Bearer "+testAuthToken {
				t.Errorf("upstream Authorization = %q, want pass-through bearer token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, openAICompletionBody)
		case "/v1/messages":
			if got := r.Header.Get("x-api-key"); got != testAuthToken {
				t.Errorf("upstream x-api-key = %q, want pass-through key", got)
			}
			streamOut(w, anthropicSSEChunks)
		case "/v1/stream-no-usage":
			streamOut(w, openAISSENoUsageChunks)
		default:
			http.NotFound(w, r)
		}
	}))
}

// newProxy wires a proxy in front of upstreamURL, recording into a temp
// store, and returns the front server plus the records file path.
func newProxy(t *testing.T, upstreamURL string) (front *httptest.Server, recordsPath string) {
	t.Helper()
	dir := t.TempDir()
	store, err := record.Open(dir)
	if err != nil {
		t.Fatalf("record.Open() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })

	u, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	handler, err := proxy.New(proxy.Config{
		Upstream: u,
		Project:  "e2e",
		Recorder: store,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatalf("proxy.New() error = %v", err)
	}
	front = httptest.NewServer(handler)
	t.Cleanup(front.Close)
	return front, filepath.Join(dir, record.FileName)
}

// waitForRecords polls the JSONL file until want records exist.
func waitForRecords(t *testing.T, path string, want int) []record.Record {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var recs []record.Record
		if _, err := os.Stat(path); err == nil {
			if _, err := record.Scan(path, func(r record.Record) error {
				recs = append(recs, r)
				return nil
			}); err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
		}
		if len(recs) >= want {
			return recs
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %d record(s) in %s, want %d", len(recs), path, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func postJSON(t *testing.T, url, body string, header http.Header) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp.StatusCode, string(got)
}

func TestProxyEndToEnd(t *testing.T) {
	upstream := newUpstream(t)
	defer upstream.Close()
	front, recordsPath := newProxy(t, upstream.URL)

	// (a) OpenAI non-streaming JSON completion.
	status, body := postJSON(t, front.URL+"/v1/chat/completions",
		`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hey"}]}`,
		http.Header{"Authorization": {"Bearer " + testAuthToken}})
	if status != http.StatusOK {
		t.Fatalf("openai request status = %d, want 200", status)
	}
	if body != openAICompletionBody {
		t.Errorf("openai body not passed through verbatim:\n got %q\nwant %q", body, openAICompletionBody)
	}

	// (b) Anthropic SSE stream.
	status, body = postJSON(t, front.URL+"/v1/messages",
		`{"model":"claude-sonnet-4-5-20250929","stream":true,"messages":[]}`,
		http.Header{"X-Api-Key": {testAuthToken}})
	if status != http.StatusOK {
		t.Fatalf("anthropic request status = %d, want 200", status)
	}
	if want := strings.Join(anthropicSSEChunks, ""); body != want {
		t.Errorf("anthropic stream not passed through verbatim:\n got %q\nwant %q", body, want)
	}

	recs := waitForRecords(t, recordsPath, 2)

	oa := recs[0]
	if oa.Provider != "openai" {
		t.Errorf("openai record provider = %q (sniffed from body), want openai", oa.Provider)
	}
	if oa.Model != "gpt-4o-mini-2024-07-18" {
		t.Errorf("openai record model = %q, want gpt-4o-mini-2024-07-18", oa.Model)
	}
	if oa.Path != "/v1/chat/completions" || oa.Status != 200 || oa.Project != "e2e" {
		t.Errorf("openai record path/status/project = %q/%d/%q", oa.Path, oa.Status, oa.Project)
	}
	if oa.InputTokens != 90 || oa.OutputTokens != 25 || oa.CachedTokens != 10 {
		t.Errorf("openai record tokens in/out/cached = %d/%d/%d, want 90/25/10",
			oa.InputTokens, oa.OutputTokens, oa.CachedTokens)
	}
	if oa.Unknown {
		t.Error("openai record marked unknown, want parsed usage")
	}
	if oa.LatencyMS < 10 {
		t.Errorf("openai record latency = %dms, want >= 10 (upstream sleeps %v)", oa.LatencyMS, upstreamSleep)
	}
	// (90*0.15 + 25*0.60 + 10*0.075) / 1e6
	if !oa.Priced || math.Abs(oa.CostUSD-29.25e-6) > 1e-12 {
		t.Errorf("openai record cost = %v (priced=%v), want 2.925e-05 (true)", oa.CostUSD, oa.Priced)
	}

	an := recs[1]
	if an.Provider != "anthropic" {
		t.Errorf("anthropic record provider = %q, want anthropic", an.Provider)
	}
	if an.Model != "claude-sonnet-4-5-20250929" {
		t.Errorf("anthropic record model = %q", an.Model)
	}
	if an.InputTokens != 25 || an.OutputTokens != 15 || an.CachedTokens != 100 || an.CacheCreationTokens != 50 {
		t.Errorf("anthropic record tokens in/out/cached/cachew = %d/%d/%d/%d, want 25/15/100/50",
			an.InputTokens, an.OutputTokens, an.CachedTokens, an.CacheCreationTokens)
	}
	if an.Unknown || an.Status != 200 {
		t.Errorf("anthropic record unknown=%v status=%d", an.Unknown, an.Status)
	}
	if an.LatencyMS < 10 {
		t.Errorf("anthropic record latency = %dms, want >= 10", an.LatencyMS)
	}
	// (25*3 + 15*15 + 100*0.30 + 50*3.75) / 1e6
	if !an.Priced || math.Abs(an.CostUSD-517.5e-6) > 1e-12 {
		t.Errorf("anthropic record cost = %v (priced=%v), want 5.175e-04 (true)", an.CostUSD, an.Priced)
	}

	// The auth token must never reach the log file.
	raw, err := os.ReadFile(recordsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testAuthToken) {
		t.Error("auth token leaked into the records file")
	}
	if strings.Contains(string(raw), "Hello") {
		t.Error("response content leaked into the records file")
	}
}

func TestProxyOpenAIStreamWithoutUsage(t *testing.T) {
	upstream := newUpstream(t)
	defer upstream.Close()
	front, recordsPath := newProxy(t, upstream.URL)

	status, body := postJSON(t, front.URL+"/v1/stream-no-usage",
		`{"model":"gpt-4o","stream":true,"messages":[]}`, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("stream body not passed through: %q", body)
	}

	rec := waitForRecords(t, recordsPath, 1)[0]
	if !rec.Unknown {
		t.Error("record without usage chunk should be marked unknown")
	}
	if rec.InputTokens != 0 || rec.OutputTokens != 0 {
		t.Errorf("unknown record tokens = %d/%d, want 0/0", rec.InputTokens, rec.OutputTokens)
	}
	if rec.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o recovered from the request body", rec.Model)
	}
	if rec.Priced {
		t.Error("unknown-usage record must not be priced")
	}
}

func TestProxyUpstreamDown(t *testing.T) {
	// Grab a port that is guaranteed closed.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + l.Addr().String()
	l.Close()

	front, recordsPath := newProxy(t, dead)
	status, _ := postJSON(t, front.URL+"/v1/messages", `{"model":"claude-haiku-4-5"}`, nil)
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}

	rec := waitForRecords(t, recordsPath, 1)[0]
	if rec.Status != http.StatusBadGateway || !rec.Unknown {
		t.Errorf("record status=%d unknown=%v, want 502/true", rec.Status, rec.Unknown)
	}
	if rec.Model != "claude-haiku-4-5" {
		t.Errorf("model = %q, want request-body fallback claude-haiku-4-5", rec.Model)
	}
}

func TestNewValidation(t *testing.T) {
	goodURL, _ := url.Parse("https://api.anthropic.com")
	rec := recorderFunc(func(record.Record) error { return nil })
	tests := []struct {
		name string
		cfg  proxy.Config
	}{
		{"nil upstream", proxy.Config{Recorder: rec}},
		{"nil recorder", proxy.Config{Upstream: goodURL}},
		{"bad scheme", proxy.Config{Upstream: mustParse("ftp://x"), Recorder: rec}},
		{"no host", proxy.Config{Upstream: mustParse("https://"), Recorder: rec}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := proxy.New(tt.cfg); err == nil {
				t.Error("New() succeeded, want error")
			}
		})
	}
	if _, err := proxy.New(proxy.Config{Upstream: goodURL, Recorder: rec}); err != nil {
		t.Errorf("New() with valid config error = %v", err)
	}
}

type recorderFunc func(record.Record) error

func (f recorderFunc) Append(r record.Record) error { return f(r) }

func mustParse(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("mustParse(%q): %v", s, err))
	}
	return u
}
