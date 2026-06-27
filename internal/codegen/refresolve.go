package codegen

// $ref resolution for spec composition (W8/D-W8.4). A command `$ref` names another
// rotini spec to compose; this file turns that ref — relative to where the referring
// spec was loaded from — into a concrete spec plus what composition needs from it. Two
// source forms today:
//
//   - LOCAL relative path  (e.g. "../deploy/.rotini.spec.yaml") — resolved against the
//     referring spec's directory.
//   - MODULE-resolved      "mod://<module>@<version>/<path>" — a spec file inside a Go
//     module the project depends on, read from the module cache (ride go.mod/go.sum —
//     D-W8.5; reproducibility/integrity/caching are Go's, not rotini's).
//
// External git:: and raw https:// refs are NOT supported (rotini neither fetches nor
// pins them); loadRef rejects them.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const (
	modScheme   = "mod://"   // module-resolved ref / locator
	gitScheme   = "git::"    // git-resolved ref / locator: git::<url>@<ref>/<path>
	httpsScheme = "https://" // raw URL ref / locator
)

// isExternalLocator reports whether a locator is a lock-pinned external source
// (git:: or raw https://) — as opposed to a local path or a module-resolved (mod://)
// ref, which ride the filesystem and go.sum respectively.
func isExternalLocator(locator string) bool {
	return strings.HasPrefix(locator, gitScheme) || strings.HasPrefix(locator, httpsScheme)
}

// moduleDirFunc resolves a Go module@version to its extracted directory in the module
// cache. Overridable in tests; production rides `go mod download` (go.sum-verified).
var moduleDirFunc = goModDownloadDir

// resolvedRef is a $ref resolved to a concrete spec plus the metadata composition
// needs: the filesystem dir of the spec (to discover its conf → cli import path), the
// module the spec belongs to (the consuming module for a LOCAL ref, the EXTERNAL module
// for a mod:// ref — so an external child's handlers import from the right module), and
// the base locator for the spec's own relative sub-refs.
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
	case strings.HasPrefix(ref, gitScheme):
		repo, rev, sub, err := parseGitLocator(ref)
		if err != nil {
			return "", err
		}
		return gitLocator(repo, rev, sub), nil
	case strings.HasPrefix(ref, httpsScheme):
		return ref, nil // an absolute URL is its own locator
	case strings.HasPrefix(base, modScheme):
		return joinModLocator(base, ref)
	case strings.HasPrefix(base, gitScheme):
		return joinGitLocator(base, ref)
	case strings.HasPrefix(base, httpsScheme):
		return joinURL(base, ref)
	default: // local relative path against a local directory
		p := filepath.Clean(filepath.Join(base, filepath.FromSlash(ref)))
		if abs, err := filepath.Abs(p); err == nil {
			p = filepath.Clean(abs)
		}
		return p, nil
	}
}

// parseGitLocator splits "git::<url>@<ref>/<sub>". The URL carries no '@' (https git
// remotes don't), so the revision is the first '@'-delimited field and the subpath the
// remainder after the next '/'. The subpath is cleaned; "." means the repo root.
func parseGitLocator(loc string) (repo, rev, sub string, err error) {
	repo, rest, ok := strings.Cut(strings.TrimPrefix(loc, gitScheme), "@")
	if !ok || repo == "" {
		return "", "", "", fmt.Errorf("invalid git ref %q — want git::<url>@<ref>/<path>", loc)
	}
	if r, sp, ok := strings.Cut(rest, "/"); ok {
		rev, sub = r, path.Clean(sp)
	} else {
		rev, sub = rest, "."
	}
	if rev == "" {
		return "", "", "", fmt.Errorf("invalid git ref %q — missing revision", loc)
	}
	return repo, rev, sub, nil
}

func gitLocator(repo, rev, sub string) string {
	return gitScheme + repo + "@" + rev + "/" + path.Clean(sub)
}

func joinGitLocator(base, ref string) (string, error) {
	repo, rev, dir, err := parseGitLocator(base)
	if err != nil {
		return "", err
	}
	sub := path.Clean(path.Join(dir, filepath.ToSlash(ref)))
	if sub == ".." || strings.HasPrefix(sub, "../") {
		return "", fmt.Errorf("relative $ref %q escapes its repo %s", ref, repo)
	}
	return gitLocator(repo, rev, sub), nil
}

// joinURL resolves a relative ref against a raw https base URL (standard URL reference
// resolution — a relative ref resolves against the base document's directory).
func joinURL(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", base, err)
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid $ref URL %q: %w", ref, err)
	}
	return b.ResolveReference(r).String(), nil
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
		return resolvedRef{}, fmt.Errorf("external (git/raw) $ref composition is not supported — use a local path or a mod://<module> $ref")
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
		return "", "", "", fmt.Errorf("invalid module ref %q — want mod://<module>@<version>/<path>", loc)
	}
	if v, s, ok := strings.Cut(rest, "/"); ok {
		version, sub = v, path.Clean(s)
	} else {
		version, sub = rest, "."
	}
	if version == "" {
		return "", "", "", fmt.Errorf("invalid module ref %q — missing version", loc)
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

// goModDownloadDir resolves a module@version to its module-cache directory via the Go
// toolchain — `go mod download` verifies against go.sum and caches, so codegen rides
// Go's reproducibility/integrity (D-W8.5) and adds no machinery of its own. A module
// not resolvable (absent from the build graph, checksum mismatch, …) surfaces Go's own
// error, pointing the author at `go get`.
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
		return "", fmt.Errorf("module %s@%s not available — add it to go.mod (go get %s@%s): %s", module, version, module, version, res.Error)
	}
	if res.Dir == "" {
		return "", fmt.Errorf("module %s@%s resolved to no directory", module, version)
	}
	return res.Dir, nil
}
