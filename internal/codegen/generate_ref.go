package codegen

// This file resolves $refs for spec composition. Two source forms are supported:
//
//   - A local relative path, resolved against the referring spec's directory.
//   - "mod://<module>@<version>/<path>", a spec inside a dependency module, read from the
//     module cache and verified by go.sum.
//
// Remote refs (https://, git::, any other URL scheme) are refused, since rotini neither
// fetches nor pins them.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const modScheme = "mod://" // module-resolved ref / locator

// remoteLocatorRe matches a URL scheme (https://, ssh://) or a forced-getter prefix (git::).
var remoteLocatorRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*(://|::)`)

// isExternalLocator reports whether a locator names an unsupported remote source: anything
// with a URL scheme or getter prefix other than mod://. loadRef refuses it.
func isExternalLocator(locator string) bool {
	return !strings.HasPrefix(locator, modScheme) && remoteLocatorRe.MatchString(locator)
}

// refLocatorError reports what is wrong with how a $ref is written, without I/O: a mod://
// locator that doesn't parse, or a remote source. A relative path always passes.
func refLocatorError(ref string) error {
	switch {
	case strings.HasPrefix(ref, modScheme):
		_, _, _, err := parseModLocator(ref)
		return err
	case isExternalLocator(ref):
		return fmt.Errorf("$ref %q is a remote source, which is not supported; use a relative path or mod://<module>@<version>/<path>", ref)
	}
	return nil
}

// refEntryError is a composition error about how a $ref entry in a local spec file is
// written. That file's own validation reports it at the entry, so the composer's copy of it,
// which has no position, is dropped.
type refEntryError struct{ err error }

func (e *refEntryError) Error() string { return e.err.Error() }
func (e *refEntryError) Unwrap() error { return e.err }

// moduleDirFunc resolves a Go module@version to its directory in the module cache. Tests
// override it.
var moduleDirFunc = goModDownloadDir

// resolvedRef is a $ref resolved to a concrete spec, plus the spec's directory (to discover
// its conf and import path), the module it belongs to (the consuming module for a local ref),
// and the base locator for its own relative refs.
type resolvedRef struct {
	spec      *Spec
	dir       string
	module    string
	childBase string
}

// locateRef computes the canonical locator for a $ref evaluated against base, without I/O,
// so callers can cycle-check on the locator before loading. A mod:// ref is absolute; a bare
// ref is relative to base, which is a local directory or a mod:// subtree.
func locateRef(base, ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, modScheme):
		m, v, sub, err := parseModLocator(ref)
		if err != nil {
			return "", err
		}
		return modLocator(m, v, sub), nil
	case isExternalLocator(ref):
		return ref, nil // refused by loadRef
	case strings.HasPrefix(base, modScheme):
		return joinModLocator(base, ref)
	case isExternalLocator(base):
		return ref, nil // refused by loadRef
	default:
		p := filepath.Clean(filepath.Join(base, filepath.FromSlash(ref)))
		if abs, err := filepath.Abs(p); err == nil {
			p = filepath.Clean(abs)
		}
		return p, nil
	}
}

// loadRef reads the spec at a locator and resolves its composition metadata.
// consumingModule is the entry spec's module, which local refs share. External locators
// are refused.
func loadRef(locator, consumingModule string) (resolvedRef, error) {
	switch {
	case strings.HasPrefix(locator, modScheme):
		module, version, sub, err := parseModLocator(locator)
		if err != nil {
			return resolvedRef{}, err
		}
		build, err := goModBuildFunc()
		if err != nil {
			return resolvedRef{}, err
		}
		if err := build.check(module, version); err != nil {
			return resolvedRef{}, err
		}
		cacheDir, err := moduleDirFunc(module, version)
		if err != nil {
			return resolvedRef{}, err
		}
		file := filepath.Join(cacheDir, filepath.FromSlash(sub))
		spec, err := readSpec(file)
		if err != nil {
			return resolvedRef{}, err
		}
		return resolvedRef{
			spec:      spec,
			dir:       filepath.Dir(file),
			module:    module,
			childBase: modLocator(module, version, path.Dir(sub)),
		}, nil
	case isExternalLocator(locator):
		return resolvedRef{}, refLocatorError(locator)
	default:
		spec, err := readSpec(locator)
		if err != nil {
			return resolvedRef{}, err
		}
		dir := filepath.Dir(locator)
		return resolvedRef{spec: spec, dir: dir, module: consumingModule, childBase: dir}, nil
	}
}

// modLocator builds the canonical mod:// locator for a module@version + cleaned subpath.
func modLocator(module, version, sub string) string {
	return modScheme + module + "@" + version + "/" + path.Clean(sub)
}

// parseModLocator splits "mod://<module>@<version>/<sub>" into its parts. The module
// path itself may contain '/', so the version is taken from the first '@' and the
// subpath from the first '/' after it. The subpath is cleaned; "" means the module root.
func parseModLocator(loc string) (module, version, sub string, err error) {
	module, rest, ok := strings.Cut(strings.TrimPrefix(loc, modScheme), "@")
	if !ok || module == "" {
		return "", "", "", fmt.Errorf("invalid module ref %q; want mod://<module>@<version>/<path>", loc)
	}
	if v, s, ok := strings.Cut(rest, "/"); ok {
		version, sub = v, path.Clean(s)
	} else {
		version, sub = rest, "."
	}
	if version == "" {
		return "", "", "", fmt.Errorf("invalid module ref %q; missing version", loc)
	}
	if strings.HasPrefix(module, "-") || strings.HasPrefix(version, "-") {
		return "", "", "", fmt.Errorf("invalid module ref %q; a module path or version can't start with '-'", loc)
	}
	if modSubpathEscapes(sub) {
		return "", "", "", fmt.Errorf("$ref %q escapes its module %s", loc, module)
	}
	return module, version, sub, nil
}

