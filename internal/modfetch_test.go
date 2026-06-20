package internal

import (
	"strings"
	"testing"
)

const modTestSchema = "https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json"

// stubFetchers swaps the git/raw fetch backends for in-memory doubles keyed by repo/URL,
// restoring them when the test ends.
func stubFetchers(t *testing.T, gitByRepo map[string]struct {
	rev  string
	body string
}, httpByURL map[string]string) {
	t.Helper()
	og, oh := gitFetchFunc, httpFetchFunc
	gitFetchFunc = func(repo, _ref, _sub string) (string, []byte, error) {
		g, ok := gitByRepo[repo]
		if !ok {
			t.Fatalf("unexpected git fetch: %s", repo)
		}
		return g.rev, []byte(g.body), nil
	}
	httpFetchFunc = func(url string) ([]byte, error) {
		b, ok := httpByURL[url]
		if !ok {
			t.Fatalf("unexpected http fetch: %s", url)
		}
		return []byte(b), nil
	}
	t.Cleanup(func() { gitFetchFunc, httpFetchFunc = og, oh })
}

func TestFetchAndPin_git(t *testing.T) {
	body := "$schema: " + modTestSchema + "\nname: deploy\n"
	stubFetchers(t, map[string]struct{ rev, body string }{
		"https://github.com/acme/clis": {rev: "deadbeef", body: body},
	}, nil)

	entry, data, err := fetchAndPin("git::https://github.com/acme/clis@v1/deploy/.rotini.spec.yaml", "0.0.0")
	if err != nil {
		t.Fatalf("fetchAndPin: %v", err)
	}
	if entry.revision != "deadbeef" {
		t.Errorf("revision = %q, want deadbeef", entry.revision)
	}
	if entry.hash != hashBytes([]byte(body)) {
		t.Errorf("hash = %q", entry.hash)
	}
	if entry.format != formatYAML {
		t.Errorf("format = %q, want yaml", entry.format)
	}
	if entry.schema != "0.0.0" {
		t.Errorf("schema = %q, want 0.0.0", entry.schema)
	}
	if string(data) != body {
		t.Errorf("data mismatch")
	}
}

// A version mismatch on the fetched spec's $schema is a hard error (D-W8.7).
func TestFetchAndPin_schemaVersionMismatch(t *testing.T) {
	body := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/9.9.9/schema-spec.json\nname: deploy\n"
	stubFetchers(t, nil, map[string]string{
		"https://example.com/cli/.rotini.spec.yaml": body,
	})
	if _, _, err := fetchAndPin("https://example.com/cli/.rotini.spec.yaml", "0.0.0"); err == nil {
		t.Fatalf("want a schema-version error, got nil")
	}
}

// populateLock fetches every external ref a tree reaches (git + raw), writes the lock +
// cache, and the round-trip reads back through loadLockedExternal.
func TestPopulateLock_roundTrip(t *testing.T) {
	root := t.TempDir()
	gitBody := "$schema: " + modTestSchema + "\nname: deploy\n"
	rawBody := "$schema: " + modTestSchema + "\nname: report\n"
	stubFetchers(t, map[string]struct{ rev, body string }{
		"https://github.com/acme/clis": {rev: "abc123", body: gitBody},
	}, map[string]string{
		"https://example.com/report/.rotini.spec.yaml": rawBody,
	})

	gitLoc := "git::https://github.com/acme/clis@v1/deploy/.rotini.spec.yaml"
	rawLoc := "https://example.com/report/.rotini.spec.yaml"
	spec := &Spec{Command: Command{
		Name: "app",
		Commands: []Command{
			{Ref: gitLoc, Handler: &HandlerSource{Import: "deploycli x.com/deploy", Convention: "Handlers"}},
			{Ref: rawLoc, Handler: &HandlerSource{Import: "reportcli x.com/report", Convention: "Handlers"}},
		},
	}}

	if err := populateLock(spec, root+"/.rotini.spec.yaml", root, "x.com/app", "0.0.0"); err != nil {
		t.Fatalf("populateLock: %v", err)
	}

	lock, err := readLockfile(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock) != 2 {
		t.Fatalf("lock has %d entries, want 2: %+v", len(lock), lock)
	}
	if lock[gitLoc].revision != "abc123" {
		t.Errorf("git revision = %q", lock[gitLoc].revision)
	}
	if lock[rawLoc].revision != "" {
		t.Errorf("raw revision = %q, want empty", lock[rawLoc].revision)
	}

	// The cache was populated so codegen can read both hermetically.
	rr, err := loadLockedExternal(gitLoc, root)
	if err != nil {
		t.Fatalf("loadLockedExternal(git): %v", err)
	}
	if rr.spec.Command.Name != "deploy" {
		t.Errorf("git spec name = %q", rr.spec.Command.Name)
	}
	rr2, err := loadLockedExternal(rawLoc, root)
	if err != nil {
		t.Fatalf("loadLockedExternal(raw): %v", err)
	}
	if rr2.spec.Command.Name != "report" {
		t.Errorf("raw spec name = %q", rr2.spec.Command.Name)
	}
}

// A ref-less spec needs no fetching and writes an empty lock without touching the network.
func TestPopulateLock_noExternalRefs(t *testing.T) {
	root := t.TempDir()
	og, oh := gitFetchFunc, httpFetchFunc
	gitFetchFunc = func(_, _, _ string) (string, []byte, error) {
		t.Fatal("git fetch should not run for a ref-less spec")
		return "", nil, nil
	}
	httpFetchFunc = func(_ string) ([]byte, error) {
		t.Fatal("http fetch should not run for a ref-less spec")
		return nil, nil
	}
	defer func() { gitFetchFunc, httpFetchFunc = og, oh }()

	spec := &Spec{Command: Command{Name: "app", Commands: []Command{{Name: "sub"}}}}
	if err := populateLock(spec, root+"/.rotini.spec.yaml", root, "x.com/app", "0.0.0"); err != nil {
		t.Fatalf("populateLock: %v", err)
	}
	lock, err := readLockfile(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock) != 0 {
		t.Errorf("lock = %+v, want empty", lock)
	}
}

func TestSpecSchemaVersion(t *testing.T) {
	if v := specSchemaVersion(modTestSchema); v != "0.0.0" {
		t.Errorf("version = %q, want 0.0.0", v)
	}
	if v := specSchemaVersion(""); v != "-" {
		t.Errorf("absent = %q, want -", v)
	}
	if v := specSchemaVersion("https://example.com/foreign.json"); !strings.HasPrefix(v, "-") {
		t.Errorf("foreign = %q, want -", v)
	}
}
