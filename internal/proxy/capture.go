package proxy

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/Bruhadev45/tokentrace/internal/parse"
)

// bounded captures up to max bytes of a stream as it passes through.
type bounded struct {
	max       int
	buf       bytes.Buffer
	truncated bool
}

func (b *bounded) capture(p []byte) {
	room := b.max - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return
	}
	if len(p) > room {
		p = p[:room]
		b.truncated = true
	}
	b.buf.Write(p)
}

func (b *bounded) bytes() []byte {
	if b == nil {
		return nil
	}
	return b.buf.Bytes()
}

// captureRequestBody arranges for a prefix of the request body to be
// available for the model-name fallback. Small bodies with a known length
// are buffered up front, which also makes them replayable (GetBody) and
// keeps the model available even when the upstream dial fails before the
// body is read; anything larger streams through a best-effort tee.
func captureRequestBody(out *http.Request, m *reqMeta) {
	if out.Body == nil {
		return
	}
	if out.ContentLength >= 0 && out.ContentLength <= maxRequestCapture {
		data, err := io.ReadAll(io.LimitReader(out.Body, maxRequestCapture))
		m.reqBody.capture(data)
		if err != nil {
			// Could not slurp the body: forward what arrived, then
			// surface the same read error to the transport.
			out.Body = io.NopCloser(io.MultiReader(bytes.NewReader(data), errorReader{err: err}))
			return
		}
		out.Body = io.NopCloser(bytes.NewReader(data))
		out.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(data)), nil
		}
		return
	}
	out.Body = &captureReader{rc: out.Body, b: m.reqBody}
}

// errorReader replays a stored read error.
type errorReader struct{ err error }

func (e errorReader) Read([]byte) (int, error) { return 0, e.err }

// captureReader tees a request body into a bounded buffer on its way out.
type captureReader struct {
	rc io.ReadCloser
	b  *bounded
}

func (c *captureReader) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 {
		c.b.capture(p[:n])
	}
	return n, err
}

func (c *captureReader) Close() error {
	if err := c.rc.Close(); err != nil {
		return fmt.Errorf("close request body: %w", err)
	}
	return nil
}

// captureBody relays the response body to the client while feeding a copy
// to the usage parser, then finishes exactly one record when the body ends
// — on EOF, on a transport error, or on Close (client disconnect included).
type captureBody struct {
	rc     io.ReadCloser
	status int
	meta   *reqMeta
	proxy  *proxy

	sse     *parse.SSEAccumulator // streaming: incremental, O(1) memory
	body    *bounded              // non-streaming: bounded buffered copy
	gzipped bool
	isSSE   bool

	once sync.Once
}

func (c *captureBody) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 {
		if c.sse != nil {
			_, _ = c.sse.Write(p[:n]) // never fails
		} else {
			c.body.capture(p[:n])
		}
	}
	if err != nil {
		// io.EOF or a mid-stream failure: either way the body is over.
		c.finish()
	}
	return n, err
}

func (c *captureBody) Close() error {
	c.finish()
	if err := c.rc.Close(); err != nil {
		return fmt.Errorf("close response body: %w", err)
	}
	return nil
}

func (c *captureBody) finish() {
	c.once.Do(func() { c.proxy.finishRecord(c) })
}

// extract recovers usage from whatever was captured. It never fails: an
// unparseable body simply yields Usage{Known: false}.
func (c *captureBody) extract(defaultProvider parse.Provider) (parse.Usage, parse.Provider) {
	if c.sse != nil {
		c.sse.Flush()
		return c.sse.Usage(), c.sse.Provider()
	}

	body := c.body.bytes()
	if c.gzipped {
		plain, err := gunzip(body)
		if err != nil {
			return parse.Usage{}, defaultProvider
		}
		body = plain
	} else if c.body.truncated {
		// Usage sits at the end of the body and the end was not kept.
		return parse.Usage{}, defaultProvider
	}

	if c.isSSE {
		a := parse.NewSSEAccumulator(defaultProvider)
		_, _ = a.Write(body)
		a.Flush()
		return a.Usage(), a.Provider()
	}

	u, provider, err := parse.ParseJSON(defaultProvider, body)
	if err != nil {
		return parse.Usage{}, provider
	}
	return u, provider
}

// gunzip decompresses a fully captured gzip body, bounded by the same cap
// as the capture buffer.
func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("gunzip response: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxResponseCapture))
	if err != nil {
		return nil, fmt.Errorf("gunzip response: %w", err)
	}
	return out, nil
}
