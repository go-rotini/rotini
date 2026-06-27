package internal

// `rotini mod` engine (W8/D-W8.4b): fetch every external (git/raw) `$ref` a spec tree
// reaches, pin each to an immutable revision + content hash, and write the `.rotini.lock`
// + content-addressed cache. Codegen then reads those hermetically (see loadLockedExternal)
// — fetching is confined here, never to a generate/validate pass. Module-resolved (mod://)
// and local refs are followed (to reach nested external refs) but not locked: go.sum and
// the filesystem are their pins. The fetch backends are package vars so tests stub them.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	// gitFetchFunc resolves git::<repo>@<ref> and returns the immutable revision the ref
	// points at plus the bytes of <subpath> at that revision. Overridable in tests.
	gitFetchFunc = gitFetch
	// httpFetchFunc fetches a raw URL's bytes. Overridable in tests.
	httpFetchFunc = httpFetch
)

// ModFn is the signature of [Processor.Mod]. A command handler binds it under a registry
// key and fetches it as an injectable service, so tests substitute a double (see
// [GenerateFn]).
type ModFn = func(specPath string) error

// populateLock fetches + pins every external `$ref` reachable from spec into the
// module's lock + cache, then writes them. It is what `rotini mod` runs.
func populateLock(spec *Spec, specPath, moduleRoot, moduleName string) error {
	lock, err := readLockfile(moduleRoot)
	if err != nil {
		return err
	}
	absSpec := specPath
	if a, err := filepath.Abs(specPath); err == nil {
		absSpec = filepath.Clean(a)
	}
	seen := map[string]bool{absSpec: true}
	if err := collectExternal(spec.Command.Commands, filepath.Dir(absSpec), moduleName, moduleRoot, lock, seen); err != nil {
		return err
	}
	return writeLockfile(moduleRoot, lock)
}

// collectExternal walks a command list resolving every `$ref`: an external (git/raw)
// ref is fetched, pinned into lock+cache, and recursed into; a mod:///local ref is
// loaded and recursed into (to reach nested external refs); an inline command recurses
// against the same base.
func collectExternal(cmds []Command, base, moduleName, moduleRoot string, lock map[string]lockEntry, seen map[string]bool) error {
	for _, c := range cmds {
		if c.Ref == "" {
			if err := collectExternal(c.Commands, base, moduleName, moduleRoot, lock, seen); err != nil {
				return err
			}
			continue
		}
		locator, err := locateRef(base, c.Ref)
		if err != nil {
			return fmt.Errorf("resolve %q: %w", c.Ref, err)
		}
		if seen[locator] {
			continue
		}
		seen[locator] = true

		var childSpec *Spec
		var childBase string
		if isExternalLocator(locator) {
			entry, data, err := fetchAndPin(locator)
			if err != nil {
				return err
			}
			if err := cacheWrite(moduleRoot, entry.hash, data); err != nil {
				return err
			}
			lock[locator] = entry
			childSpec, err = decodeData[Spec](entry.format, data, locator)
			if err != nil {
				return fmt.Errorf("decode fetched %q: %w", locator, err)
			}
			childBase = externalChildBase(locator)
		} else {
			rr, err := loadRef(locator, moduleName, moduleRoot)
			if err != nil {
				return fmt.Errorf("resolve %q: %w", c.Ref, err)
			}
			childSpec, childBase = rr.spec, rr.childBase
		}
		// Recurse into the ref'd spec's own subtree (its base) and the siblings authored
		// next to the ref (this spec's base).
		if err := collectExternal(childSpec.Command.Commands, childBase, moduleName, moduleRoot, lock, seen); err != nil {
			return err
		}
		if err := collectExternal(c.Commands, base, moduleName, moduleRoot, lock, seen); err != nil {
			return err
		}
	}
	return nil
}

// fetchAndPin fetches an external locator live, validates it is a version-matched spec,
// and returns its lock entry (revision + content hash + format + version) plus the bytes.
func fetchAndPin(locator string) (lockEntry, []byte, error) {
	var (
		revision string
		data     []byte
		format   fileFormat
		err      error
	)
	switch {
	case strings.HasPrefix(locator, gitScheme):
		repo, ref, sub, perr := parseGitLocator(locator)
		if perr != nil {
			return lockEntry{}, nil, perr
		}
		revision, data, err = gitFetchFunc(repo, ref, sub)
		format = detectFileFormat(sub)
	default: // https
		data, err = httpFetchFunc(locator)
		format = detectFileFormat(locator)
	}
	if err != nil {
		return lockEntry{}, nil, fmt.Errorf("fetch %q: %w", locator, err)
	}
	spec, err := decodeData[Spec](format, data, locator)
	if err != nil {
		return lockEntry{}, nil, fmt.Errorf("fetched %q is not a valid spec: %w", locator, err)
	}
	return lockEntry{revision: revision, hash: hashBytes(data), format: format, schema: specSchemaVersion(spec.Version)}, data, nil
}

// specSchemaVersion normalizes a composed spec's declared `version` for the lock entry
// ("-" when absent — the version guard has already vetted it when a version is enforced).
func specSchemaVersion(docVersion string) string {
	if v := strings.TrimPrefix(docVersion, "v"); v != "" {
		return v
	}
	return "-"
}

// gitFetch shallow-fetches git::<repo>@<ref> and returns the resolved commit + the bytes
// of subpath at it. It rides the system `git` (auth via the user's git credentials/SSH —
// D-W8.6).
func gitFetch(repo, ref, subpath string) (string, []byte, error) {
	tmp, err := os.MkdirTemp("", "rotini-git-")
	if err != nil {
		return "", nil, fmt.Errorf("mktemp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	// A branch or tag clones shallowly; a commit SHA needs init+fetch (it can't be a
	// clone --branch target).
	clone := exec.Command("git", "clone", "--quiet", "--depth", "1", "--branch", ref, "--single-branch", repo, tmp)
	if out, cerr := clone.CombinedOutput(); cerr != nil {
		if ferr := gitFetchRevision(tmp, repo, ref); ferr != nil {
			return "", nil, fmt.Errorf("git fetch %s@%s: %w (%s)", repo, ref, ferr, strings.TrimSpace(string(out)))
		}
	}
	rev, err := exec.Command("git", "-C", tmp, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", nil, fmt.Errorf("git rev-parse %s@%s: %w", repo, ref, err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, filepath.FromSlash(subpath)))
	if err != nil {
		return "", nil, fmt.Errorf("read %s from %s@%s: %w", subpath, repo, ref, err)
	}
	return strings.TrimSpace(string(rev)), data, nil
}

func gitFetchRevision(dir, repo, rev string) error {
	for _, args := range [][]string{
		{"-C", dir, "init", "--quiet"},
		{"-C", dir, "remote", "add", "origin", repo},
		{"-C", dir, "fetch", "--quiet", "--depth", "1", "origin", rev},
		{"-C", dir, "checkout", "--quiet", "FETCH_HEAD"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w (%s)", args[2], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// httpFetch GETs a raw spec URL.
func httpFetch(rawURL string) ([]byte, error) {
	resp, err := http.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: HTTP %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rawURL, err)
	}
	return data, nil
}
