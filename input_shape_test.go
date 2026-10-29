package rotini

import (
	"net"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/go-rotini/recon"
)

func TestHoldsOneValue(t *testing.T) {
	for typ, want := range map[reflect.Type]bool{
		reflect.TypeFor[string]():            true,
		reflect.TypeFor[*int]():              true,
		reflect.TypeFor[time.Time]():         true,
		reflect.TypeFor[time.Duration]():     true,
		reflect.TypeFor[net.IP]():            true,
		reflect.TypeFor[*url.URL]():          true,
		reflect.TypeFor[[]string]():          false,
		reflect.TypeFor[map[string]string](): false,
		reflect.TypeFor[map[string]any]():    false,
		reflect.TypeFor[struct{ A int }]():   false,
		reflect.TypeFor[any]():               false,
	} {
		if got := holdsOneValue(typ); got != want {
			t.Errorf("holdsOneValue(%v) = %v, want %v", typ, got, want)
		}
	}
}

func TestShapeProblems(t *testing.T) {
	list := recon.NewValue([]any{"a", "b"})
	nested := recon.NewValue(map[string]any{"a": "1", "b": map[string]any{"c": []any{"x"}}})
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"scalar config", configShapeProblem(list, reflect.TypeFor[string]()), "takes one value, not a list"},
		{"config map entry", configShapeProblem(nested, reflect.TypeFor[map[string]string]()), "takes one value per key, not a map (key b)"},
		{"config any map", configShapeProblem(nested, reflect.TypeFor[map[string]any]()), ""},
		{"config list item", configShapeProblem(recon.NewValue([]any{[]any{"a"}}), reflect.TypeFor[[]string]()), "takes one value per item, not a list (item 1)"},
		{"flag nested list", flagShapeProblem(nested, reflect.TypeFor[map[string]string](), false), "takes one value per key, not a list (key b.c)"},
		{"flag list", flagShapeProblem(list, reflect.TypeFor[[]string](), false), ""},
		{"object list", flagShapeProblem(list, reflect.TypeFor[map[string]any](), true), "takes one object, not a list"},
		{"object map", flagShapeProblem(nested, reflect.TypeFor[map[string]any](), true), ""},
		{"scalar", flagShapeProblem(recon.NewValue("a"), reflect.TypeFor[string](), false), ""},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	block := objectBlock(nested, []string{"a=1", "b.c=[x]"})
	if !slices.Equal(block, []string{`{"a":"1","b":{"c":["x"]}}`}) {
		t.Errorf("objectBlock = %q, want one JSON document", block)
	}
}

// An empty variable leaves a .env file's value; with none, it is set and empty.
func TestInputLookup_emptyVariableUnderDotenv(t *testing.T) {
	view := newOSView([]string{"A=", "B=", "C=real"}, "", "linux").withInputEnv(&inputEnv{layers: []dotenvLayer{
		{vals: map[string]string{"A": "dot", "C": "dot", "D": ""}, path: "/w/.env"},
	}})
	for name, want := range map[string]struct {
		val, origin string
		set         bool
	}{
		"A": {"dot", " (from .env)", true},
		"B": {"", "", true},
		"C": {"real", "", true},
		"D": {"", " (from .env)", true},
		"E": {"", "", false},
	} {
		val, set := view.inputLookup(name)
		if val != want.val || set != want.set || view.inputOrigin(name) != want.origin {
			t.Errorf("%s = %q, %v, %q; want %+v", name, val, set, view.inputOrigin(name), want)
		}
	}
	if env := view.inputEnviron(); !slices.Contains(env, "A=dot") || slices.Contains(env, "A=") {
		t.Errorf("inputEnviron = %q, want A from .env", env)
	}
}
