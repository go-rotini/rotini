//go:generate go tool gentypes -package internal -root Spec -o ../../internal/spec_types.go ../../schema-spec.json
//go:generate go tool gentypes -package internal -root Conf -o ../../internal/conf_types.go ../../schema-conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/cmd/rotini/cli"
)

func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "v0.0.0"
	}
	if ver := bi.Main.Version; ver != "" && ver != "(devel)" {
		return ver
	}
	return "v0.0.0"
}

// schemaRef returns the release tag the binary was built from, as the "X.Y.Z"
// segment used in the scaffolded $schema URLs (no leading "v"), or "" when the
// binary is not a clean tagged release — a local/dev build ("(devel)"), or an
// untagged `go install` (which yields a Go pseudo-version like
// "v0.0.0-20260607230016-012c443827ff"). It is bound as the "schema_ref" service:
// "" means scaffolds fall back to the baseline 0.0.0 schema and `validate`/
// `generate` skip the $schema↔binary version match (an untagged build has no
// authoritative version to enforce). Only a clean vX.Y.Z release tag drives a
// stamped $schema and an enforced match.
func schemaRef() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return releaseTag(bi.Main.Version)
}

// releaseTag maps a module version string (a runtime/debug Main.Version) to the
// "X.Y.Z" schema tag segment, or "" when v is not a clean release tag. "" is
// returned for an empty/"(devel)" version, a Go pseudo-version (an untagged
// install), and a pre-release/+build tag — none of which name a published
// refs/tags/<VER> schema. It is a pure function of v so the classification is
// testable without controlling ReadBuildInfo.
func releaseTag(v string) string {
	if !semver.IsValid(v) || module.IsPseudoVersion(v) || semver.Prerelease(v) != "" || semver.Build(v) != "" {
		return ""
	}
	return strings.TrimPrefix(semver.Canonical(v), "v")
}

func main() {
	cli.Program.
		Bind("parser", rotini.NewParser()).
		Bind("version", version()).
		Bind("schema_ref", schemaRef()).
		Execute()
}
