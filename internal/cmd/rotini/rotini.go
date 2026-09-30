package rotini

import (
	"context"
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/go-rotini/rotini"
)

var (
	readBuildInfo = debug.ReadBuildInfo
	// releaseVersionRe matches a full semantic version and captures its X.Y.Z, anchored at
	// both ends so it cannot match the LEADING "v0.0.0" of something longer.
	releaseVersionRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	// pseudoVersionRe matches the Go pseudo-version tail: a 14-digit UTC timestamp and a
	// 12-hex commit prefix. Both shapes the go tool produces end this way — the untagged
	// v0.0.0-20260901233311-3ef400c2a629 and the after-a-tag
	// v1.3.0-0.20260901233311-3ef400c2a629, whose timestamp follows a '.' rather than a '-'.
	pseudoVersionRe = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}(?:\+[0-9A-Za-z.-]+)?$`)
)

// releaseVersion returns v's bare X.Y.Z when v names an actual release, or "" when it does
// not — "(devel)", an empty string, or a Go PSEUDO-version.
//
// The pseudo-version exclusion is the whole point. The matcher used to be unanchored, so the
// leading "v0.0.0" of a pseudo-version read as a release version; since build info is
// preferred over the -ldflags stamp, a binary built with `-X main.version=1.4.2` inside an
// untagged checkout reported 0.0.0 — which the spec and conf version guard then rejected
// against every correctly-versioned document in the project.
func releaseVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "(devel)" || pseudoVersionRe.MatchString(v) {
		return ""
	}
	if m := releaseVersionRe.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return ""
}

var _ rotini.Handlers = (*rotiniHandlers)(nil)

type rotiniHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

// ResolveVersion picks the version the binary reports, from the two places it can come from.
//
// Build info wins when it names a real release: that is the `go install pkg@v1.2.3` path,
// where no -ldflags stamp was passed and the module version is the only truth. Otherwise the
// stamp is used, which covers a release pipeline building from source and a dev build alike.
//
// Neither "(devel)" nor a pseudo-version counts as a release — see [releaseVersion].
func ResolveVersion(ldflagVersion string) string {
	if info, ok := readBuildInfo(); ok {
		if v := releaseVersion(info.Main.Version); v != "" {
			return v
		}
	}
	if v := releaseVersion(ldflagVersion); v != "" {
		return v
	}
	return ldflagVersion
}

func (*rotiniHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if answerHelp(rtx, func(in RotiniInputs) bool { return in.Rotini.Flags.Help }) {
		return
	}

	inputs, err := rotini.Collect[RotiniInputs](rtx)
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}

	if inputs.Rotini.Flags.Version {
		fmt.Fprintf(rtx.Stdout, "v%s\n", rtx.Version())
		rtx.HaltWithCode(0)
		return
	}

	// A bare `rotini` names no command: show what there is, and exit non-zero, since nothing ran.
	fmt.Fprintln(rtx.Stdout, rtx.Help())
	rtx.HaltWithCode(1)
}
