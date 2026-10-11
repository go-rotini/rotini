// Package contractdiff compares two versions of a rotini contract document and reports what
// changed for the people and scripts using the CLI: breaking, possibly breaking, expected
// (planned for removal at or below the release being prepared) or safe.
//
// Commands match by path, flags by identifier, arguments by position, environment variables
// by variable and config entries by key. Input and output schemas are compared in opposite
// directions: narrowing what a caller may pass breaks callers, widening what the program
// writes breaks readers. A shared definition is compared once, where it is defined.
package contractdiff

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// Severity is how a change affects the CLI's users.
type Severity string

// The severities, from most to least severe.
const (
	Breaking         Severity = "breaking"
	PossiblyBreaking Severity = "possibly_breaking"
	Expected         Severity = "expected"
	Safe             Severity = "safe"
)

// rank orders severities, most severe first.
func (s Severity) rank() int {
	switch s {
	case Breaking:
		return 0
	case PossiblyBreaking:
		return 1
	case Expected:
		return 2
	default:
		return 3
	}
}

// Finding is one change between the contracts.
type Finding struct {
	Severity Severity    `json:"severity"`
	Rule     string      `json:"rule"`
	Where    string      `json:"where"`
	Message  string      `json:"message"`
	Note     string      `json:"note,omitempty"`
	Accepted *Acceptance `json:"accepted,omitempty"`
}

// Acceptance is the acknowledgement a finding matched.
type Acceptance struct {
	Reason string `json:"reason"`
}

// Accept acknowledges one intentional change: it matches the finding with this rule and where.
type Accept struct {
	Rule   string `json:"rule"`
	Where  string `json:"where"`
	Reason string `json:"reason"`
}

// Options adjust a comparison.
type Options struct {
	// Release is the release being prepared (X.Y.Z). A removal the old contract planned for
	// this release or an earlier one is expected rather than breaking.
	Release string
	// Accept acknowledges intentional changes, one finding per entry.
	Accept []Accept
}

// Summary counts the findings by severity. An accepted finding counts only as accepted.
type Summary struct {
	Breaking         int `json:"breaking"`
	PossiblyBreaking int `json:"possibly_breaking"`
	Expected         int `json:"expected"`
	Safe             int `json:"safe"`
	Accepted         int `json:"accepted"`
}

// Report is the result of a comparison.
type Report struct {
	Findings         []Finding `json:"findings"`
	UnmatchedAccepts []Accept  `json:"unmatched_accepts"`
	Summary          Summary   `json:"summary"`
}

// FailOn levels: the least severe finding that fails a comparison.
const (
	FailOnBreaking = "breaking"
	FailOnPossibly = "possibly"
	FailOnNever    = "never"
)

// Failed reports whether the report fails at the given level: an unaccepted finding at or
// above it, or an acknowledgement that matched nothing. FailOnNever never fails.
func (r Report) Failed(failOn string) bool {
	switch failOn {
	case FailOnNever:
		return false
	case FailOnPossibly:
		return r.Summary.Breaking+r.Summary.PossiblyBreaking > 0 || len(r.UnmatchedAccepts) > 0
	default:
		return r.Summary.Breaking > 0 || len(r.UnmatchedAccepts) > 0
	}
}

// Diff compares the old contract document with the new one.
//
// An old contract written before rotini 1.4 records fewer facts. The new one is then compared
// only on the facts the old one records: a fact the old format couldn't state is unknown, not
// added.
func Diff(oldRaw, newRaw []byte, opts Options) (Report, error) {
	o, err := decode("old", oldRaw)
	if err != nil {
		return Report{}, err
	}
	n, err := decode("new", newRaw)
	if err != nil {
		return Report{}, err
	}
	if o.legacy && !n.legacy {
		n = projectLegacy(n)
	}
	d := &differ{old: o, new: n, release: opts.Release}
	d.document()
	return d.report(opts.Accept), nil
}

// differ accumulates the findings of one comparison.
type differ struct {
	old, new *document
	release  string
	findings []Finding
}

// stability is the effective stability of an item in the old contract: "experimental",
// "beta" or "" (stable).
type stability string

// stabilityRank orders stabilities, least settled first.
func stabilityRank(s string) int {
	switch s {
	case "experimental":
		return 0
	case "beta":
		return 1
	default:
		return 2
	}
}

