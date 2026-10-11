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

// recipeBlocks pins each code block on the recipes page to the file an r12 script builds or
// compares, so the page shows code and output that are known to work.
var recipeBlocks = []pinnedBlock{
	{"internal/cmd/lazydemo/store.go", "r12_recipe_lazy_dep.txtar", "store.go.txt"},
	{"internal/cmd/lazydemo/lazydemo_get.go", "r12_recipe_lazy_dep.txtar", "get.go.txt"},
	{"internal/cmd/mandemo/mandemo_man.go", "r12_recipe_man_all.txtar", "man.go.txt"},
	{"$ taskr __complete list --status ''", "r12_recipe_complete_debug.txtar", "status.txt"},
	{"$ taskr __complete add ''", "r12_recipe_complete_debug.txtar", "title.txt"},
	{"$ taskr __complete list --out ''", "r12_recipe_complete_debug.txtar", "out.txt"},
	{"internal/cmd/tracedemo/tracedemo.go", "r12_recipe_otel.txtar", "root.go.txt"},
	{"internal/cmd/tracedemo/tracedemo_work.go", "r12_recipe_otel.txtar", "work.go.txt"},
	{"internal/cmd/teedemo/teedemo_export.go", "r12_recipe_tee.txtar", "export.go.txt"},
	{"internal/cmd/app/app_complete.go", "r1_config_profiles.txtar", "complete.go.txt"},
	{"cmd/gh-hello/.rotini.spec.yaml", "r12_recipe_gh_extension.txtar", "spec.yaml.txt"},
	{"cmd/tsdemo/main_test.go", "r12_recipe_testscript.txtar", "main_test.go.txt"},
	{"cmd/tsdemo/testdata/script/hello.txtar", "r12_recipe_testscript.txtar", "hello.txtar.txt"},
	{"$ coverage of a built binary", "r12_recipe_cover.txtar", "cover.sh.txt"},
	{"internal/cmd/fuzzdemo/fuzz_test.go", "r12_recipe_fuzz.txtar", "fuzz_test.go.txt"},
	{"internal/cmd/logdemo/logdemo.go", "r12_recipe_verbosity.txtar", "root.go.txt"},
	{"internal/cmd/logdemo/logdemo_run.go", "r12_recipe_verbosity.txtar", "run.go.txt"},
	{"internal/cmd/profdemo/profdemo.go", "r12_recipe_profile.txtar", "root.go.txt"},
	{"internal/cmd/crashdemo/report.go", "r12_recipe_crash.txtar", "report.go.txt"},
	{"cmd/crashdemo/main.go", "r12_recipe_crash.txtar", "main.go.txt"},
	{"internal/cmd/secdemo/secdemo_sync.go", "r12_recipe_secrets.txtar", "sync.go.txt"},
	{"internal/cmd/origindemo/origindemo_config_list.go", "r12_recipe_show_origin.txtar", "list.go.txt"},
	{"internal/cmd/reloaddemo/reloaddemo_serve.go", "r12_recipe_reload.txtar", "serve.go.txt"},
	{"internal/cmd/promptdemo/promptdemo_greet.go", "r12_recipe_prompting.txtar", "greet.go.txt"},
	{"internal/cmd/updemo/notify.go", "r12_recipe_update_notice.txtar", "notify.go.txt"},
	{"internal/cmd/cfgctl/cfgctl_config_set.go", "r1_config_set.txtar", "set.go.txt"},
}

// guideBlocks pins code blocks in the guide the same way.
var guideBlocks = []pinnedBlock{
	{"internal/cmd/todo/report.go", "r12_recipe_exit_codes.txtar", "report.go.txt"},
	{"cmd/todo/main.go (time limit)", "r12_runtime_control.txtar", "main.go.txt"},
	{"internal/cmd/todo/client.go", "r12_runtime_control.txtar", "client.go.txt"},
	{"internal/cmd/todo/conventions.go", "r12_conventions.txtar", "conventions.go.txt"},
	{"internal/cmd/todo/report_debug.go", "r12_conventions.txtar", "report_debug.go.txt"},
	{"cmd/todo/main.go (build info)", "r12_recipe_version.txtar", "main.go.txt"},
	{"cmd/logs/.rotini.spec.yaml (filter)", "r12_recipe_filter.txtar", "spec.yaml.txt"},
	{"internal/cmd/logs/logs_grep.go (filter)", "r12_recipe_filter.txtar", "grep.go.txt"},
	{"internal/cmd/todo/todo_deploy.go", "r12_explain_inputs.txtar", "deploy.go.txt"},
}

func TestRecipeBlocksMatchScripts(t *testing.T) {
	checkPinnedBlocks(t, filepath.Join("recipes", "_index.md"), recipeBlocks)
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
