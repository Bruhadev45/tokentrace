package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/live"
	"github.com/Bruhadev45/tokentrace/internal/record"
	"github.com/Bruhadev45/tokentrace/internal/stats"
)

// footerRedrawInterval keeps the footer moving while no records arrive, so
// the 60s rate and median visibly decay when traffic stops.
const footerRedrawInterval = time.Second

// clearLine returns the cursor to column 0 and erases the whole line.
const clearLine = "\r\x1b[2K"

// liveFooter interleaves `tail` record lines with a stats footer that is
// always the terminal's last line. Records go to out; the footer goes to
// term (stderr), so piping stdout still yields clean record lines.
type liveFooter struct {
	mu      sync.Mutex
	out     io.Writer
	term    io.Writer
	now     func() time.Time
	tracker live.Tracker
}

func newLiveFooter(out, term io.Writer, now func() time.Time) *liveFooter {
	return &liveFooter{out: out, term: term, now: now}
}

// add prints r above the footer and redraws the footer with r counted.
func (f *liveFooter) add(r record.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tracker.Add(r, f.now())
	fmt.Fprint(f.term, clearLine)
	fmt.Fprintln(f.out, stats.FormatRecord(r))
	f.drawLocked()
}

func (f *liveFooter) redraw() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drawLocked()
}

func (f *liveFooter) drawLocked() {
	fmt.Fprint(f.term, clearLine+stats.FormatLive(f.tracker.Snapshot(f.now())))
}

// start draws the footer and redraws it every interval until the returned
// stop func is called. stop draws it one last time and ends the line, so
// the final numbers stay on screen above the shell prompt.
func (f *liveFooter) start(every time.Duration) (stop func()) {
	f.redraw()
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				f.redraw()
			}
		}
	}()
	return func() {
		close(quit)
		<-done
		f.mu.Lock()
		defer f.mu.Unlock()
		f.drawLocked()
		fmt.Fprintln(f.term)
	}
}
