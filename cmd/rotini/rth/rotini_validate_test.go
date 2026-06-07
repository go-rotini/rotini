package rth

import (
	"errors"
	"testing"

	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
)

// TestRotiniValidate covers the validate command handler (rotini_validate.go). The work is
// injected at the "validate" registry seam so the success and failure branches are exercised
// with doubles (the "spec:/conf:" header always prints first). One case leaves "validate"
// unbound so the real internal.Validate runs (integration).
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
			argv: []string{"validate", "--help"}, wantOut: rtg.HelpRotiniValidate, wantCode: 0,
		},
		{
			name: "parse error on unknown flag",
			argv: []string{"validate", "--nope"}, wantOut: rtg.HelpRotiniValidate, wantErr: "Error:", wantCode: 1,
		},
		{
			name:    "success prints only the header",
			argv:    []string{"validate"},
			binds:   []svc{{"validate", internal.ValidateFn(func(_, _, _ string) error { return nil })}},
			wantOut: "spec: .rotini.spec.yaml", wantCode: 0,
		},
		{
			name:    "validation error surfaces",
			argv:    []string{"validate"},
			binds:   []svc{{"validate", internal.ValidateFn(func(_, _, _ string) error { return errors.New("schema violation") })}},
			wantOut: "spec: .rotini.spec.yaml", wantErr: "schema violation", wantCode: 1,
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
