package rotini

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The reference implementations below are the straightforward forms the completion code
// replaced; the tests require the same answers from both.

func filterPrefixCaseRef(candidates []string, prefix string, fold bool) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		name := c
		if before, _, ok := strings.Cut(c, "\t"); ok {
			name = before
		}
		has := strings.HasPrefix(name, prefix) || (fold && foldedPrefix(name, prefix))
		if name != "" && has && !seen[name] {
			seen[name] = true
			out = append(out, c)
		}
	}
	return out
}

func FuzzFilterPrefixCase(f *testing.F) {
	f.Add("--a\tx|--b|--a|-c\ty|\tz||--ab", "--", false)
	f.Add("A|a|ä|Ä\td", "a", true)
	f.Fuzz(func(t *testing.T, joined, prefix string, fold bool) {
		cands := strings.Split(joined, "|")
		for _, n := range []int{1, dedupSmall + 1} { // both dedup strategies
			var in []string
			for range n {
				in = append(in, cands...)
				if len(in) > dedupSmall {
					break
				}
			}
			got, want := filterPrefixCase(in, prefix, fold), filterPrefixCaseRef(in, prefix, fold)
			if !slices.Equal(got, want) {
				t.Fatalf("filterPrefixCase(%q, %q, %v) = %q, want %q", in, prefix, fold, got, want)
			}
		}
	})
}

func flagSpellingsRef(f FlagDef) []string {
	var ids []string
	for _, id := range f.Identifiers {
		if !slices.Contains(f.DeprecatedIdentifiers, id) {
			ids = append(ids, id)
		}
	}
	if f.Negatable {
		live := f
		live.Identifiers = ids
		ids = append(ids, negatedIdentifiers(live)...)
	}
	return ids
}

func groupEffectsRef(frame Command, set map[string]bool) (hidden, needed map[string]bool) {
	hidden, needed = map[string]bool{}, map[string]bool{}
	if len(frame.FlagGroups) == 0 && len(frame.FlagDependencies) == 0 {
		return hidden, needed
	}
	return groupEffects(frame, set)
}

func completeFlagWordRef(cc completionContext, words []string, partial string, lookup HandlerLookup, rtx *Context) completionAnswer {
	if _, _, hasInline := splitFlag(partial); hasInline {
		return completeFlagWord(cc, words, partial, lookup, rtx)
	}
	var required, rest []string
	claimed := map[string]bool{}
	for i, v := range slices.Backward(cc.chain) {
		hidden, needed := groupEffectsRef(ruleFrame(cc.chain, i), cc.setAt(i))
		var mine []string
		for _, f := range v.Flags {
			mine = append(mine, slices.Concat(f.Identifiers, f.HiddenIdentifiers, negatedForms(f))...)
			if !offerFlag(f, cc.setAt(i), hidden) || (f.Local && i != len(cc.chain)-1) {
				continue
			}
			var ids []string
			for _, id := range flagSpellingsRef(f) {
				switch {
				case claimed[id]:
				case slices.Contains(f.Identifiers, id):
					ids = append(ids, withDescription(id, f.Summary))
				default:
					ids = append(ids, id)
				}
			}
			if !f.ShortCircuit && !cc.setAt(i)[f.Name] && (f.Required || needed[f.Name]) {
				required = append(required, ids...)
			} else {
				rest = append(rest, ids...)
			}
		}
		for _, id := range mine {
			claimed[id] = true
		}
	}
	required = filterPrefixCaseRef(required, partial, false)
	sort.Strings(required)
	rest = filterPrefixCaseRef(rest, partial, false)
	sort.Strings(rest)
	out := filterPrefixCaseRef(append(required, rest...), partial, false)
	return completionAnswer{cands: out, keepOrder: !slices.IsSorted(out)}
}

func TestCompleteFlagWord_matchesReference(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool", Summary: "a talk\nmore"},
			{Name: "color", Identifiers: []string{"--color", "--colour"}, DeprecatedIdentifiers: []string{"--colour"}, Type: "bool", Negatable: true, Summary: "paint"},
			{Name: "local", Identifiers: []string{"--local"}, Type: "string", Local: true, Summary: "root only"},
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true},
			{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string", Summary: "\x1b[1mlabel\x1b[0m"},
			{Name: "js", Identifiers: []string{"--js"}, Type: "bool", Summary: "root js"},
			{Name: "quiet", Identifiers: []string{"-q", "--quiet"}, Type: "bool", Summary: "root quiet"},
		},
		Commands: []CommandDef{{
			Name: "db", Handler: "Db",
			Flags: []FlagDef{
				{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "count", Summary: "louder"},
				{Name: "json", Identifiers: []string{"--json"}, HiddenIdentifiers: []string{"--js"}, Type: "bool", Summary: "as JSON"},
				{Name: "yaml", Identifiers: []string{"--yaml"}, Type: "bool", Negatable: true, Negation: "--plain"},
				{Name: "host", Identifiers: []string{"-H", "--host"}, Type: "string", Required: true, Summary: "where"},
				{Name: "port", Identifiers: []string{"--port"}, Type: "int"},
				{Name: "quiet", Identifiers: []string{"-q", "--quiet"}, Type: "bool", Summary: "db quiet"},
				{Name: "user", Identifiers: []string{"--user"}, Type: "string", Hidden: true},
				{Name: "old", Identifiers: []string{"--old"}, Type: "string", Deprecated: "gone"},
			},
			FlagGroups: []FlagGroup{
				{Kind: FlagGroupMutuallyExclusive, Flags: []string{"json", "yaml"}},
				{Kind: FlagGroupRequiredTogether, Flags: []string{"port", "user"}},
			},
			FlagDependencies: []FlagDependency{{When: "port", Requires: []string{"host"}, Forbids: []string{"json"}}},
			Commands: []CommandDef{{Name: "migrate", Handler: "Migrate", Flags: []FlagDef{
				{Name: "dry", Identifiers: []string{"-n", "--dry-run"}, Type: "bool", Summary: "pretend"},
				{Name: "json", Identifiers: []string{"--json"}, Type: "string", Summary: "shadowing"},
			}}},
		}},
	}
	befores := [][]string{
		nil, {"db"}, {"db", "--json"}, {"db", "--port", "1"}, {"db", "--yaml"}, {"db", "-v"},
		{"db", "migrate"}, {"db", "migrate", "-n"}, {"--color", "db"}, {"db", "--host", "x", "migrate"}, {"db", "-q", "migrate"}, {"db", "-q"},
	}
	partials := []string{"-", "--", "--c", "--no", "--j", "-v", "--h", "--p", "--d", "--x", "-H", "--js", "--tag", "-q", "--q", ""}
	for _, before := range befores {
		cc := walkContext(def, before)
		for _, partial := range partials {
			if partial == "" {
				continue
			}
			words := append(slices.Clone(before), partial)
			got := completeFlagWord(cc, words, partial, nil, nil)
			want := completeFlagWordRef(cc, words, partial, nil, nil)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("before %q, partial %q:\n got  %q\n want %q", before, partial, got.cands, want.cands)
			}
		}
	}
}
