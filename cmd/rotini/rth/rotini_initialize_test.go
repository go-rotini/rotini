package rth

import (
	"errors"
	"testing"

	"github.com/go-rotini/rotini/cmd/rotini/rtg"
	"github.com/go-rotini/rotini/internal"
)

// TestRotiniInitialize covers the initialize command handler (rotini_initialize.go). The
// missing-name guard runs before any work; the scaffold itself is injected at the
// "initialize" registry seam so its success/failure branches are exercised with doubles.
// One case leaves "initialize" unbound and runs outside a module so the real
// internal.Initialize fails (integration).
func TestRotiniInitialize(t *testing.T) {
	cases := []struct {
		name             string
		setup            func(t *testing.T)
		argv             []string
		binds            []svc
		wantOut, wantErr string
		wantCode         int
	}{
		{
			name: "help flag",
			argv: []string{"init", "--help"}, wantOut: rtg.HelpRotiniInitialize, wantCode: 0,
		},
		{
			name: "parse error on unknown flag",
			argv: []string{"init", "--nope"}, wantOut: rtg.HelpRotiniInitialize, wantErr: "Error:", wantCode: 1,
		},
		{
			name: "missing name argument (guarded before any work)",
			argv: []string{"init"}, wantErr: "a name argument is required", wantCode: 1,
		},
		{
			name:     "success is silent",
			argv:     []string{"init", "mycli"},
			binds:    []svc{{"initialize", internal.InitializeFn(func(_, _ string, _ bool, _ string) error { return nil })}},
			wantCode: 0,
		},
		{
			name:    "initialize error surfaces",
			argv:    []string{"init", "mycli"},
			binds:   []svc{{"initialize", internal.InitializeFn(func(_, _ string, _ bool, _ string) error { return errors.New("already exists") })}},
			wantErr: "already exists", wantCode: 1,
		},
		{
			name:  "real initialize outside a module errors (integration)",
			setup: func(t *testing.T) { t.Chdir(t.TempDir()) }, // no go.mod → module resolution fails
			argv:  []string{"init", "mycli"}, wantErr: "Error:", wantCode: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			out, errb, code := runRotini(t, tc.argv, tc.binds...)
			check(t, out, errb, code, tc.wantOut, tc.wantErr, tc.wantCode)
		})
	}
}
