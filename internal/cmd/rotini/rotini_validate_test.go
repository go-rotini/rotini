package rotini

import (
	"errors"
	"testing"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal"
)

// TestRotiniValidate covers the validate command handler (rotini_validate.go). Validate now
// mirrors Generate (watch + a per-pass callback), injected at the "validate" registry seam, so
// the handler's branches — the per-pass callback's result vs. error paths and the final error —
// are exercised with doubles (the "spec:/conf:" header always prints first). One case leaves
// "validate" unbound so the real internal.Validate runs (integration).
func TestRotiniValidate(t *testing.T) {
	cases := []struct {
		name             string
		argv             []string
		binds            []svc
		wantOut, wantErr string
		wantCode         int
	}{
		{
			name: "help flag",
			argv: []string{"validate", "--help"}, wantOut: HelpRotiniValidate, wantCode: 0,
		},
		{
			name: "parse error on unknown flag",
			argv: []string{"validate", "--nope"}, wantOut: "", wantErr: `rotini: unknown flag "--nope"`, wantCode: rotini.ExitUsage,
		},
		{
			name: "success: header and the pass summary print",
			argv: []string{"validate"},
			binds: []svc{{"validate", internal.ValidateFn(func(_, _ string, _ bool, _ string, cb func(string, error)) error {
				cb("[12:00:00] 1ms", nil)
				return nil
			})}},
			wantOut: "[12:00:00] 1ms", wantCode: 0,
		},
		{
			name: "per-pass callback error and a final error both surface",
			argv: []string{"validate"},
			binds: []svc{{"validate", internal.ValidateFn(func(_, _ string, _ bool, _ string, cb func(string, error)) error {
				cb("[12:00:00] 1ms", nil)              // a clean pass → stdout
				cb("", errors.New("schema violation")) // a failing pass → stderr, inline, non-terminal
				return errors.New("validation failed")
			})}},
			// The per-pass callback error still prints inline; the final error is
			// recorded and reported by the default OnError, exit code stands.
			wantOut: "[12:00:00] 1ms", wantErr: "schema violation", wantCode: rotini.ExitUsage,
		},
		{
			name: "real validate on a missing spec errors (integration)",
			argv: []string{"validate", "/no/such/spec.yaml"}, wantOut: "spec: /no/such/spec.yaml", wantErr: "rotini:", wantCode: rotini.ExitUsage,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv, tc.binds...)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
