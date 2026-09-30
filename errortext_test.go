package rotini

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Guards over the TEXT of the package's errors, across every type that renders one. An Error()
// with no test is a message no one has read, and for a framework whose pitch includes actionable,
// non-leaky messages these are the wrong place to have a gap.

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
			name:  "ExitCode cancellation cause",
			err:   ExitCode(3),
			parts: []string{"canceled", "3"},
		},
		{
			name:  "ServiceError",
			err:   &ServiceError{Key: "store"},
			parts: []string{"rotini", "store"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			// rotini's name belongs in a message about a mistake in the PROGRAM (a service
			// never bound), not in one its users read as a result of running it.
			if tc.name == "ExitCode cancellation cause" && strings.Contains(msg, "rotini") {
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

// TestUserFacingErrorsDoNotNameTheFramework pins the text a CLI's USERS can see when rotini
// reports on their behalf. It used to lead with "rotini: " — `Error: rotini: shutdown timed
// out` — so every CLI had to catch and reword each one to avoid telling its users about its
// implementation (rubectl R-16, R-29). Messages about a mistake in the program itself (a nil
// context, an inputs type for the wrong command) keep the name: there it tells the developer
// where to look.
func TestUserFacingErrorsDoNotNameTheFramework(t *testing.T) {
	t.Parallel()
	worker := NewService().WithShutdownTimeout(time.Second).
		Go("indexer", func(context.Context) error { return errors.New("disk full") }).
		Run(context.Background())
	for name, err := range map[string]error{
		"ErrUsage":           ErrUsage,
		"ErrInternal":        ErrInternal,
		"wrapped ErrUsage":   fmt.Errorf("%w: bad flag", ErrUsage),
		"ErrNotInteractive":  ErrNotInteractive,
		"ErrInterrupted":     ErrInterrupted,
		"ErrShutdownTimeout": ErrShutdownTimeout,
		"a worker failure":   worker,
		"SubprocessError":    &SubprocessError{Name: "git", ExitCode: 1},
		"ExitCode":           ExitCode(130),
	} {
		if strings.Contains(err.Error(), "rotini") {
			t.Errorf("%s = %q: names the framework in text the CLI's users read", name, err)
		}
	}
}
