package codegen

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// validateTree writes files into a module, validates the spec at .rotini.spec.yaml and returns
// every problem reported, warnings first, then errors.
func validateTree(t *testing.T, files map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/v\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	for path, body := range files {
		writeTestFile(t, dir, path, body)
	}
	t.Chdir(dir)
	var out []string
	err := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", "",
		func(string, error) {}, func(w []error) {
			for _, e := range w {
				out = append(out, "warning: "+e.Error())
			}
		})
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		for _, e := range joined.Unwrap() {
			out = append(out, e.Error())
		}
	} else if err != nil {
		out = append(out, err.Error())
	}
	return out
}

// shadowParent is a root with two cascading flags, -o of which still has --output, and -q
// which is quiet's only identifier.
const shadowParent = `version: 0.0.0
command:
  name: demo
  flags:
    - name: output
      identifiers: [-o, --output]
      cascading: true
      schema: { type: string }
    - name: quiet
      identifiers: [-q]
      cascading: true
      schema: { type: bool }
  commands:
`

// shadowChild is a child whose root reuses -o and whose sub-command reuses -q.
const shadowChild = `  name: kid
  summary: the kid
  flags:
    - name: owner
      identifiers: [-o, --owner]
      schema: { type: string }
  commands:
    - name: work
      summary: do work
      flags:
        - name: quota
          identifiers: [-q, --quota]
          schema: { type: int }
`

// TestComposedShadowing_sameAsInline pins that a child mounted with `$ref` whose flags reuse a
// short form of a parent's cascading flag gets the problems an inline child gets, word for word,
// placed at the flag in the child's file.
func TestComposedShadowing_sameAsInline(t *testing.T) {
	inline := validateTree(t, map[string]string{
		".rotini.spec.yaml": shadowParent + "    - " + strings.TrimPrefix(indent(shadowChild, "      "), "      "),
	})
	mounted := validateTree(t, map[string]string{
		".rotini.spec.yaml": shadowParent + "    - $ref: kid.spec.yaml\n      name: kid\n",
		"kid.spec.yaml":     "version: 0.0.0\ncommand:\n" + shadowChild,
	})
	const (
		warn = `command demo/kid: flag "owner": identifier "-o" is also flag "output" on command demo, which cascades; ` +
			`after "kid" it means this flag instead, and only "--output" still reaches flag "output" there. Use another identifier for --owner`
		fail = `command demo/kid/work: flag "quota": identifier "-q" is also flag "quiet" on command demo, which cascades; ` +
			`after "work" it means this flag instead, so flag "quiet" can't be given there at all, only before "work". Use another identifier for --quota`
	)
	for _, tc := range []struct {
		name string
		got  []string
		pos  [2]string
	}{
		{"inline", inline, [2]string{".rotini.spec.yaml:17:13: ", ".rotini.spec.yaml:24:17: "}},
		{"mounted", mounted, [2]string{"kid.spec.yaml:6:7: ", "kid.spec.yaml:13:11: "}},
	} {
		if n := strings.Count(strings.Join(tc.got, "\n"), "also flag"); n != 2 {
			t.Errorf("%s: %d shadowing problems, want 2: %q", tc.name, n, tc.got)
		}
		want := []string{"warning: spec: " + tc.pos[0] + warn, "spec: " + tc.pos[1] + fail}
		for _, w := range want {
			if !slices.Contains(tc.got, w) {
				t.Errorf("%s: problems = %q\nwant %q", tc.name, tc.got, w)
			}
		}
	}
}

// TestComposedShadowing_besideTheRef pins the other direction: a command written beside the
// $ref, in the parent's file, reusing a short form of the mounted root's cascading flag.
func TestComposedShadowing_besideTheRef(t *testing.T) {
	got := validateTree(t, map[string]string{
		".rotini.spec.yaml": `version: 0.0.0
command:
  name: demo
  commands:
    - $ref: kid.spec.yaml
      commands:
        - name: extra
          summary: the parent's own
          flags:
            - name: count
              identifiers: [-c, --count]
              schema: { type: int }
`,
		"kid.spec.yaml": `version: 0.0.0
command:
  name: kid
  summary: the kid
  flags:
    - name: color
      identifiers: [-c, --color]
      cascading: true
      schema: { type: string }
`,
	})
	const want = `warning: spec: .rotini.spec.yaml:10:15: command demo/kid/extra: flag "count": identifier "-c" is also flag "color" on command demo/kid, ` +
		`which cascades; after "extra" it means this flag instead, and only "--color" still reaches flag "color" there. Use another identifier for --count`
	if !slices.Contains(got, want) {
		t.Errorf("problems = %q\nwant %q", got, want)
	}
}

// TestComposedShadowing_redeclarationIsQuiet pins that a mounted child redeclaring the parent's
// cascading switch, the documented override, is no problem.
func TestComposedShadowing_redeclarationIsQuiet(t *testing.T) {
	got := validateTree(t, map[string]string{
		".rotini.spec.yaml": shadowParent + "    - $ref: kid.spec.yaml\n",
		"kid.spec.yaml": `version: 0.0.0
command:
  name: kid
  summary: the kid
  flags:
    - name: output
      identifiers: [-o, --output]
      schema: { type: string }
`,
	})
	for _, p := range got {
		if strings.Contains(p, "also flag") {
			t.Errorf("a redeclared switch was reported: %s", p)
		}
	}
}

// indent prefixes every line of s but the empty last one with pad.
func indent(s, pad string) string {
	return pad + strings.ReplaceAll(strings.TrimSuffix(s, "\n"), "\n", "\n"+pad) + "\n"
}
