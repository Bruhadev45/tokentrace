// Command tokentrace is a local reverse proxy that records LLM API token
// usage, latency and cache hits per request, plus terminal reports over
// the recorded log. Know what every prompt costs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/pricing"
	"github.com/Bruhadev45/tokentrace/internal/proxy"
	"github.com/Bruhadev45/tokentrace/internal/record"
	"github.com/Bruhadev45/tokentrace/internal/stats"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

const usageText = `tokentrace — know what every prompt costs.

A local reverse proxy that records token usage, latency and cache hits for
every LLM API request, to an append-only JSONL log on your machine.

Usage:
  tokentrace serve --upstream URL [--port 4141] [--project NAME]
  tokentrace stats [--project NAME] [--since 24h|7d|2026-08-01] [--by model|project|day]
  tokentrace tail
  tokentrace prices
  tokentrace version

Records live in ~/.tokentrace/records.jsonl (override dir: TOKENTRACE_DIR).
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "tokentrace:", err)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return errors.New("missing command")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return cmdServe(rest, stderr)
	case "stats":
		return cmdStats(rest, stdout, stderr)
	case "tail":
		return cmdTail(rest, stdout, stderr)
	case "prices":
		return cmdPrices(stdout)
	case "version":
		fmt.Fprintln(stdout, "tokentrace", version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return nil
	default:
		fmt.Fprint(stderr, usageText)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// signalContext is cancelled by Ctrl-C or SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func cmdServe(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	upstream := fs.String("upstream", "", "upstream base URL, e.g. https://api.anthropic.com (required)")
	port := fs.Int("port", 4141, "local port to listen on")
	project := fs.String("project", "default", "project label stamped on every record")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *upstream == "" {
		fs.Usage()
		return errors.New("--upstream is required")
	}
	u, err := url.Parse(*upstream)
	if err != nil {
		return fmt.Errorf("parse --upstream: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("--upstream must be an absolute URL like https://api.openai.com, got %q", *upstream)
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("--port out of range: %d", *port)
	}

	dir, err := record.Dir()
	if err != nil {
		return err
	}
	store, err := record.Open(dir)
	if err != nil {
		return err
	}
	defer store.Close()

	logf := func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }
	handler, err := proxy.New(proxy.Config{
		Upstream: u,
		Project:  *project,
		Recorder: store,
		Logf:     logf,
	})
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	logf("tokentrace %s proxying http://%s -> %s (project=%s)", version, addr, u, *project)
	logf("recording usage to %s (Ctrl-C to stop)", store.Path())
	return proxy.ListenAndServe(ctx, addr, handler, logf)
}

func cmdStats(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "only include records for this project")
	since := fs.String("since", "", `only include records after this ("30m", "24h", "7d" or "2026-08-01")`)
	by := fs.String("by", "model", "group by: model, project or day")
	if err := fs.Parse(args); err != nil {
		return err
	}
	groupBy, err := stats.ParseBy(*by)
	if err != nil {
		return err
	}
	sinceT, err := stats.ParseSince(*since, time.Now())
	if err != nil {
		return err
	}
	path, err := record.DefaultPath()
	if err != nil {
		return err
	}

	rows, total, skipped, err := stats.Aggregate(path, groupBy, stats.Filter{
		Project: *project,
		Since:   sinceT,
	})
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stdout, "no records yet (looked in %s) — start the proxy with `tokentrace serve` first\n", path)
		return nil
	}
	if err != nil {
		return err
	}
	if total.Requests == 0 {
		fmt.Fprintln(stdout, "no records match the given filters")
		return nil
	}
	if err := stats.Render(stdout, groupBy, rows, total); err != nil {
		return err
	}
	if skipped > 0 {
		fmt.Fprintf(stderr, "warning: skipped %d unparseable line(s) in %s\n", skipped, path)
	}
	return nil
}

func cmdTail(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "only show records for this project")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := record.DefaultPath()
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	fmt.Fprintf(stderr, "following %s (Ctrl-C to stop)\n", path)
	return record.Follow(ctx, path, record.DefaultFollowInterval, func(r record.Record) {
		if *project != "" && r.Project != *project {
			return
		}
		fmt.Fprintln(stdout, stats.FormatRecord(r))
	})
}

func cmdPrices(stdout io.Writer) error {
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL PREFIX\tINPUT\tOUTPUT\tCACHED READ\tCACHE WRITE")
	for _, e := range pricing.Entries() {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			e.Prefix,
			perMTok(e.Price.Input),
			perMTok(e.Price.Output),
			perMTok(e.Price.CachedRead),
			perMTok(e.Price.CacheWrite),
		)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("render prices: %w", err)
	}
	fmt.Fprintln(stdout, "\nUSD per million tokens; longest matching prefix wins; \"-\" falls back to the input rate.")
	fmt.Fprintln(stdout, "Prices are static estimates — verify against your provider's current price list.")
	return nil
}

func perMTok(v float64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("$%.3f", v)
}
