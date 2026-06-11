package rotini

import (
	"errors"
	"testing"

	"github.com/go-rotini/rotini/internal"
)

// TestRotiniGenerate covers the generate command handler (rotini_generate.go). The work is
// injected at the "generate" registry seam, so the handler's branches — including the
// per-file callback's result vs. error paths, which are near-impossible to drive through the
// real codegen — are exercised with doubles. One case leaves "generate" unbound so the real
// internal.Generate runs (integration), proving the handler↔dependency wiring.
func TestRotiniGenerate(t *testing.T) {
	cases := []struct {
		name             string
		argv             []string
		binds            []svc
		wantOut, wantErr string
		wantCode         int
	}{
		{
			name: "parse error on unknown flag",
			argv: []string{"generate", "--nope"}, wantOut: HelpRotiniGenerate, wantErr: "Error:", wantCode: 1,
		},
		{
			name: "help flag",
			argv: []string{"generate", "--help"}, wantOut: HelpRotiniGenerate, wantCode: 0,
		},
		{
			name: "success: header and the per-file result print",
			argv: []string{"generate", ".rotini.spec.yaml"},
			binds: []svc{{"generate", internal.GenerateFn(func(_, _ string, _ bool, cb func(string, error)) error {
				cb("cmd/mycli/rtg/rotini.go", nil)
				return nil
			})}},
			wantOut: "cmd/mycli/rtg/rotini.go", wantCode: 0,
		},
		{
			name: "per-file callback error and a final error both surface",
			argv: []string{"generate", ".rotini.spec.yaml"},
			binds: []svc{{"generate", internal.GenerateFn(func(_, _ string, _ bool, cb func(string, error)) error {
				cb("cmd/mycli/rtg/rotini.go", nil) // a good file → stdout
				cb("", errors.New("bad template")) // a per-file error → stderr
				return errors.New("generation failed")
			})}},
			wantOut: "cmd/mycli/rtg/rotini.go", wantErr: "bad template", wantCode: 1,
		},
		{
			name: "real generate on a missing spec errors (integration)",
			argv: []string{"generate", "/no/such/spec.yaml"}, wantOut: "spec: /no/such/spec.yaml", wantErr: "Error:", wantCode: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv, tc.binds...)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
