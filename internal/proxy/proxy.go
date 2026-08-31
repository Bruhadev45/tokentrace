// Package proxy implements the recording reverse proxy: it forwards every
// request to a single upstream unchanged, watches the response for token
// usage as it streams back, and appends one usage record per request.
//
// Privacy: request and response bodies are inspected in memory only to
// extract usage metadata. Bodies and headers — including Authorization and
// x-api-key, which pass through untouched — are never logged and never
// written to disk.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/parse"
	"github.com/Bruhadev45/tokentrace/internal/pricing"
	"github.com/Bruhadev45/tokentrace/internal/record"
)

const (
	// maxRequestCapture bounds the request-body prefix kept in memory for
	// the model-name fallback.
	maxRequestCapture = 256 << 10
	// maxResponseCapture bounds the buffered copy of non-streaming
	// responses. SSE streams are parsed incrementally and need no buffer.
	maxResponseCapture = 8 << 20
)

// Recorder receives one record per proxied request.
type Recorder interface {
	Append(record.Record) error
}

// Config configures New.
type Config struct {
	// Upstream is the base URL requests are forwarded to. Required.
	Upstream *url.URL
	// Project is the label stamped on every record. Defaults to "default".
	Project string
	// Recorder persists records. Required.
	Recorder Recorder
	// Logf, when set, receives one line per request: method, path, status,
	// latency and token counts. Never headers, never body content.
	Logf func(format string, args ...any)
}

type proxy struct {
	cfg      Config
	provider parse.Provider
	rp       *httputil.ReverseProxy
}

