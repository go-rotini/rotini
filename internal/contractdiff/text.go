package contractdiff

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// headings name each severity's group in the text report, most severe first.
var headings = []struct {
	sev   Severity
	title string
}{
	{Breaking, "Breaking"},
	{PossiblyBreaking, "Possibly breaking"},
	{Expected, "Expected"},
	{Safe, "Safe"},
}

// WriteText writes the report for people: the findings grouped by severity, the accepted ones
// last with their reasons, then a summary line.
func (r Report) WriteText(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	group := func(title string, keep func(Finding) bool, tail func(Finding) string) {
		first := true
		for _, f := range r.Findings {
			if !keep(f) {
				continue
			}
			if first {
				fmt.Fprintf(tw, "%s:\n", title)
				first = false
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s%s\n", f.Rule, f.Where, f.Message, tail(f))
		}
		if !first {
			fmt.Fprintln(tw)
		}
	}
	note := func(f Finding) string {
		if f.Note == "" {
			return ""
		}
		return " (" + f.Note + ")"
	}
	for _, h := range headings {
		group(h.title, func(f Finding) bool { return f.Accepted == nil && f.Severity == h.sev }, note)
	}
	group("Accepted", func(f Finding) bool { return f.Accepted != nil }, func(f Finding) string {
		return " (" + string(f.Severity) + "; " + f.Accepted.Reason + ")"
	})
	fmt.Fprintln(tw, r.Summary.line())
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write the report: %w", err)
	}
	return nil
}

// line is the summary line: "2 breaking, 1 possibly breaking, 0 expected, 4 safe, 1 accepted",
// or "no changes".
func (s Summary) line() string {
	if s == (Summary{}) {
		return "no changes"
	}
	parts := []string{
		fmt.Sprintf("%d breaking", s.Breaking),
		fmt.Sprintf("%d possibly breaking", s.PossiblyBreaking),
		fmt.Sprintf("%d expected", s.Expected),
		fmt.Sprintf("%d safe", s.Safe),
		fmt.Sprintf("%d accepted", s.Accepted),
	}
	return strings.Join(parts, ", ")
}
