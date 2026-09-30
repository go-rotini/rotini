package codegen

import (
	"path/filepath"
	"slices"
	"testing"
)

// Constraints written on an array input's `items` used to be decoded and then ignored:
//
//	schema: { type: array, items: { type: string, enum: [low, high] } }
//	$ mycli --level BOGUS        # accepted
//
// `items` is where JSON Schema puts an element constraint, so the natural spelling was the one
// that did nothing. hoistItemConstraints makes it mean what the array-level spelling means.

func decodeSpecYAML(t *testing.T, body string) *Spec {
	t.Helper()
	spec, err := decodeData[Spec](formatYAML, []byte(body), "test.yaml")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return spec
}

const itemsSpecHead = "version: 0.0.0\ncommand:\n  name: t\n  summary: s\n"

func TestHoistItemConstraints_everyPerValueConstraintReachesTheList(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema:
        type: array
        items:
          type: int
          enum: ["10", "20"]
          pattern: "^[0-9]+$"
          minimum: 1
          maximum: 99
          exclusiveMinimum: 0
          exclusiveMaximum: 100
          multipleOf: 10
          minLength: 1
          maxLength: 3
`)
	s := spec.Command.Flags[0].Schema
	switch {
	case !slices.Equal(s.Enum, []string{"10", "20"}):
		t.Errorf("enum = %v", s.Enum)
	case s.Pattern != "^[0-9]+$":
		t.Errorf("pattern = %q", s.Pattern)
	case s.Minimum == nil || *s.Minimum != 1:
		t.Errorf("minimum = %v", s.Minimum)
	case s.Maximum == nil || *s.Maximum != 99:
		t.Errorf("maximum = %v", s.Maximum)
	case s.ExclusiveMinimum == nil || *s.ExclusiveMinimum != 0:
		t.Errorf("exclusiveMinimum = %v", s.ExclusiveMinimum)
	case s.ExclusiveMaximum == nil || *s.ExclusiveMaximum != 100:
		t.Errorf("exclusiveMaximum = %v", s.ExclusiveMaximum)
	case s.MultipleOf == nil || *s.MultipleOf != 10:
		t.Errorf("multipleOf = %v", s.MultipleOf)
	case s.MinLength != 1 || s.MaxLength != 3:
		t.Errorf("minLength/maxLength = %d/%d", s.MinLength, s.MaxLength)
	}
}

// Every channel that parses list elements from text gets the same treatment — not just flags.
func TestHoistItemConstraints_appliesToFlagsArgumentsEnvAndConfig(t *testing.T) {
	item := "        type: array\n        items: { type: string, enum: [a, b] }\n"
	spec := decodeSpecYAML(t, itemsSpecHead+
		"  flags:\n    - name: f\n      identifiers: [--f]\n      summary: s\n      schema:\n"+item+
		"  arguments:\n    - name: g\n      schema:\n"+item+
		"  env:\n    - name: h\n      schema:\n"+item+
		"  config:\n    - name: i\n      schema:\n"+item)

	for name, s := range map[string]*InputSchema{
		"flag":     spec.Command.Flags[0].Schema,
		"argument": spec.Command.Arguments[0].Schema,
		"env":      spec.Command.Env[0].Schema,
		"config":   spec.Command.Config[0].Schema,
	} {
		if !slices.Equal(s.Enum, []string{"a", "b"}) {
			t.Errorf("%s: enum = %v, want the items enum hoisted", name, s.Enum)
		}
	}
}

// The array-level value is never overwritten. When the two disagree, lintItemConstraints says so;
// hoisting must not quietly pick a winner.
func TestHoistItemConstraints_neverOverwritesTheList(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: array, enum: [a], minimum: 5, items: { type: string, enum: [x], minimum: 9 } }
`)
	s := spec.Command.Flags[0].Schema
	if !slices.Equal(s.Enum, []string{"a"}) || *s.Minimum != 5 {
		t.Errorf("list-level values overwritten: enum=%v minimum=%v", s.Enum, *s.Minimum)
	}
}

// stdin validates a whole document with JSON Schema semantics, where `items` constraints are
// already real. Hoisting them would change what a nested document means.
func TestHoistItemConstraints_leavesStdinAlone(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  stdin:
    format: json
    schema:
      type: array
      items: { type: string, enum: [a, b] }
`)
	if s := spec.Command.Stdin.Schema; len(s.Enum) != 0 {
		t.Errorf("stdin schema was rewritten: enum = %v", s.Enum)
	}
}

// Non-array inputs are untouched even if they carry an `items` (which other rules reject).
func TestHoistItemConstraints_onlyForLists(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: string, items: { type: string, enum: [a] } }
`)
	if s := spec.Command.Flags[0].Schema; len(s.Enum) != 0 {
		t.Errorf("a scalar input received an items enum: %v", s.Enum)
	}
}

// A spec pulled in by `$ref` is decoded through readSpec, not reconcileSpec. The hook lives in
// decodeData so neither path can miss it — this is the path that would have.
func TestHoistItemConstraints_reachesComposedSpecs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "child.spec.yaml", itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: array, items: { type: string, enum: [a, b] } }
`)
	spec, err := readSpec(filepath.Join(dir, "child.spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if s := spec.Command.Flags[0].Schema; !slices.Equal(s.Enum, []string{"a", "b"}) {
		t.Errorf("composed spec not normalized: enum = %v", s.Enum)
	}
}

// The hoist feeds every later rule. A default element that breaks an items-level enum is exactly
// the "fails on every run" case lintDefaultConstraints exists for, and it must now be caught.
func TestHoistItemConstraints_feedsDefaultValidation(t *testing.T) {
	spec := decodeSpecYAML(t, itemsSpecHead+`  flags:
    - name: f
      identifiers: [--f]
      summary: s
      schema: { type: array, items: { type: string, enum: [low, high] }, default: [low, BOGUS] }
`)
	if len(lintDefaultConstraints(spec)) == 0 {
		t.Error("a default element violating an items-level enum was not caught")
	}
}
