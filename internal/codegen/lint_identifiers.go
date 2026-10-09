package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// Identifier rules that look across a command chain: a flag hiding an ancestor's, a one-dash
// word the parser could also read as a bundle of short flags, and a flag with no long form.

// commandFlags returns the flags a command declares itself.
func commandFlags(c *Command) []FlagInput { return c.Flags }

// isBoolFlag reports whether a flag is a bool switch, which takes no value.
func isBoolFlag(f FlagInput) bool { return getSchemaType(f.Schema) == "bool" }

// isCountFlag reports whether a flag counts its occurrences, which takes no value.
func isCountFlag(f FlagInput) bool { return f.Schema != nil && f.Schema.Type == "count" }

// longForm names a flag for a message by its first long identifier, else its first one.
func longForm(f FlagInput) string {
	ids := flagIdentifiers(f)
	for _, id := range ids {
		if strings.HasPrefix(id, "--") {
			return id
		}
	}
	return ids[0]
}

// ancestorFlag is a cascading or short-circuit flag of an ancestor, reachable after the
// descendant's name.
type ancestorFlag struct {
	flag FlagInput
	path string // the declaring command's path
}

// lintShadowedIdentifiers reports a flag that reuses an identifier of a cascading or
// short-circuit ancestor flag. After the descendant's name the parser searches the chain from
// the leaf, so the identifier then means the descendant's flag. A flag redeclaring the same
// switch (same name, and the same type unless the ancestor short-circuits) is the documented
// override and is skipped. It is an error when every identifier of the ancestor flag is taken,
// since the ancestor flag then can't be given after the descendant's name at all, and a warning
// when some still reach it.
func lintShadowedIdentifiers(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		if len(chain) < 2 {
			return
		}
		owners := ancestorOwners(chain)
		if len(owners) == 0 {
			return
		}
		leaf := chain[len(chain)-1]
		hits, taken := shadowHits(leaf, owners)
		for _, h := range hits {
			problems = append(problems, shadowProblem(h, leaf, taken[h.owner], path, ptr))
		}
	})
	return problems
}

// ancestorOwners maps each identifier of a cascading or short-circuit ancestor flag in chain,
// negated forms included, to the nearest ancestor flag declaring it.
func ancestorOwners(chain []*Command) map[string]*ancestorFlag {
	owners := map[string]*ancestorFlag{}
	paths := chainPaths(chain)
	for i, a := range chain[:len(chain)-1] {
		for _, f := range commandFlags(a) {
			if !f.Cascading && !f.ShortCircuit {
				continue
			}
			owner := &ancestorFlag{flag: f, path: paths[i]}
			for _, id := range flagIdentifiers(f) {
				owners[id] = owner
			}
			for _, neg := range negatedForms(f) {
				owners[neg] = owner
			}
		}
	}
	return owners
}

// shadowHit is one of the leaf's flags taking identifiers of one ancestor flag.
type shadowHit struct {
	flag  int // index in the leaf's flags
	owner *ancestorFlag
	ids   []string
}

// shadowHits lists, per leaf flag and ancestor flag, the identifiers the leaf's flags take
// from ancestors, and every identifier of each ancestor flag that is taken.
func shadowHits(leaf *Command, owners map[string]*ancestorFlag) (hits []*shadowHit, taken map[*ancestorFlag]map[string]bool) {
	taken = map[*ancestorFlag]map[string]bool{}
	for i, f := range commandFlags(leaf) {
		byOwner := map[*ancestorFlag]*shadowHit{}
		for _, id := range flagIdentifiers(f) {
			o := owners[id]
			if o == nil || redeclares(f, o.flag) {
				continue
			}
			h := byOwner[o]
			if h == nil {
				h = &shadowHit{flag: i, owner: o}
				byOwner[o] = h
				hits = append(hits, h)
			}
			h.ids = append(h.ids, id)
			if taken[o] == nil {
				taken[o] = map[string]bool{}
			}
			taken[o][id] = true
		}
	}
	return hits, taken
}