// modSubpathEscapes reports whether a cleaned module subpath leaves the module directory: one
// that is absolute or climbs out with "..", in either separator.
func modSubpathEscapes(sub string) bool {
	s := strings.ReplaceAll(sub, `\`, "/")
	return path.IsAbs(s) || filepath.IsAbs(sub) || filepath.VolumeName(sub) != "" ||
		s == ".." || strings.HasPrefix(s, "../") || strings.Contains(s, "/../") || strings.HasSuffix(s, "/..")
}

// joinModLocator resolves a relative ref against a mod:// base subtree, refusing to
// escape the module.
func joinModLocator(base, ref string) (string, error) {
	module, version, dir, err := parseModLocator(base)
	if err != nil {
		return "", err
	}
	sub := path.Clean(path.Join(dir, filepath.ToSlash(ref)))
	if modSubpathEscapes(sub) {
		return "", fmt.Errorf("relative $ref %q escapes its module %s", ref, module)
	}
	return modLocator(module, version, sub), nil
}

// goModDownloadDir resolves a module@version to its cache directory via `go mod download`,
// which verifies against go.sum. An unavailable module reports Go's error with a `go get` hint.
func goModDownloadDir(module, version string) (string, error) {
	cmd := exec.Command("go", "mod", "download", "-json", module+"@"+version)
	out, runErr := cmd.Output() // -json writes a JSON object to stdout even on failure
	var res struct {
		Dir   string `json:"Dir"`
		Error string `json:"Error"`
	}
	if jsonErr := json.Unmarshal(out, &res); jsonErr != nil {
		// A module that fails verification against go.sum gets no JSON, only Go's stderr.
		if ee, ok := errors.AsType[*exec.ExitError](runErr); ok {
			if msg, _, _ := strings.Cut(strings.TrimSpace(string(ee.Stderr)), "\n"); msg != "" {
				return "", fmt.Errorf("go mod download %s@%s: %s", module, version, msg)
			}
		}
		if runErr != nil {
			return "", fmt.Errorf("go mod download %s@%s: %w", module, version, runErr)
		}
		return "", fmt.Errorf("go mod download %s@%s: %w", module, version, jsonErr)
	}
	if res.Error != "" {
		return "", fmt.Errorf("module %s@%s not available; add it to go.mod (go get %s@%s): %s", module, version, module, version, res.Error)
	}
	if res.Dir == "" {
		return "", fmt.Errorf("module %s@%s resolved to no directory", module, version)
	}
	return res.Dir, nil
}

// goModBuildFunc reads the main module's requirements and replacements. Tests override it.
var goModBuildFunc = readGoModBuild

// goModBuild is what the main module's go.mod says about the modules it builds.
type goModBuild struct {
	Require []struct {
		Path    string `json:"Path"`
		Version string `json:"Version"`
	} `json:"Require"`
	Replace []struct {
		Old goModVersion `json:"Old"`
		New goModVersion `json:"New"`
	} `json:"Replace"`
}

type goModVersion struct {
	Path    string `json:"Path"`
	Version string `json:"Version,omitempty"`
}

// readGoModBuild parses the main module's go.mod (or vendor.mod) with `go mod edit -json`.
func readGoModBuild() (*goModBuild, error) {
	_, _, file, err := findModuleFile()
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("go", "mod", "edit", "-json", file).Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			if msg, _, _ := strings.Cut(strings.TrimSpace(string(ee.Stderr)), "\n"); msg != "" {
				return nil, fmt.Errorf("read %s: %s", file, msg)
			}
		}
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	var b goModBuild
	if err := json.Unmarshal(out, &b); err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return &b, nil
}

// builds returns what go.mod builds module from: the required version, or the replacement
// that applies to it (one naming that version wins over one naming none, as in Go). required
// is false when go.mod doesn't require the module, which Go then doesn't replace either.
func (b *goModBuild) builds(module string) (built goModVersion, required bool) {
	for _, r := range b.Require {
		if r.Path == module {
			built, required = goModVersion{Path: module, Version: r.Version}, true
		}
	}
	if !required {
		return built, false
	}
	var general *goModVersion
	for i := range b.Replace {
		old := b.Replace[i].Old
		switch {
		case old.Path != module:
		case old.Version == built.Version:
			return b.Replace[i].New, true
		case old.Version == "":
			general = &b.Replace[i].New
		}
	}
	if general != nil {
		return *general, true
	}
	return built, true
}

// modVersionError is a mod:// $ref naming another version of its module than go.mod builds.
type modVersionError struct{ msg string }

func (e *modVersionError) Error() string { return e.msg }

// check refuses a mod:// $ref unless go.mod builds module at exactly version, so the spec
// composed is the one released with the code that is compiled.
func (b *goModBuild) check(module, version string) error {
	built, required := b.builds(module)
	var msg string
	switch {
	case !required:
		msg = fmt.Sprintf("it names %s %s, but go.mod does not require %s; add it with go get %s@%s",
			module, version, module, module, version)
	case built.Version == "":
		msg = fmt.Sprintf("it names %s %s, but go.mod replaces %s with the directory %s, which is what builds; remove the replace, or align the $ref or go.mod",
			module, version, module, built.Path)
	case built.Path != module:
		msg = fmt.Sprintf("it names %s %s, but go.mod replaces %s with %s %s, which is what builds; remove the replace, or align the $ref or go.mod",
			module, version, module, built.Path, built.Version)
	case built.Version != version:
		msg = fmt.Sprintf("it names %s %s, but go.mod builds %s at %s; align the $ref or go.mod",
			module, version, module, built.Version)
	default:
		return nil
	}
	return &modVersionError{msg}
}
