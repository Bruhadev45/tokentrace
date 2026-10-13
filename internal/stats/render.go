package stats

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/Bruhadev45/tokentrace/internal/live"
	"github.com/Bruhadev45/tokentrace/internal/record"
)

// Render writes rows plus a TOTAL line as an aligned table, followed by
// footnotes for unpriced or unknown-usage requests when present.
func Render(w io.Writer, by By, rows []Row, total Row) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\tREQS\tINPUT\tOUTPUT\tCACHED\tCOST\tAVG MS\tP95 MS\tHIT %%\n",
		strings.ToUpper(string(by)))
	for _, r := range rows {
		writeRow(tw, r)
	}
	if len(rows) > 1 {
		fmt.Fprintln(tw, strings.Repeat("-", 5)+"\t\t\t\t\t\t\t\t")
		writeRow(tw, total)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("render table: %w", err)
	}
	if total.Unpriced > 0 {
		fmt.Fprintf(w, "* includes %d request(s) with no known price, counted at $0\n", total.Unpriced)
	}
	if total.Unknown > 0 {
		fmt.Fprintf(w, "note: %d request(s) carried no parseable usage (tokens counted as 0)\n", total.Unknown)
	}
	return nil
}

func writeRow(tw *tabwriter.Writer, r Row) {
	cost := fmt.Sprintf("$%.4f", r.Cost)
	if r.Unpriced > 0 {
		cost += "*"
	}
	hit := "-"
	if rate, ok := r.HitRate(); ok {
		hit = fmt.Sprintf("%.1f", rate*100)
	}
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		r.Key,
		comma(int64(r.Requests)),
		comma(r.Input),
		comma(r.Output),
		comma(r.Cached),
		cost,
		comma(r.AvgLatencyMS()),
		comma(r.P95LatencyMS()),
		hit,
	)
}

// FormatRecord renders one record as a single `tokentrace tail` line.
// It prints usage metadata only — never headers or content.
func FormatRecord(r record.Record) string {
	model := r.Model
	if model == "" {
		model = "(unknown)"
	}
	tokens := fmt.Sprintf("in=%s out=%s cached=%s", comma(r.InputTokens), comma(r.OutputTokens), comma(r.CachedTokens))
	if r.Unknown {
		tokens = "tokens=unknown"
	}
	cost := fmt.Sprintf("$%.4f", r.CostUSD)
	if !r.Priced {
		cost = "$-"
	}
	return fmt.Sprintf("%s  %-9s %3d %7s  %-30s %-34s %s  %s",
		r.Time.Local().Format("15:04:05"),
		r.Provider,
		r.Status,
		comma(r.LatencyMS)+"ms",
		r.Project,
		model,
		tokens,
		cost,
	)
}

// FormatLive renders the `tokentrace tail --stats` footer line. A "*"
// after the cost flags requests with no known price, as in the table.
func FormatLive(s live.Snapshot) string {
	p50 := "-"
	if s.PerMinute > 0 {
		p50 = comma(s.P50LatencyMS) + "ms"
	}
	cost := fmt.Sprintf("$%.4f", s.CostUSD)
	if s.Unpriced > 0 {
		cost += "*"
	}
	return fmt.Sprintf("last 60s: %d req/min, p50 %s | since start: %s req, %s",
		s.PerMinute, p50, comma(int64(s.Requests)), cost)
}

// comma formats n with thousands separators ("1234567" -> "1,234,567").
func comma(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
