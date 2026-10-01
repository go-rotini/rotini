package codegen

// $ref resolution for spec composition: turning a ref, relative to where the referring spec
// was loaded from, into a concrete spec plus what composition needs from it. Two source forms:
//
//   - A local relative path, resolved against the referring spec's directory.
//   - "mod://<module>@<version>/<path>", a spec inside a module the project depends on, read
//     from the module cache — reproducibility and integrity are Go's, not rotini's.
//
// git:: and raw https:// refs are not supported, since rotini neither fetches nor pins them.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const (
	modScheme   = "mod://"   // module-resolved ref / locator
	gitScheme   = "git::"    // a git locator — recognized only so it can be refused
	httpsScheme = "https://" // a raw URL locator — recognized only so it can be refused
)

// isExternalLocator reports whether a locator names an unsupported external source (git:: or a
// raw https:// URL), which loadRef refuses — as opposed to a local path or a module-resolved
// (mod://) ref, which ride the filesystem and go.sum respectively.
func isExternalLocator(locator string) bool {
	return strings.HasPrefix(locator, gitScheme) || strings.HasPrefix(locator, httpsScheme)
}

// moduleDirFunc resolves a Go module@version to its extracted directory in the module
// cache. Overridable in tests; production rides `go mod download` (go.sum-verified).
var moduleDirFunc = goModDownloadDir

// resolvedRef is a $ref resolved to a concrete spec plus what composition needs: the spec's
// directory (to discover its conf, and so its import path), the module it belongs to — the
// consuming module for a local ref, the external one for a mod:// ref — and the base locator
// for its own relative sub-refs.
type resolvedRef struct {
	spec      *Spec
	dir       string
	module    string
	childBase string
}

// locateRef computes the canonical locator for a $ref evaluated against a base. Pure
// string work, no IO — so a caller can cycle-check (the locator is the cycle identity)
// before loading. A scheme-qualified ref (mod://…) is absolute; a bare ref is relative
// to the base, which is itself either a local directory or a mod:// subtree.
func locateRef(base, ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, modScheme):
		m, v, sub, err := parseModLocator(ref)
		if err != nil {
			return "", err
		}
		return modLocator(m, v, sub), nil
	case isExternalLocator(ref):
		return ref, nil // git::/https:// is its own locator; loadRef refuses it (rotini neither fetches nor pins external refs)
	case strings.HasPrefix(base, modScheme):
		return joinModLocator(base, ref)
	case isExternalLocator(base):
		return ref, nil // a relative ref beneath an external base is refused at load too
	default: // local relative path against a local directory
		p := filepath.Clean(filepath.Join(base, filepath.FromSlash(ref)))
		if abs, err := filepath.Abs(p); err == nil {
			p = filepath.Clean(abs)
		}
		return p, nil
	}
}

// loadRef reads the spec at a locator and resolves the composition metadata.
// consumingModule is the module the ENTRY spec belongs to (used for local refs, which
// share the entry's module). Only LOCAL and mod:// refs compose: external git/raw refs
// are not supported (rotini neither fetches nor pins them).
func loadRef(locator, consumingModule string) (resolvedRef, error) {
	switch {
	case strings.HasPrefix(locator, modScheme):
		module, version, sub, err := parseModLocator(locator)
		if err != nil {
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
		return resolvedRef{}, errors.New("external (git/raw) $ref composition is not supported; use a local path or a mod://<module> $ref")
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
	return module, version, sub, nil
}

// joinModLocator resolves a relative ref against a mod:// base subtree, refusing to
// escape the module.
func joinModLocator(base, ref string) (string, error) {
	module, version, dir, err := parseModLocator(base)
	if err != nil {
		return "", err
	}
	sub := path.Clean(path.Join(dir, filepath.ToSlash(ref)))
	if sub == ".." || strings.HasPrefix(sub, "../") {
		return "", fmt.Errorf("relative $ref %q escapes its module %s", ref, module)
	}
	return modLocator(module, version, sub), nil
}

// goModDownloadDir resolves a module@version to its cache directory via `go mod download`,
// which verifies against go.sum, so codegen adds no machinery of its own. An unresolvable
// module surfaces Go's own error, pointing the author at `go get`.
func goModDownloadDir(module, version string) (string, error) {
	cmd := exec.Command("go", "mod", "download", "-json", module+"@"+version)
	out, runErr := cmd.Output() // -json writes a JSON object to stdout even on failure
	var res struct {
		Dir   string `json:"Dir"`
		Error string `json:"Error"`
	}
	if jsonErr := json.Unmarshal(out, &res); jsonErr != nil {
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
