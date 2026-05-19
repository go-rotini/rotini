package rtk_test

import (
	"reflect"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// AssignFlag — scalar
// =============================================================================

func TestAssignFlag_unwrapsLatest(t *testing.T) {
	t.Parallel()
	scope := map[string]any{
		"name": []any{"first", "second", "third"},
	}
	var got string
	rtk.AssignFlag(scope, "name", &got)
	if got != "third" {
		t.Errorf("got %q, want %q", got, "third")
	}
}

func TestAssignFlag_singleValue(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"name": []any{"only"}}
	var got string
	rtk.AssignFlag(scope, "name", &got)
	if got != "only" {
		t.Errorf("got %q, want %q", got, "only")
	}
}

func TestAssignFlag_missingKey_leavesTarget(t *testing.T) {
	t.Parallel()
	got := "untouched"
	rtk.AssignFlag(map[string]any{}, "missing", &got)
	if got != "untouched" {
		t.Errorf("got %q, want %q", got, "untouched")
	}
}

func TestAssignFlag_wrongType_leavesTarget(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"name": []any{42}} // int, but asking for string
	got := "untouched"
	rtk.AssignFlag(scope, "name", &got)
	if got != "untouched" {
		t.Errorf("got %q, want %q (type mismatch should leave target)", got, "untouched")
	}
}

func TestAssignFlag_emptySlice_leavesTarget(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"name": []any{}}
	got := "untouched"
	rtk.AssignFlag(scope, "name", &got)
	if got != "untouched" {
		t.Errorf("got %q, want %q (empty slice should leave target)", got, "untouched")
	}
}

func TestAssignFlag_bareValueNotSliced_leavesTarget(t *testing.T) {
	t.Parallel()
	// Raw value not wrapped in []any (defensive — parser doesn't produce this,
	// but external callers may).
	scope := map[string]any{"name": "bare"}
	got := "untouched"
	rtk.AssignFlag(scope, "name", &got)
	if got != "untouched" {
		t.Errorf("got %q, want %q", got, "untouched")
	}
}

func TestAssignFlag_assortedTypes(t *testing.T) {
	t.Parallel()

	t.Run("int", func(t *testing.T) {
		scope := map[string]any{"n": []any{42}}
		var got int
		rtk.AssignFlag(scope, "n", &got)
		if got != 42 {
			t.Errorf("got %d, want 42", got)
		}
	})

	t.Run("bool", func(t *testing.T) {
		scope := map[string]any{"b": []any{true}}
		var got bool
		rtk.AssignFlag(scope, "b", &got)
		if !got {
			t.Errorf("got false, want true")
		}
	})

	t.Run("float64", func(t *testing.T) {
		scope := map[string]any{"f": []any{3.14}}
		var got float64
		rtk.AssignFlag(scope, "f", &got)
		if got != 3.14 {
			t.Errorf("got %f, want 3.14", got)
		}
	})
}

// =============================================================================
// AssignFlagPtr — nullable
// =============================================================================

func TestAssignFlagPtr_setsPointer(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"name": []any{"value"}}
	var got *string
	rtk.AssignFlagPtr(scope, "name", &got)
	if got == nil {
		t.Fatal("got nil, want non-nil pointer")
	}
	if *got != "value" {
		t.Errorf("got %q, want %q", *got, "value")
	}
}

func TestAssignFlagPtr_missingKey_leavesNil(t *testing.T) {
	t.Parallel()
	var got *string
	rtk.AssignFlagPtr(map[string]any{}, "missing", &got)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestAssignFlagPtr_wrongType_leavesNil(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"name": []any{42}}
	var got *string
	rtk.AssignFlagPtr(scope, "name", &got)
	if got != nil {
		t.Errorf("got %v, want nil (type mismatch)", got)
	}
}

