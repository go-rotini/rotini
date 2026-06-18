package rotini

import (
	"errors"
	"testing"

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
			argv: []string{"validate", "--nope"}, wantOut: "", wantErr: `Error: unknown flag "--nope"`, wantCode: 1,
		},
		{
			name: "success: header and the pass summary print",
			argv: []string{"validate"},
			binds: []svc{{"validate", internal.ValidateFn(func(_, _ string, _ bool, _ string, cb func(string, error), _ func([]error)) error {
				cb("[12:00:00] 1ms", nil)
				return nil
			})}},
			wantOut: "[12:00:00] 1ms", wantCode: 0,
		},
		{
			name: "per-pass callback error and a final error both surface",
			argv: []string{"validate"},
			binds: []svc{{"validate", internal.ValidateFn(func(_, _ string, _ bool, _ string, cb func(string, error), _ func([]error)) error {
				cb("[12:00:00] 1ms", nil)              // a clean pass → stdout
				cb("", errors.New("schema violation")) // a failing pass → stderr, inline, non-terminal
				return errors.New("validation failed")
			})}},
			// The per-pass callback error still prints inline; the final error is
			// recorded and reported by the default OnError, exit code stands.
			wantOut: "[12:00:00] 1ms", wantErr: "schema violation", wantCode: 1,
		},
		{
			name: "validator warnings route to the OnWarning funnel; the run still succeeds",
			argv: []string{"validate"},
			binds: []svc{{"validate", internal.ValidateFn(func(_, _ string, _ bool, _ string, cb func(string, error), warn func([]error)) error {
				warn([]error{errors.New("config_files shadow")}) // non-fatal → RecordWarning
				cb("[12:00:00] 1ms", nil)
				return nil
			})}},
			// RecordWarning → the default OnWarning prints "Warning: …" to stderr; exit stays 0.
			wantOut: "[12:00:00] 1ms", wantErr: "Warning: config_files shadow", wantCode: 0,
		},
		{
			name: "real validate on a missing spec errors (integration)",
			argv: []string{"validate", "/no/such/spec.yaml"}, wantOut: "spec: /no/such/spec.yaml", wantErr: "Error:", wantCode: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runRotini(t, tc.argv, tc.binds...)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
