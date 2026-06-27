package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readLockfile parses the lock that codegen reads. The writer lived with
// `rotini mod` (removed), so the fixture is written directly in the on-disk
// format: "<ref> <revision> <hash> <format> <schema>" lines, "-" for an empty
// revision, "#" comment lines skipped.
func TestReadLockfile(t *testing.T) {
	root := t.TempDir()
	const gitRef = "git::https://github.com/acme/clis@v1/deploy/.rotini.spec.yaml"
	const rawRef = "https://example.com/cli/.rotini.spec.json"
	content := "# rotini.lock — machine-managed pins for external $ref specs.\n" +
		"# Fields: <ref> <revision> <hash> <format> <schema>\n" +
		gitRef + " abc123 sha256:dead yaml 1.2.3\n" +
		rawRef + " - sha256:beef json 1.2.3\n"
	if err := os.WriteFile(lockfilePath(root), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := readLockfile(root)
	if err != nil {
		t.Fatalf("readLockfile: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(out), out)
	}
	if git := out[gitRef]; git.revision != "abc123" || git.hash != "sha256:dead" || git.format != formatYAML || git.schema != "1.2.3" {
		t.Errorf("git entry = %+v", git)
	}
	// A "-" revision placeholder decodes back to empty.
	if r := out[rawRef].revision; r != "" {
		t.Errorf("empty revision = %q, want empty", r)
	}

	// A missing lock file is an empty (not erroneous) lock.
	empty, err := readLockfile(t.TempDir())
	if err != nil || len(empty) != 0 {
		t.Errorf("missing lock = %v, %v; want empty, nil", empty, err)
	}
}

func TestCacheReadAndHash(t *testing.T) {
	root := t.TempDir()
	data := []byte("hello spec")
	h := hashBytes(data)
	if !strings.HasPrefix(h, "sha256:") || len(h) != len("sha256:")+64 {
		t.Fatalf("hash = %q, want sha256:<64 hex>", h)
	}

	// Place the content in the cache directly (the cache writer left with `rotini mod`).
	p := cachePath(root, h)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := cacheRead(root, h); err != nil || string(got) != "hello spec" {
		t.Errorf("cacheRead = %q, %v", got, err)
	}
	if _, err := cacheRead(root, "sha256:0000"); err == nil {
		t.Error("cacheRead(miss) = nil err, want an error")
	}
}