func TestAssignFlagPtr_zeroValueDistinguished(t *testing.T) {
	t.Parallel()
	// The point of the *T form: distinguish "not supplied" (nil) from
	// "supplied as zero value" (non-nil pointer to zero).
	scope := map[string]any{"name": []any{""}}
	var got *string
	rtk.AssignFlagPtr(scope, "name", &got)
	if got == nil {
		t.Fatal("got nil, want non-nil pointer to empty string")
	}
	if *got != "" {
		t.Errorf("got %q, want empty string", *got)
	}
}

// =============================================================================
// AssignFlagSlice — repeated flag
// =============================================================================

func TestAssignFlagSlice_appendsAll(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"name": []any{"a", "b", "c"}}
	var got []string
	rtk.AssignFlagSlice(scope, "name", &got)
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v, want [a b c]", got)
	}
}

func TestAssignFlagSlice_appendsToExisting(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"name": []any{"new1", "new2"}}
	got := []string{"existing"}
	rtk.AssignFlagSlice(scope, "name", &got)
	if !reflect.DeepEqual(got, []string{"existing", "new1", "new2"}) {
		t.Errorf("got %v, want [existing new1 new2]", got)
	}
}

func TestAssignFlagSlice_missingKey_leaves(t *testing.T) {
	t.Parallel()
	got := []string{"existing"}
	rtk.AssignFlagSlice(map[string]any{}, "missing", &got)
	if !reflect.DeepEqual(got, []string{"existing"}) {
		t.Errorf("got %v, want [existing]", got)
	}
}

func TestAssignFlagSlice_skipsWrongTypeElements(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"name": []any{"a", 42, "b"}} // int in middle
	var got []string
	rtk.AssignFlagSlice(scope, "name", &got)
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("got %v, want [a b] (int silently skipped)", got)
	}
}

// =============================================================================
// AssignFlagMap
// =============================================================================

func TestAssignFlagMap_basicAssignment(t *testing.T) {
	t.Parallel()
	scope := map[string]any{"labels": map[string]string{"env": "prod", "tier": "web"}}
	var got map[string]string
	rtk.AssignFlagMap(scope, "labels", &got)
	if !reflect.DeepEqual(got, map[string]string{"env": "prod", "tier": "web"}) {
		t.Errorf("got %v", got)
	}
}

func TestAssignFlagMap_missingKey_leaves(t *testing.T) {
	t.Parallel()
	got := map[string]string{"keep": "this"}
	rtk.AssignFlagMap(map[string]any{}, "missing", &got)
	if !reflect.DeepEqual(got, map[string]string{"keep": "this"}) {
		t.Errorf("got %v, want {keep: this}", got)
	}
}

func TestAssignFlagMap_wrongType_leaves(t *testing.T) {
	t.Parallel()
	// Stored as map[string]int but asking for map[string]string.
	scope := map[string]any{"labels": map[string]int{"n": 1}}
	got := map[string]string{"keep": "this"}
	rtk.AssignFlagMap(scope, "labels", &got)
	if !reflect.DeepEqual(got, map[string]string{"keep": "this"}) {
		t.Errorf("got %v, want untouched", got)
	}
}

// =============================================================================
// AssignStringArg / AssignVariadicStringArg
// =============================================================================

func TestAssignStringArg_inRange(t *testing.T) {
	t.Parallel()
	args := []string{"first", "second", "third"}
	var got string
	rtk.AssignStringArg(args, 1, &got)
	if got != "second" {
		t.Errorf("got %q, want %q", got, "second")
	}
}

func TestAssignStringArg_outOfRange(t *testing.T) {
	t.Parallel()
	args := []string{"first"}
	got := "default"
	rtk.AssignStringArg(args, 5, &got)
	if got != "default" {
		t.Errorf("got %q, want default", got)
	}
}

func TestAssignVariadicStringArg_collectsRest(t *testing.T) {
	t.Parallel()
	args := []string{"first", "second", "third", "fourth"}
	var got []string
	rtk.AssignVariadicStringArg(args, 1, &got)
	if !reflect.DeepEqual(got, []string{"second", "third", "fourth"}) {
		t.Errorf("got %v", got)
	}
}