// leastStable returns the less settled of a and b.
func leastStable(a stability, b string) stability {
	if stabilityRank(b) < stabilityRank(string(a)) {
		return stability(b)
	}
	return a
}

// add records a finding, adjusted for the old item's stability: on an experimental item every
// finding is safe, and on a beta item a breaking one is possibly breaking. An expected finding
// stays expected.
func (d *differ) add(st stability, sev Severity, rule, where, msg, note string) {
	if sev != Expected && sev != Safe {
		switch st {
		case "experimental":
			sev, note = Safe, joinNote(note, "experimental")
		case "beta":
			if sev == Breaking {
				sev, note = PossiblyBreaking, joinNote(note, "beta")
			}
		}
	}
	d.findings = append(d.findings, Finding{Severity: sev, Rule: rule, Where: where, Message: msg, Note: note})
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// removal records the removal of an item the old contract declared: expected when its
// removed_in is at or below the release, possibly breaking when it was hidden, else breaking.
func (d *differ) removal(st stability, rule, where, msg, removedIn string, hidden bool) {
	d.removalWith(st, rule, where, msg, removedIn, hidden, "")
}

// removalWith is removal with a note of the caller's added.
func (d *differ) removalWith(st stability, rule, where, msg, removedIn string, hidden bool, note string) {
	switch {
	case removedIn != "" && d.release != "" && compareVersions(removedIn, d.release) <= 0:
		d.add(st, Expected, rule, where, msg, joinNote(note, "planned for removal in "+removedIn))
	case removedIn != "" && d.release == "":
		d.add(st, Breaking, rule, where, msg, joinNote(note, "planned for removal in "+removedIn+"; pass --release"))
	case removedIn != "":
		d.add(st, Breaking, rule, where, msg, joinNote(note, "planned for removal in "+removedIn))
	case hidden:
		d.add(st, PossiblyBreaking, rule, where, msg, joinNote(note, "was hidden"))
	default:
		d.add(st, Breaking, rule, where, msg, note)
	}
}

// report sorts the findings, applies the acknowledgements and counts.
func (d *differ) report(accepts []Accept) Report {
	fs := d.findings
	slices.SortStableFunc(fs, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Severity.rank(), b.Severity.rank()), cmp.Compare(a.Where, b.Where),
			cmp.Compare(a.Rule, b.Rule), cmp.Compare(a.Message, b.Message))
	})
	r := Report{Findings: fs, UnmatchedAccepts: []Accept{}}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	for _, a := range accepts {
		i := slices.IndexFunc(r.Findings, func(f Finding) bool {
			return f.Accepted == nil && f.Rule == a.Rule && f.Where == a.Where
		})
		if i < 0 {
			r.UnmatchedAccepts = append(r.UnmatchedAccepts, a)
			continue
		}
		r.Findings[i].Accepted = &Acceptance{Reason: a.Reason}
	}
	for _, f := range r.Findings {
		switch {
		case f.Accepted != nil:
			r.Summary.Accepted++
		case f.Severity == Breaking:
			r.Summary.Breaking++
		case f.Severity == PossiblyBreaking:
			r.Summary.PossiblyBreaking++
		case f.Severity == Expected:
			r.Summary.Expected++
		default:
			r.Summary.Safe++
		}
	}
	return r
}

// compareVersions compares two X.Y.Z releases as -1, 0 or 1. A leading v and a prerelease or
// build suffix are ignored, so 2.0.0-rc.1 counts as 2.0.0.
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := range 3 {
		if c := cmp.Compare(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		if n, err := strconv.Atoi(p); err == nil {
			out[i] = n
		}
	}
	return out
}

// pathKey is a command's path as one string, the key commands match by.
func pathKey(path []string) string { return strings.Join(path, " ") }

// flip is the finding a boolean fact gives when it turns on, or off.
type flip struct {
	sev       Severity
	rule, msg string
}

// flipped reports a boolean fact that changed: on when it turned on, off when it turned off.
func (d *differ) flipped(st stability, where string, o, n bool, on, off flip) {
	switch {
	case !o && n:
		d.add(st, on.sev, on.rule, where, on.msg, "")
	case o && !n:
		d.add(st, off.sev, off.rule, where, off.msg, "")
	}
}