// shadowProblem phrases one shadowing: an error when no identifier of the ancestor flag is
// left after the leaf's name, a warning naming the ones that still reach it otherwise.
func shadowProblem(h *shadowHit, leaf *Command, taken map[string]bool, path, ptr string) *problem {
	f, a := leaf.Flags[h.flag], h.owner.flag
	var remaining []string
	for _, id := range flagIdentifiers(a) {
		if !taken[id] {
			remaining = append(remaining, id)
		}
	}
	does := "cascades"
	if a.ShortCircuit {
		does = "short-circuits"
	}
	subject, it := fmt.Sprintf("identifier %q is", h.ids[0]), "it means"
	if len(h.ids) > 1 {
		subject, it = fmt.Sprintf("identifiers %s are", quotedList(h.ids)), "they mean"
	}
	fix := "Use another identifier for " + longForm(f)
	if f.Name == a.Name {
		fix = fmt.Sprintf("Declare it as a %s to redeclare the same switch, or rename it", getSchemaType(a.Schema))
	}
	msg := fmt.Sprintf("%s also flag %q on command %s, which %s; after %q %s this flag instead, ",
		subject, a.Name, h.owner.path, does, leaf.Name, it)
	p := inputProblem(fmt.Sprintf("%s/flags/%d", ptr, h.flag), path, "flag", f.Name, "")
	if len(remaining) == 0 {
		p.msg += msg + fmt.Sprintf("so flag %q can't be given there at all, only before %q. %s", a.Name, leaf.Name, fix)
		return p
	}
	p.msg += msg + fmt.Sprintf("and only %s still %s flag %q there. %s", quotedList(remaining), reach(len(remaining)), a.Name, fix)
	p.sev = severityWarning
	return p
}

// reach conjugates "reach" for n subjects.
func reach(n int) string {
	if n == 1 {
		return "reaches"
	}
	return "reach"
}

// redeclares reports whether f is the documented redeclaration of ancestor flag a: the same
// name, and the same type unless a short-circuits (a help declared as a string can't ask for
// help).
func redeclares(f, a FlagInput) bool {
	if f.Name != a.Name {
		return false
	}
	return getSchemaType(f.Schema) == getSchemaType(a.Schema) || !a.ShortCircuit
}

// chainPaths returns the display path of each command in chain, as walkCommandsAt spells it.
func chainPaths(chain []*Command) []string {
	out := make([]string, len(chain))
	for i, c := range chain {
		seg := c.Name
		if seg == "" {
			seg = c.Ref
		}
		if i == 0 {
			if seg == "" {
				seg = "(root)"
			}
			out[i] = seg
			continue
		}
		out[i] = out[i-1] + "/" + seg
	}
	return out
}

// clusterReading is how the parser would read a one-dash word as a bundle of short flags.
type clusterReading struct {
	shorts []string // the short flags set, in order
	value  string   // the value the last short takes, when it takes one
	valued bool     // whether the last short takes a value
}

// readAsCluster simulates the parser's bundle reading of the one-dash word id against the
// short flags reachable from a chain: each letter must be a declared short, bool and count
// shorts continue, and the first value-taking short ends the bundle with the rest of the word
// as its value. ok is false when some letter is not a declared short.
func readAsCluster(id string, shorts map[string]FlagInput) (r clusterReading, ok bool) {
	body := strings.TrimPrefix(id, "-")
	for k := range len(body) {
		short := "-" + body[k:k+1]
		f, declared := shorts[short]
		if !declared {
			return clusterReading{}, false
		}
		r.shorts = append(r.shorts, short)
		if isBoolFlag(f) || isCountFlag(f) {
			continue
		}
		r.value, r.valued = body[k+1:], true
		return r, true
	}
	return r, true
}

// multiLetterSingleDash reports whether an identifier is one dash followed by several
// characters.
func multiLetterSingleDash(id string) bool {
	return len(id) > 2 && id[0] == '-' && id[1] != '-'
}