func TestAssignVariadicStringArg_emptyTail(t *testing.T) {
	t.Parallel()
	args := []string{"only"}
	var got []string
	rtk.AssignVariadicStringArg(args, 5, &got)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// =============================================================================
// AssignCoercedArg
// =============================================================================

func TestAssignCoercedArg_coercesAndAssigns(t *testing.T) {
	t.Parallel()
	args := []string{"42"}
	var got int
	rtk.AssignCoercedArg(args, 0, "int", &got, rtk.CoerceBuiltinValue)
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestAssignCoercedArg_outOfRange_leaves(t *testing.T) {
	t.Parallel()
	args := []string{}
	got := 99
	rtk.AssignCoercedArg(args, 0, "int", &got, rtk.CoerceBuiltinValue)
	if got != 99 {
		t.Errorf("got %d, want 99 (out-of-range should leave target)", got)
	}
}

func TestAssignCoercedArg_coercionError_leaves(t *testing.T) {
	t.Parallel()
	args := []string{"not-a-number"}
	got := 99
	rtk.AssignCoercedArg(args, 0, "int", &got, rtk.CoerceBuiltinValue)
	if got != 99 {
		t.Errorf("got %d, want 99 (coercion failure should leave target)", got)
	}
}

func TestAssignCoercedArg_typeMismatch_leaves(t *testing.T) {
	t.Parallel()
	// Coerce returns int; ask for int64 — type assertion fails.
	args := []string{"42"}
	got := int64(99)
	rtk.AssignCoercedArg(args, 0, "int", &got, rtk.CoerceBuiltinValue)
	if got != 99 {
		t.Errorf("got %d, want 99 (T != coerce-result type should leave target)", got)
	}
}

// =============================================================================
// End-to-end via Parser + Target with codegen-style PopulateFromArgv
// =============================================================================

// todoAddInputs is a small Target that mimics what codegen would emit for
// a `todo add` command spec with `--priority` and `text`.
type todoAddInputs struct {
	Help     bool
	Priority string
	Text     string
}

func (t *todoAddInputs) RotiniCommandPath() string { return "add" }

func (t *todoAddInputs) PopulateFromArgv(r *rtk.Result) error {
	if scope, ok := r.FlagsByScope[""]; ok {
		rtk.AssignFlag(scope, "help", &t.Help)
	}
	if scope, ok := r.FlagsByScope["add"]; ok {
		rtk.AssignFlag(scope, "priority", &t.Priority)
	}
	rtk.AssignStringArg(r.ParsedArgs, 0, &t.Text)
	return nil
}

func TestAssign_endToEnd_codegenShape(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Name: "todo",
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"},
		},
		Commands: []rtk.CommandSpec{
			{
				Name: "add", Path: "add",
				Flags: []rtk.FlagSpec{
					{Name: "priority", Identifiers: []string{"-p"}, Type: "string", Default: "medium"},
				},
				Arguments: []rtk.ArgumentSpec{
					{Name: "text", Type: "string", Required: true},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"--help", "add", "-p", "high", "buy milk"}})
	var inputs todoAddInputs
	if err := p.Parse(&inputs); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !inputs.Help {
		t.Error("Help: got false, want true")
	}
	if inputs.Priority != "high" {
		t.Errorf("Priority: got %q, want %q", inputs.Priority, "high")
	}
	if inputs.Text != "buy milk" {
		t.Errorf("Text: got %q, want %q", inputs.Text, "buy milk")
	}
}

func TestAssign_endToEnd_defaultsFlowToTarget(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Name: "todo",
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"},
		},
		Commands: []rtk.CommandSpec{
			{
				Name: "add", Path: "add",
				Flags: []rtk.FlagSpec{
					{Name: "priority", Identifiers: []string{"-p"}, Type: "string", Default: "medium"},
				},
				Arguments: []rtk.ArgumentSpec{
					{Name: "text", Type: "string"},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{Argv: []string{"add", "buy milk"}})
	var inputs todoAddInputs
	if err := p.Parse(&inputs); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if inputs.Priority != "medium" {
		t.Errorf("Priority: got %q, want \"medium\" (the default)", inputs.Priority)
	}
}
