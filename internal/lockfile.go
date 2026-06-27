package internal

// The .rotini.lock + content-addressed cache (W8/D-W8.4b, D-W8.5) — a go.sum for
// external specs. Module-resolved (mod://) refs are NOT locked here: go.sum is their
// lock. git:: and raw https:// refs ARE: the lock pins each authored ref to an
// immutable revision + content hash, and the cache holds the fetched bytes, so codegen
// is hermetic and reproducible (it verifies against the lock, never trusting a moved
// tag or a tampered cache). The lock + cache are committed/vendored; codegen reads them.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	lockfileName = ".rotini.lock"
	cacheSubdir  = ".rotini/cache" // committable (vendored) for hermetic/offline codegen
)

// lockEntry pins one external (git/raw) $ref: the immutable revision it resolved to
// (a commit SHA for git; "" for a raw URL, which the content hash alone pins), the
// sha256 of the fetched spec, its serialization format, and the $schema version
// captured at lock time. The authored ref string is the map key.
type lockEntry struct {
	revision string
	hash     string // "sha256:<hex>"
	format   fileFormat
	schema   string
}

func lockfilePath(moduleRoot string) string { return filepath.Join(moduleRoot, lockfileName) }

// hashBytes returns the canonical content hash of data ("sha256:<hex>").
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// readLockfile parses the module's .rotini.lock into ref→entry. A missing file is an
// empty (not erroneous) lock — nothing has been pinned yet.
func readLockfile(moduleRoot string) (map[string]lockEntry, error) {
	f, err := os.Open(lockfilePath(moduleRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]lockEntry{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", lockfileName, err)
	}
	defer func() { _ = f.Close() }()

	out := map[string]lockEntry{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return nil, fmt.Errorf("%s: malformed entry %q (want: <ref> <revision> <hash> <format> <schema>)", lockfileName, line)
		}
		rev := fields[1]
		if rev == "-" {
			rev = ""
		}
		out[fields[0]] = lockEntry{revision: rev, hash: fields[2], format: fileFormat(fields[3]), schema: fields[4]}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", lockfileName, err)
	}
	return out, nil
}

// cachePath is where a hashed spec is stored — content-addressed, committable
// (vendored) for hermetic/offline codegen.
func cachePath(moduleRoot, hash string) string {
	h := strings.TrimPrefix(hash, "sha256:")
	return filepath.Join(moduleRoot, filepath.FromSlash(cacheSubdir), "sha256", h)
}

func cacheRead(moduleRoot, hash string) ([]byte, error) {
	data, err := os.ReadFile(cachePath(moduleRoot, hash))
	if err != nil {
		return nil, fmt.Errorf("read cache %s: %w", hash, err)
	}
	return data, nil
}
