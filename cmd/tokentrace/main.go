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
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Bruhadev45/tokentrace/internal/config"
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
  tokentrace serve [ALIAS|URL] [--upstream ALIAS|URL] [--port 4141] [--project NAME]
  tokentrace stats [--project NAME] [--since 24h|7d|2026-08-01] [--until ...]
                   [--by model|project|day] [--json|--csv] [--out FILE]
  tokentrace tail [--project NAME] [--stats]
  tokentrace prices
  tokentrace version

Built-in upstream aliases: anthropic, openai (e.g. tokentrace serve anthropic).
Records live in ~/.tokentrace/records.jsonl (override dir: TOKENTRACE_DIR);
aliases and serve defaults can be set in config.toml in the same directory.
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

// serveOptions is what serve runs with once flags and config are merged.
type serveOptions struct {
	upstream *url.URL
	port     int
	project  string
}

func cmdServe(args []string, stderr io.Writer) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	opts, err := parseServeArgs(args, cfg, stderr)
	if err != nil {
		return err
	}
	u := opts.upstream

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
		Project:  opts.project,
		Recorder: store,
		Logf:     logf,
	})
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()
	addr := fmt.Sprintf("127.0.0.1:%d", opts.port)
	logf("tokentrace %s proxying http://%s -> %s (project=%s)", version, addr, u, opts.project)
	logf("recording usage to %s (Ctrl-C to stop)", store.Path())
	return proxy.ListenAndServe(ctx, addr, handler, logf)
}

// loadConfig reads config.toml from the records directory, if present.
func loadConfig() (config.Config, error) {
	dir, err := record.Dir()
	if err != nil {
		return config.Config{}, err
	}
	return config.Load(filepath.Join(dir, config.FileName))
}

// parseServeArgs merges serve's flags over the config defaults. The
// upstream may be given as a leading positional alias/URL ("serve
// anthropic --port 4142"), a trailing one, or --upstream — but only once.
func parseServeArgs(args []string, cfg config.Config, stderr io.Writer) (serveOptions, error) {
	var positional string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	upstream := fs.String("upstream", "", "upstream alias (anthropic, openai, or one from config.toml) or base URL")
	port := fs.Int("port", cfg.Defaults.Port, "local port to listen on")
	project := fs.String("project", cfg.Defaults.Project, "project label stamped on every record")
	if err := fs.Parse(args); err != nil {
		return serveOptions{}, err
	}
	switch {
	case fs.NArg() == 1 && positional == "":
		positional = fs.Arg(0)
	case fs.NArg() > 0:
		return serveOptions{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if positional != "" && *upstream != "" {
		return serveOptions{}, errors.New("give the upstream once: as an argument or with --upstream, not both")
	}
	target := *upstream
	if target == "" {
		target = positional
	}
	if target == "" {
		target = cfg.Defaults.Upstream
	}
	if target == "" {
		fs.Usage()
		return serveOptions{}, errors.New("--upstream is required (or name an alias: tokentrace serve anthropic)")
	}
	u, err := cfg.ResolveUpstream(target)
	if err != nil {
		return serveOptions{}, err
	}
	if *port < 1 || *port > 65535 {
		return serveOptions{}, fmt.Errorf("--port out of range: %d", *port)
	}
	return serveOptions{upstream: u, port: *port, project: *project}, nil
}

func cmdStats(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "only include records for this project")
	since := fs.String("since", "", `only include records from this point on ("30m", "24h", "7d" or "2026-08-01")`)
	until := fs.String("until", "", `only include records before this (same forms as --since; exclusive)`)
	by := fs.String("by", "model", "group by: model, project or day")
	asJSON := fs.Bool("json", false, "write JSON instead of the terminal table")
	asCSV := fs.Bool("csv", false, "write CSV instead of the terminal table")
	out := fs.String("out", "", "write the report to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *asJSON && *asCSV {
		return errors.New("--json and --csv are mutually exclusive")
	}
	format := stats.FormatTable
	switch {
	case *asJSON:
		format = stats.FormatJSON
	case *asCSV:
		format = stats.FormatCSV
	}
	groupBy, err := stats.ParseBy(*by)
	if err != nil {
		return err
	}
	now := time.Now()
	sinceT, err := stats.ParseSince(*since, now)
	if err != nil {
		return err
	}
	untilT, err := stats.ParseUntil(*until, now)
	if err != nil {
		return err
	}
	if !sinceT.IsZero() && !untilT.IsZero() && !untilT.After(sinceT) {
		return fmt.Errorf("--until (%s) must be later than --since (%s)",
			untilT.Format(time.DateTime), sinceT.Format(time.DateTime))
	}
	path, err := record.DefaultPath()
	if err != nil {
		return err
	}

	rows, total, skipped, err := stats.Aggregate(path, groupBy, stats.Filter{
		Project: *project,
		Since:   sinceT,
		Until:   untilT,
	})
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	if missing {
		// No log file yet: report zero rows rather than a half-built total.
		rows, total = nil, stats.Row{Key: "TOTAL"}
	}

	// The machine formats always emit a well-formed document, empty log or
	// not, so scripts never have to special-case "no records yet".
	if format == stats.FormatTable {
		if missing {
			fmt.Fprintf(stdout, "no records yet (looked in %s) — start the proxy with `tokentrace serve` first\n", path)
			return nil
		}
		if total.Requests == 0 {
			fmt.Fprintln(stdout, "no records match the given filters")
			return nil
		}
	}

	w, done, err := openOutput(*out, stdout)
	if err != nil {
		return err
	}
	if err := writeReport(w, format, groupBy, rows, total); err != nil {
		_ = done(false)
		return err
	}
	if err := done(true); err != nil {
		return err
	}
	if *out != "" {
		fmt.Fprintf(stderr, "wrote %d row(s) to %s\n", len(rows), *out)
	}
	if skipped > 0 {
		fmt.Fprintf(stderr, "warning: skipped %d unparseable line(s) in %s\n", skipped, path)
	}
	return nil
}

func writeReport(w io.Writer, format stats.Format, by stats.By, rows []stats.Row, total stats.Row) error {
	switch format {
	case stats.FormatJSON:
		return stats.RenderJSON(w, by, rows, total)
	case stats.FormatCSV:
		return stats.RenderCSV(w, rows)
	default:
		return stats.Render(w, by, rows, total)
	}
}

// openOutput returns the writer for a report plus a done func that closes
// an opened file. done(false) discards the close error on an earlier
// failure; done(true) reports it, since a failed close means a short write.
func openOutput(path string, stdout io.Writer) (io.Writer, func(ok bool) error, error) {
	if path == "" {
		return stdout, func(bool) error { return nil }, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open --out: %w", err)
	}
	return f, func(ok bool) error {
		cerr := f.Close()
		if !ok || cerr == nil {
			return nil
		}
		return fmt.Errorf("close --out: %w", cerr)
	}, nil
}

func cmdTail(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "only show records for this project")
	withStats := fs.Bool("stats", false, "keep a live footer on stderr: req/min and p50 latency over the last 60s, running cost")
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

	show := func(r record.Record) { fmt.Fprintln(stdout, stats.FormatRecord(r)) }
	if *withStats {
		footer := newLiveFooter(stdout, stderr, time.Now)
		stopFooter := footer.start(footerRedrawInterval)
		defer stopFooter()
		show = footer.add
	}
	return record.Follow(ctx, path, record.DefaultFollowInterval, func(r record.Record) {
		if *project != "" && r.Project != *project {
			return
		}
		show(r)
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
