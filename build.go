package rotini

import (
	"regexp"
	"runtime/debug"
	"time"
)

// readBuildInfo is a seam over [debug.ReadBuildInfo] so tests can supply
// synthetic build information (and exercise the no-info path).
var readBuildInfo = debug.ReadBuildInfo

// BuildInformation is a JSON-friendly projection of [debug.BuildInfo]. The standard
// struct buries the most-asked-for build facts (the VCS revision, whether the
// tree was dirty, the target platform, the toolchain) inside an untyped
// []debug.BuildSetting slice, which marshals to an awkward array of {Key,Value}
// pairs. BuildInformation lifts those settings into named, individually-tagged
// fields so a `version`/`--version` command can emit a clean, stable object.
//
// [BuildInfo] reads the live build info and resolves every field. A BuildInformation
// may carry a fallback supplying values to use when the embedded build metadata
// is missing — common for `go run`, `go test`, or binaries built without module
// information, where the runtime reports a "(devel)" or empty Version.
//
// BuildInformation deliberately models only Go's embedded build metadata. Extra,
// build-system-supplied values (e.g. a GoReleaser commit or date) belong in the
// program registry as their own bindings — `Bind("commit", commit)` — retrieved
// where needed with [MustGet]; they are not grafted onto this type.
type BuildInformation struct {
	// Version is the main module's version: a release tag ("v1.2.3"), a
	// pseudo-version, "(devel)" for an unstamped build, or "" outside a module.
	Version string `json:"version,omitempty"`
	// GoVersion is the toolchain that produced the build, e.g. "go1.22.0".
	GoVersion string `json:"goVersion,omitempty"`
	// Path is the main package's import path.
	Path string `json:"path,omitempty"`
	// Module is the main module's path.
	Module string `json:"module,omitempty"`
	// OS is the target operating system (the GOOS build setting).
	OS string `json:"os,omitempty"`
	// Arch is the target architecture (the GOARCH build setting).
	Arch string `json:"arch,omitempty"`
	// Compiler is the compiler used, "gc" or "gccgo" (the -compiler setting).
	Compiler string `json:"compiler,omitempty"`
	// CGO reports whether cgo was enabled (CGO_ENABLED == "1").
	CGO bool `json:"cgo,omitempty"`
	// VCS is the version-control system the build was stamped from, e.g. "git";
	// empty when the build carried no VCS information.
	VCS string `json:"vcs,omitempty"`
	// Revision is the full commit hash the build was made from (vcs.revision).
	Revision string `json:"revision,omitempty"`
	// Time is the commit timestamp (vcs.time); zero when unstamped.
	Time time.Time `json:"time,omitzero"`
	// Dirty reports whether the working tree had uncommitted changes at build
	// time (vcs.modified == "true").
	Dirty bool `json:"dirty,omitempty"`
	// fallback supplies values for fields the live build info leaves empty.
	fallback *BuildInformation
}

// BuildInfo reads the binary's embedded build information and returns a fully
// resolved BuildInformation. fallback (may be nil) supplies values for fields the
// live build info leaves empty.
func BuildInfo(fallback *BuildInformation) *BuildInformation {
	b := &BuildInformation{
		fallback: fallback,
	}

	info, ok := readBuildInfo()
	if !ok {
		b.overlay()
		return b
	}

	b.Version = info.Main.Version
	b.GoVersion = info.GoVersion
	b.Path = info.Path
	b.Module = info.Main.Path

	for _, s := range info.Settings {
		switch s.Key {
		case "GOOS":
			b.OS = s.Value
		case "GOARCH":
			b.Arch = s.Value
		case "-compiler":
			b.Compiler = s.Value
		case "CGO_ENABLED":
			b.CGO = s.Value == "1"
		case "vcs":
			b.VCS = s.Value
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.time":
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				b.Time = t
			}
		case "vcs.modified":
			b.Dirty = s.Value == "true"
		}
	}

	b.overlay()

	return b
}

// overlay fills b's empty fields from b.fallback: a "(devel)" or empty Version
// defers to the fallback, and each other field still at its zero value inherits
// the fallback's. Booleans are not overlaid: false is indistinguishable from
// unset, and the live build settings are the authoritative source for them.
func (b *BuildInformation) overlay() {
	fb := b.fallback
	if fb == nil {
		return
	}

	if b.Version == "" || b.Version == "(devel)" {
		b.Version = fb.Version
	}

	if b.GoVersion == "" {
		b.GoVersion = fb.GoVersion
	}

	if b.Path == "" {
		b.Path = fb.Path
	}

	if b.Module == "" {
		b.Module = fb.Module
	}

	if b.OS == "" {
		b.OS = fb.OS
	}

	if b.Arch == "" {
		b.Arch = fb.Arch
	}

	if b.Compiler == "" {
		b.Compiler = fb.Compiler
	}

	if b.VCS == "" {
		b.VCS = fb.VCS
	}

	if b.Revision == "" {
		b.Revision = fb.Revision
	}

	if b.Time.IsZero() {
		b.Time = fb.Time
	}
}

// versionCoreRe matches the leading semantic-version core (MAJOR.MINOR.PATCH) of
// a Go module version, with an optional "v" prefix.
var versionCoreRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`)

// CleanVersion returns the bare MAJOR.MINOR.PATCH core of [BuildInformation.Version],
// dropping the leading "v", any pseudo-version suffix, and build metadata — e.g.
// "v0.0.0-20260610090009-0ddb0aafb83c+dirty" becomes "0.0.0". It returns "" when
// Version carries no recognizable semver core (e.g. "" or "(devel)").
func (b *BuildInformation) CleanVersion() string {
	m := versionCoreRe.FindStringSubmatch(b.Version)
	if m == nil {
		return ""
	}
	return m[1]
}