// New returns the proxy handler for cfg.
func New(cfg Config) (http.Handler, error) {
	if cfg.Upstream == nil {
		return nil, errors.New("proxy: upstream is required")
	}
	if cfg.Upstream.Scheme != "http" && cfg.Upstream.Scheme != "https" {
		return nil, fmt.Errorf("proxy: unsupported upstream scheme %q", cfg.Upstream.Scheme)
	}
	if cfg.Upstream.Host == "" {
		return nil, errors.New("proxy: upstream URL has no host")
	}
	if cfg.Recorder == nil {
		return nil, errors.New("proxy: recorder is required")
	}
	if cfg.Project == "" {
		cfg.Project = "default"
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}

	p := &proxy{cfg: cfg, provider: parse.DetectProviderFromHost(cfg.Upstream.Host)}
	p.rp = &httputil.ReverseProxy{
		Rewrite:        p.rewrite,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.errorHandler,
		// Flush every write so SSE tokens reach the client immediately.
		FlushInterval: -1,
		// The default ErrorLog prints upstream URLs; errors are handled
		// (and recorded) by errorHandler instead, without URL details.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	return p, nil
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.rp.ServeHTTP(w, r)
}

type ctxKey struct{}

// reqMeta travels with the outbound request so the response side can
// finish the record.
type reqMeta struct {
	start   time.Time
	method  string
	path    string
	reqBody *bounded
}

func (p *proxy) rewrite(pr *httputil.ProxyRequest) {
	pr.SetURL(p.cfg.Upstream)
	// Ask upstream for a body we can parse: without an explicit
	// Accept-Encoding the transport negotiates gzip itself and hands both
	// us and the client transparently-decompressed bytes.
	pr.Out.Header.Del("Accept-Encoding")

	m := &reqMeta{
		start:   time.Now(),
		method:  pr.In.Method,
		path:    pr.In.URL.Path,
		reqBody: &bounded{max: maxRequestCapture},
	}
	captureRequestBody(pr.Out, m)
	pr.Out = pr.Out.WithContext(context.WithValue(pr.Out.Context(), ctxKey{}, m))
}

func (p *proxy) modifyResponse(resp *http.Response) error {
	m, ok := resp.Request.Context().Value(ctxKey{}).(*reqMeta)
	if !ok {
		return nil
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	isSSE := strings.HasPrefix(ct, "text/event-stream")
	gzipped := strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip")

	cb := &captureBody{
		rc:     resp.Body,
		status: resp.StatusCode,
		meta:   m,
		proxy:  p,
		isSSE:  isSSE,
	}
	if isSSE && !gzipped {
		// Streaming: parse incrementally, O(1) memory, no buffering.
		cb.sse = parse.NewSSEAccumulator(p.provider)
	} else {
		// JSON (or a gzipped stream, which cannot be split into lines
		// until decompressed): keep a bounded copy and parse at the end.
		cb.body = &bounded{max: maxResponseCapture}
		cb.gzipped = gzipped
	}
	resp.Body = cb
	return nil
}

// statusClientClosedRequest is nginx's convention for "the client went
// away before a response could be sent".
const statusClientClosedRequest = 499

// errorHandler answers 502 when the upstream is unreachable and records
// the failed request. r is the outbound request, so its context has meta.
func (p *proxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	start, path, method := time.Now(), r.URL.Path, r.Method
	var reqBody []byte
	if m, ok := r.Context().Value(ctxKey{}).(*reqMeta); ok {
		start, path, method = m.start, m.path, m.method
		reqBody = m.reqBody.bytes()
	}

	status := http.StatusBadGateway
	if errors.Is(err, context.Canceled) {
		status = statusClientClosedRequest // client went away first
	} else {
		p.cfg.Logf("tokentrace: %s %s: upstream error: %v", method, path, err)
	}
	// A no-op if the client is already gone.
	http.Error(w, "tokentrace: upstream error", status)

	rec := record.Record{
		Time:      start,
		Project:   p.cfg.Project,
		Provider:  string(p.provider),
		Model:     parse.ModelFromRequest(reqBody),
		Path:      path,
		Status:    status,
		LatencyMS: time.Since(start).Milliseconds(),
		Unknown:   true,
	}
	p.append(method, rec)
}

// finishRecord turns a finished response capture into a stored record.
func (p *proxy) finishRecord(c *captureBody) {
	m := c.meta
	u, provider := c.extract(p.provider)
	if provider == parse.Unknown || provider == "" {
		provider = p.provider
	}
	if u.Model == "" {
		u.Model = parse.ModelFromRequest(m.reqBody.bytes())
	}

	rec := record.Record{
		Time:      m.start,
		Project:   p.cfg.Project,
		Provider:  string(provider),
		Model:     u.Model,
		Path:      m.path,
		Status:    c.status,
		LatencyMS: time.Since(m.start).Milliseconds(),
		Unknown:   !u.Known,
	}
	if u.Known {
		rec.InputTokens = u.InputTokens
		rec.OutputTokens = u.OutputTokens
		rec.CachedTokens = u.CachedTokens
		rec.CacheCreationTokens = u.CacheCreationTokens
		rec.CostUSD, rec.Priced = pricing.Cost(
			u.Model, u.InputTokens, u.OutputTokens, u.CachedTokens, u.CacheCreationTokens)
	}
	p.append(m.method, rec)
}

func (p *proxy) append(method string, rec record.Record) {
	if err := p.cfg.Recorder.Append(rec); err != nil {
		p.cfg.Logf("tokentrace: append record: %v", err)
	}
	if rec.Unknown {
		p.cfg.Logf("%s %s %d %dms model=%s tokens=unknown",
			method, rec.Path, rec.Status, rec.LatencyMS, orUnknown(rec.Model))
		return
	}
	p.cfg.Logf("%s %s %d %dms model=%s in=%d out=%d cached=%d cost=$%.4f",
		method, rec.Path, rec.Status, rec.LatencyMS, orUnknown(rec.Model),
		rec.InputTokens, rec.OutputTokens, rec.CachedTokens, rec.CostUSD)
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

// ListenAndServe runs handler h on addr until ctx is cancelled, then shuts
// down gracefully (forcing lingering streams closed after a grace period).
func ListenAndServe(ctx context.Context, addr string, h http.Handler, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", addr, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logf("tokentrace: forcing close after shutdown timeout: %v", err)
		if cerr := srv.Close(); cerr != nil {
			return fmt.Errorf("close server: %w", cerr)
		}
	}
	return nil
}
