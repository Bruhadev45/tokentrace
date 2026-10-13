package main

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/record"
	"github.com/Bruhadev45/tokentrace/internal/stats"
)

var footerT0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local)

func footerRecord() record.Record {
	return record.Record{
		Time: footerT0, Project: "app", Provider: "openai", Model: "gpt-4o-mini",
		Path: "/v1/chat/completions", Status: 200, LatencyMS: 1234,
		InputTokens: 1000, OutputTokens: 100, CostUSD: 0.25, Priced: true,
	}
}

// screen replays a byte stream the way a terminal shows it: on each line,
// only what follows the last clearLine survives.
func screen(stream string) []string {
	lines := strings.Split(strings.TrimSuffix(stream, "\n"), "\n")
	for i, l := range lines {
		if j := strings.LastIndex(l, clearLine); j >= 0 {
			lines[i] = l[j+len(clearLine):]
		}
	}
	return lines
}

func TestLiveFooterStaysOnTheLastLine(t *testing.T) {
	var buf bytes.Buffer // stdout and stderr on the same terminal
	f := newLiveFooter(&buf, &buf, func() time.Time { return footerT0 })
	stop := f.start(time.Hour)
	f.add(footerRecord())
	stop()

	rec := stats.FormatRecord(footerRecord())
	empty := "last 60s: 0 req/min, p50 - | since start: 0 req, $0.0000"
	one := "last 60s: 1 req/min, p50 1,234ms | since start: 1 req, $0.2500"
	wantStream := clearLine + empty +
		clearLine + rec + "\n" + clearLine + one +
		clearLine + one + "\n"
	if got := buf.String(); got != wantStream {
		t.Errorf("stream =\n%q\nwant\n%q", got, wantStream)
	}

	wantScreen := []string{rec, one}
	if got := screen(buf.String()); strings.Join(got, "\n") != strings.Join(wantScreen, "\n") {
		t.Errorf("screen =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantScreen, "\n"))
	}
}

func TestLiveFooterKeepsStdoutClean(t *testing.T) {
	var out, term bytes.Buffer
	f := newLiveFooter(&out, &term, func() time.Time { return footerT0 })
	stop := f.start(time.Hour)
	f.add(footerRecord())
	stop()

	if want := stats.FormatRecord(footerRecord()) + "\n"; out.String() != want {
		t.Errorf("stdout = %q, want only the record line %q", out.String(), want)
	}
	if strings.Contains(term.String(), "gpt-4o-mini") {
		t.Errorf("record text leaked onto the footer stream: %q", term.String())
	}
}

// syncBuffer is a bytes.Buffer safe to read while the redraw goroutine writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLiveFooterRedrawsWhileIdle(t *testing.T) {
	var nowNS atomic.Int64
	nowNS.Store(footerT0.UnixNano())
	clock := func() time.Time { return time.Unix(0, nowNS.Load()) }

	var term syncBuffer
	f := newLiveFooter(io.Discard, &term, clock)
	f.add(footerRecord())
	stop := f.start(5 * time.Millisecond)
	defer stop()

	// Two idle minutes later the rate must decay without any new record.
	nowNS.Store(footerT0.Add(2 * time.Minute).UnixNano())
	want := "last 60s: 0 req/min, p50 - | since start: 1 req, $0.2500"
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(term.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("footer never redrew to %q; stream: %q", want, term.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
