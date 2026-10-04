package rotini

import (
	"fmt"
	"strings"
	"testing"
)

// Tests over the text of the package's errors, for every type that renders one.

func TestErrorStrings(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		parts []string
	}{
		{
			name:  "Deprecation",
			err:   Deprecation{Kind: "flag", Name: "conf", Identifier: "--conf"},
			parts: []string{"deprecated", "flag", "--conf"},
		},
		{
			name:  "ExitCause cancellation cause",
			err:   ExitCause(3),
			parts: []string{"canceled", "3"},
		},
		{
			name:  "DependencyError",
			err:   &DependencyError{Name: "store"},
			parts: []string{"rotini", "store"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			// rotini's name belongs only in messages about a mistake in the program.
			if tc.name == "ExitCause cancellation cause" && strings.Contains(msg, "rotini") {
				t.Errorf("%s message %q names the framework to the CLI's users", tc.name, msg)
			}
			for _, p := range tc.parts {
				if !strings.Contains(msg, p) {
					t.Errorf("%s message %q does not name %q", tc.name, msg, p)
				}
			}
			if strings.TrimSpace(msg) == "" {
				t.Errorf("%s renders empty", tc.name)
			}
		})
	}
}

// TestUserFacingErrorsDoNotNameTheFramework pins that errors a CLI's users can see do not
// start with "rotini: ". Messages about a mistake in the program itself (a nil context, an
// inputs type for the wrong command) keep the name.
func TestUserFacingErrorsDoNotNameTheFramework(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"ErrUsage":         ErrUsage,
		"ErrInternal":      ErrInternal,
		"wrapped ErrUsage": fmt.Errorf("%w: bad flag", ErrUsage),
		"ExitCause":        ExitCause(130),
	} {
		if strings.Contains(err.Error(), "rotini") {
			t.Errorf("%s = %q: names the framework in text the CLI's users read", name, err)
		}
	}
}
