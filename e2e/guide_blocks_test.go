//go:build !mutation

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
)

// pinnedBlock ties a code block, found by its title, to the file an e2e script builds or
// compares.
type pinnedBlock struct{ title, script, file string }

// guideBlocks pins each code block in the guide to the file an e2e script builds or compares, so
// the guide shows code and output that are known to work.
var guideBlocks = []pinnedBlock{
	{"store.go", "r12_recipe_lazy_dep.txtar", "store.go.txt"},
	{"lazydemo_get.go", "r12_recipe_lazy_dep.txtar", "get.go.txt"},
	{"mandemo_man.go", "r12_recipe_man_all.txtar", "man.go.txt"},
	{"__complete list --status", "r12_recipe_complete_debug.txtar", "status.txt"},
	{"__complete add", "r12_recipe_complete_debug.txtar", "title.txt"},
	{"__complete list --out", "r12_recipe_complete_debug.txtar", "out.txt"},
	{"tracedemo.go", "r12_recipe_otel.txtar", "root.go.txt"},
	{"tracedemo_work.go", "r12_recipe_otel.txtar", "work.go.txt"},
	{"teedemo_export.go", "r12_recipe_tee.txtar", "export.go.txt"},
	{"app_complete.go", "r1_config_profiles.txtar", "complete.go.txt"},
	{"gh-hello/.rotini.spec.yaml", "r12_recipe_gh_extension.txtar", "spec.yaml.txt"},
	{"main_test.go", "r12_recipe_testscript.txtar", "main_test.go.txt"},
	{"hello.txtar", "r12_recipe_testscript.txtar", "hello.txtar.txt"},
	{"binary coverage", "r12_recipe_cover.txtar", "cover.sh.txt"},
	{"fuzz_test.go", "r12_recipe_fuzz.txtar", "fuzz_test.go.txt"},
	{"logdemo.go", "r12_recipe_verbosity.txtar", "root.go.txt"},
	{"logdemo_run.go", "r12_recipe_verbosity.txtar", "run.go.txt"},
	{"profdemo.go", "r12_recipe_profile.txtar", "root.go.txt"},
	{"crashdemo/report.go", "r12_recipe_crash.txtar", "report.go.txt"},
	{"crashdemo/main.go", "r12_recipe_crash.txtar", "main.go.txt"},
	{"secdemo_sync.go", "r12_recipe_secrets.txtar", "sync.go.txt"},
	{"origindemo_config_list.go", "r12_recipe_show_origin.txtar", "list.go.txt"},
	{"reloaddemo_serve.go", "r12_recipe_reload.txtar", "serve.go.txt"},
	{"promptdemo_greet.go", "r12_recipe_prompting.txtar", "greet.go.txt"},
	{"notify.go", "r12_recipe_update_notice.txtar", "notify.go.txt"},
	{"cfgctl_config_set.go", "r1_config_set.txtar", "set.go.txt"},
	{"todo/report.go", "r12_recipe_exit_codes.txtar", "report.go.txt"},
	{"main.go (time limit)", "r12_runtime_control.txtar", "main.go.txt"},
	{"client.go", "r12_runtime_control.txtar", "client.go.txt"},
	{"conventions.go", "r12_conventions.txtar", "conventions.go.txt"},
	{"report_debug.go", "r12_conventions.txtar", "report_debug.go.txt"},
	{"main.go (build info)", "r12_recipe_version.txtar", "main.go.txt"},
	{".rotini.spec.yaml (filter)", "r12_recipe_filter.txtar", "spec.yaml.txt"},
	{"logs_grep.go (filter)", "r12_recipe_filter.txtar", "grep.go.txt"},
	{"todo_deploy.go", "r12_explain_inputs.txtar", "deploy.go.txt"},
	{"runr_deploy.go", "r10_output_kinds.txtar", "internal/cmd/runr/runr_deploy.go"},
}

func TestGuideBlocksMatchScripts(t *testing.T) {
	checkPinnedBlocks(t, filepath.Join("docs", "_index.md"), guideBlocks)
}

// checkPinnedBlocks fails for each block that isn't on the page exactly once, or that differs
// from its script's file.
func checkPinnedBlocks(t *testing.T, page string, blocks []pinnedBlock) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "docs", "content", page))
	if err != nil {
		t.Fatalf("read %s: %v", page, err)
	}
	for _, b := range blocks {
		if n := strings.Count(string(data), `{{< code title="`+b.title+`"`); n != 1 {
			t.Errorf("%s has %d blocks titled %q, want exactly 1", page, n, b.title)
			continue
		}
		want, _ := codeBlock(string(data), b.title)
		archive, err := txtar.ParseFile(filepath.Join("testdata", "script", b.script))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range archive.Files {
			if f.Name != b.file {
				continue
			}
			found = true
			if got := strings.TrimRight(string(f.Data), "\n"); got != want {
				t.Errorf("%s's %s is not %s's %q block.\n--- script\n%s\n--- page\n%s", b.script, b.file, page, b.title, got, want)
			}
		}
		if !found {
			t.Errorf("%s carries no %s", b.script, b.file)
		}
	}
}

// TestReadmeQuickStartMatchesScript pins r0_readme_quickstart's fixtures to the README's quick
// start: the spec fragment and the handler, so the script proves the README's code works.
func TestReadmeQuickStartMatchesScript(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, quick, ok := strings.Cut(string(readme), "## Quick start")
	if !ok {
		t.Fatal("the README has no quick start")
	}
	quick, _, _ = strings.Cut(quick, "\n## ")
	archive, err := txtar.ParseFile(filepath.Join("testdata", "script", "r0_readme_quickstart.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []struct{ fence, file string }{{"```yaml\n", "hello.spec.yaml.txt"}, {"```go\n", "hello.go.txt"}} {
		_, block, ok := strings.Cut(quick, pin.fence)
		if !ok {
			t.Errorf("the quick start has no %q block", strings.TrimSpace(pin.fence))
			continue
		}
		block, _, _ = strings.Cut(block, "```")
		var got string
		for _, f := range archive.Files {
			if f.Name == pin.file {
				got = string(f.Data)
			}
		}
		if got != block {
			t.Errorf("r0_readme_quickstart's %s is not the README's block.\n--- fixture\n%s\n--- README\n%s", pin.file, got, block)
		}
	}
}
