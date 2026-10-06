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
	// releaseVersionRe matches a full semantic version, anchored at both ends, and captures
	// its X.Y.Z.
	releaseVersionRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	// pseudoVersionRe matches a Go pseudo-version tail: a 14-digit UTC timestamp and a 12-hex
	// commit prefix, after '-' (v0.0.0-20260901233311-3ef400c2a629) or '.'
	// (v1.3.0-0.20260901233311-3ef400c2a629).
	pseudoVersionRe = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}(?:\+[0-9A-Za-z.-]+)?$`)
)

var _ rotini.Handler = (*rotiniHandler)(nil)

type rotiniHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

// releaseVersion returns v's bare X.Y.Z when v names a release, or "" for an empty string,
// "(devel)", a Go pseudo-version, or anything else that is not a semantic version.
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

// ResolveVersion returns the version the binary reports. The build info's module version wins
// when it is a release (`go install pkg@v1.2.3`); otherwise the -ldflags stamp is used, reduced
// to X.Y.Z when it is a semantic version and returned as is when not.
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

func (*rotiniHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}

	if inputs.Rotini.Flags.Help {
		var path []string
		for _, c := range rtx.CommandChain()[1:] {
			path = append(path, c.Name)
		}

		page, err := Help(path...)
		if err != nil {
			rtx.HaltWith(err)
			return
		}

		fmt.Fprintln(rtx.Stdout, page)
		rtx.HaltWithCode(0)
		return
	}

	if inputs.Rotini.Flags.Version {
		fmt.Fprintf(rtx.Stdout, "v%s\n", rtx.Version())
		rtx.HaltWithCode(0)
		return
	}
}

func (*rotiniHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stdout, rtx.Help())
	rtx.HaltWithCode(1)
}
