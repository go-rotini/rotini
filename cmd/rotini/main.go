//go:generate go tool gentypes -package internal -root Spec -o ../../internal/spec_types.go ../../schema-spec.json
//go:generate go tool gentypes -package internal -root Conf -o ../../internal/conf_types.go ../../schema-conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"regexp"
	"runtime/debug"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/cli"
)

// releaseTagRe matches a clean vX.Y.Z release tag — the only Main.Version form
// that is a real release. A pseudo-version (untagged install), a pre-release,
// "(devel)", and "" all fail it and fall back to v0.0.0.
var releaseTagRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// version is the companion's single bound "version" service. The same string is
// used to print the version (rotini --version / version), stamp the $schema line
// during rotini initialize, and check it during rotini validate. It is a real
// semver "vX.Y.Z" when built from a git tag (go install / go get -tool @vX.Y.Z),
// else "v0.0.0" for any unreleased build (a local/dev build, or an untagged
// install). The internal consumers strip the leading "v" to get the $schema
// version segment.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "v0.0.0"
	}
	if v := bi.Main.Version; releaseTagRe.MatchString(v) {
		return v
	}
	return "v0.0.0"
}

func main() {
	cli.Program.
		Bind("parser", rotini.NewParser()).
		Bind("version", version()).
		Execute()
}
