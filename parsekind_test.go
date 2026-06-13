package rotini

import (
	"errors"
	"testing"
)

// TestParseError_kindPerPath drives each parse/validate failure path and asserts
// the *ParseError carries the right ParseKind — so a funnel can branch on Kind
// instead of matching the message. Every kind still classifies as CategoryUsage
// (even the API-misuse Internal kind is a usage-shaped *ParseError).
func TestParseError_kindPerPath(t *testing.T) {
	min1 := Ptr(1.0)
	cases := []struct {
		name string
		def  Definition
		argv []string
		out  any
		want ParseKind
	}{
		{
			name: "unknown flag",
			def:  Definition{Name: "app", Handler: "App"},
			argv: []string{"--nope"},
			want: ParseKindUnknownFlag,
		},
		{
			name: "unknown command (stray positional on a branch)",
			def:  Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "run", Handler: "Run"}}},
			argv: []string{"ru"},
			want: ParseKindUnknownCommand,
		},
		{
			name: "flag needs a value",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "count", Identifiers: []string{"--count"}, Type: "int"}}},
			argv: []string{"--count"},
			want: ParseKindNeedsValue,
		},
		{
			name: "value on a count flag (invalid value)",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "c", Identifiers: []string{"--c"}, Type: "count"}}},
			argv: []string{"--c=5"},
			want: ParseKindInvalidValue,
		},
		{
			name: "enum violation",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}}}},
			argv: []string{"--level", "medium"},
			want: ParseKindEnumViolation,
		},
		{
			name: "constraint violation (minimum)",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "n", Identifiers: []string{"--n"}, Type: "int", Constraints: Constraints{Minimum: min1}}}},
			argv: []string{"--n", "0"},
			want: ParseKindConstraintViolation,
		},
		{
			name: "missing required",
			def:  Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true}}},
			argv: []string{},
			want: ParseKindMissingRequired,
		},
		{
			name: "command takes no arguments",
			def:  Definition{Name: "app", Handler: "App"},
			argv: []string{"stray"},
			want: ParseKindNoArguments,
		},
		{
			name: "too many arguments",
			def:  Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "name", Type: "string"}}},
			argv: []string{"a", "b"},
			want: ParseKindTooManyArguments,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rtx := NewContextFor(tc.def, tc.argv)
			out := tc.out
			if out == nil {
				out = &struct{}{}
			}
			err := NewParser().Parse(rtx, out)
			if err == nil {
				t.Fatalf("Parse(%v) = nil, want a %s error", tc.argv, tc.want)
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("err is not a *ParseError: %T (%v)", err, err)
			}
			if pe.Kind != tc.want {
				t.Errorf("Kind = %s, want %s (msg: %q)", pe.Kind, tc.want, pe.Msg)
			}
			// Every parse failure is usage-categorized regardless of kind.
			if CategoryOf(err) != CategoryUsage {
				t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
			}
		})
	}
}

// TestParseError_internalKind: a parser API misuse (a non-pointer out) is the
// Internal kind — still a usage-shaped *ParseError.
func TestParseError_internalKind(t *testing.T) {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	err := NewParser().Parse(rtx, 42) // not a pointer
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err is not a *ParseError: %T (%v)", err, err)
	}
	if pe.Kind != ParseKindInternal {
		t.Errorf("Kind = %s, want internal", pe.Kind)
	}
}

// TestParseKind_String pins the stable labels (and the zero-value default).
func TestParseKind_String(t *testing.T) {
	if got := ParseKindUnspecified.String(); got != "unspecified" {
		t.Errorf("ParseKindUnspecified = %q, want unspecified", got)
	}
	if got := ParseKindEnumViolation.String(); got != "enum-violation" {
		t.Errorf("ParseKindEnumViolation = %q, want enum-violation", got)
	}
}
