package internal

import (
	"reflect"
	"strings"
	"testing"
)

func TestLockfileRoundTrip(t *testing.T) {
	root := t.TempDir()
	in := map[string]lockEntry{
		"git::https://github.com/acme/clis@v1/deploy/.rotini.spec.yaml": {revision: "abc123", hash: "sha256:dead", format: formatYAML, schema: "1.2.3"},
		"https://example.com/cli/.rotini.spec.json":                     {revision: "", hash: "sha256:beef", format: formatJSON, schema: "1.2.3"},
	}
	if err := writeLockfile(root, in); err != nil {
		t.Fatalf("writeLockfile: %v", err)
	}
	out, err := readLockfile(root)
	if err != nil {
		t.Fatalf("readLockfile: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", out, in)
	}
	// A raw-URL entry's empty revision round-trips through the "-" placeholder.
	if out["https://example.com/cli/.rotini.spec.json"].revision != "" {
		t.Errorf("empty revision did not round-trip: %+v", out)
	}
	// A missing lock file is an empty (not erroneous) lock.
	empty, err := readLockfile(t.TempDir())
	if err != nil || len(empty) != 0 {
		t.Errorf("missing lock = %v, %v; want empty, nil", empty, err)
	}
}

func TestCacheAndHash(t *testing.T) {
	root := t.TempDir()
	data := []byte("hello spec")
	h := hashBytes(data)
	if !strings.HasPrefix(h, "sha256:") || len(h) != len("sha256:")+64 {
		t.Fatalf("hash = %q, want sha256:<64 hex>", h)
	}
	if err := cacheWrite(root, h, data); err != nil {
		t.Fatalf("cacheWrite: %v", err)
	}
	got, err := cacheRead(root, h)
	if err != nil || string(got) != "hello spec" {
		t.Errorf("cacheRead = %q, %v", got, err)
	}
	if _, err := cacheRead(root, "sha256:0000"); err == nil {
		t.Error("cacheRead(miss) = nil err, want an error")
	}
}