// lintSingleDashIdentifiers checks each one-dash identifier of several letters (-name). An
// exact identifier always wins over a bundle of short flags, so when the chain's short flags
// could also spell the word as a bundle (-ab beside -a and -b, or -name beside a value-taking
// -n), that bundle can never be written: an error. Otherwise it is a warning, since POSIX tools
// read such a word as a bundle. An ancestor's identifier is checked against each descendant's
// chain too, since it exact-matches there.
func lintSingleDashIdentifiers(spec *Spec) []error {
	var order []*dashWord
	words := map[string]*dashWord{}
	walkChainsAt(spec, func(chain []*Command, _, ptr string) {
		shorts := chainShorts(chain)
		paths, ptrs := chainPaths(chain), chainPointers(chain, ptr)
		for ci, c := range chain {
			for fi, f := range commandFlags(c) {
				for _, id := range flagIdentifiers(f) {
					if !multiLetterSingleDash(id) {
						continue
					}
					at := fmt.Sprintf("%s/flags/%d", ptrs[ci], fi)
					w := words[at+"\x00"+id]
					if w == nil {
						w = &dashWord{ptr: at, path: paths[ci], flag: f.Name, id: id}
						words[at+"\x00"+id] = w
						order = append(order, w)
					}
					if r, ok := readAsCluster(id, shorts); ok && w.reading == nil {
						w.reading = &r
					}
				}
			}
		}
	})
	problems := make([]error, 0, len(order))
	for _, w := range order {
		problems = append(problems, w.problem())
	}
	return problems
}

// dashWord is one declared one-dash identifier of several letters, and how the parser could
// also read it as a bundle in some chain (nil when it can't).
type dashWord struct {
	ptr, path, flag, id string
	reading             *clusterReading
}

// problem phrases the finding: an error when the word is also a bundle, a warning otherwise.
func (w *dashWord) problem() *problem {
	long := "--" + strings.TrimPrefix(w.id, "-")
	p := inputProblem(w.ptr, w.path, "flag", w.flag, "")
	switch r := w.reading; {
	case r == nil:
		p.msg += fmt.Sprintf("identifier %q has one dash and several letters, which POSIX tools read as a bundle of short flags; prefer %s, with at most a one-letter short form", w.id, long)
		p.sev = severityWarning
	case r.valued && r.value != "":
		p.msg += fmt.Sprintf("identifier %q also reads as %s with the value %q; the exact identifier always wins, so %s can't be given a value starting %q attached. Use %s",
			w.id, strings.Join(r.shorts, " "), r.value, r.shorts[len(r.shorts)-1], r.value, long)
	default:
		p.msg += fmt.Sprintf("identifier %q is also the bundle %s of declared short flags; the exact identifier always wins, so they can never be bundled as %s. Use %s",
			w.id, strings.Join(r.shorts, " "), w.id, long)
	}
	return p
}

// chainShorts maps each one-letter short identifier reachable in chain to its flag, the
// nearest command's winning, as the parser searches from the leaf.
func chainShorts(chain []*Command) map[string]FlagInput {
	shorts := map[string]FlagInput{}
	for _, c := range chain {
		for _, f := range commandFlags(c) {
			for _, id := range flagIdentifiers(f) {
				if len(id) == 2 && id[0] == '-' && id[1] != '-' {
					shorts[id] = f
				}
			}
		}
	}
	return shorts
}

// chainPointers returns the JSON pointer of each command in chain, given the leaf's pointer.
func chainPointers(chain []*Command, leafPtr string) []string {
	out := make([]string, len(chain))
	out[len(chain)-1] = leafPtr
	for i := len(chain) - 2; i >= 0; i-- {
		out[i] = out[i+1][:strings.LastIndex(out[i+1], "/commands/")]
	}
	return out
}

// lintShortOnlyFlags warns about a flag with only short identifiers: a long form is what
// scripts and readers of help understand. Negatable flags (lintNegatable requires a long form)
// and hidden flags (not part of the documented surface) are skipped.
func lintShortOnlyFlags(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, f := range commandFlags(c) {
			if f.Hidden || negatable(f.Schema) {
				continue
			}
			ids := flagIdentifiers(f)
			if slices.ContainsFunc(ids, func(id string) bool { return strings.HasPrefix(id, "--") }) {
				continue
			}
			what := "a short identifier"
			if len(ids) > 1 {
				what = "short identifiers"
			}
			p := inputProblem(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name,
				fmt.Sprintf("has only %s (%s); add a long form such as --%s, which scripts and readers of help can understand",
					what, strings.Join(ids, ", "), strings.ReplaceAll(f.Name, "_", "-")))
			p.sev = severityWarning
			problems = append(problems, p)
		}
	})
	return problems
}
