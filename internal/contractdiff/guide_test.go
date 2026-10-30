package contractdiff

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestRules_matchTheGuide checks the guide's table of rules against the catalog: every rule is
// listed once, and every rating a fixture pair gives a rule is a rating its row names.
func TestRules_matchTheGuide(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "content", "docs", "_index.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(string(page), "\n### Rules\n")
	if !ok {
		t.Fatal("the guide has no Rules section")
	}
	table, _, _ = strings.Cut(table, "\n## ")

	ratings := map[string]string{}
	id := regexp.MustCompile("`([A-Z_]+)`")
	for line := range strings.Lines(table) {
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			continue
		}
		for _, m := range id.FindAllStringSubmatch(cells[1], -1) {
			if _, dup := ratings[m[1]]; dup {
				t.Errorf("the guide lists %s twice", m[1])
			}
			ratings[m[1]] = cells[3]
		}
	}
	for _, rule := range Rules() {
		if _, ok := ratings[rule]; !ok {
			t.Errorf("the guide's Rules table leaves out %s", rule)
		}
	}
	for rule := range ratings {
		if !KnownRule(rule) {
			t.Errorf("the guide's Rules table lists %s, which is not a rule", rule)
		}
	}

	var src []byte
	for _, path := range []string{"diff_test.go", "kinds_test.go", "../codegen/diff_test.go", "../codegen/diff_agent_test.go"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, withoutStabilityTests(string(b))...)
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(breaking|possibly_breaking|expected|safe) ([A-Z_]+) `).FindAllSubmatch(src, -1) {
		severity, rule := string(m[1]), string(m[2])
		if seen[severity+rule] {
			continue
		}
		seen[severity+rule] = true
		row, ok := ratings[rule]
		if !ok {
			continue
		}
		if !slices.Contains(rowRatings(row), severity) {
			t.Errorf("a fixture rates %s %s, but the guide's row says %q", rule, severity, strings.TrimSpace(row))
		}
	}
}

// withoutStabilityTests drops the tests whose fixtures lower ratings for stability, which the
// guide describes once for every rule rather than in each row.
func withoutStabilityTests(src string) string {
	for _, name := range []string{"TestDiff_stabilityAdjustsSeverity", "TestDiff_definitionStability"} {
		before, rest, ok := strings.Cut(src, "\nfunc "+name+"(")
		if !ok {
			continue
		}
		_, after, _ := strings.Cut(rest, "\nfunc ")
		src = before + "\nfunc " + after
	}
	return src
}

// rowRatings returns the severities a rating cell names.
func rowRatings(cell string) []string {
	var out []string
	if strings.Contains(cell, "possibly breaking") {
		out = append(out, "possibly_breaking")
	}
	if strings.Contains(strings.ReplaceAll(cell, "possibly breaking", ""), "breaking") {
		out = append(out, "breaking")
	}
	for _, s := range []string{"expected", "safe"} {
		if strings.Contains(cell, s) {
			out = append(out, s)
		}
	}
	return out
}
