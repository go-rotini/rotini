package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaleSpec is a synthetic spec: a chain of depth nested commands, each with aliases aliases,
// and wide leaf commands under the root, each with three flags.
func scaleSpec(depth, aliases, wide int) string {
	var b strings.Builder
	b.WriteString("version: 0.0.0\ncommand:\n  name: big\n  summary: s\n  commands:\n")
	indent := "    "
	for d := range depth {
		fmt.Fprintf(&b, "%s- name: lvl%d\n%s  summary: level %d\n", indent, d, indent, d)
		if aliases > 0 {
			names := make([]string, aliases)
			for i := range names {
				names[i] = fmt.Sprintf("l%da%d", d, i)
			}
			fmt.Fprintf(&b, "%s  aliases: [%s]\n", indent, strings.Join(names, ", "))
		}
		if d < depth-1 {
			fmt.Fprintf(&b, "%s  commands:\n", indent)
			indent += "    "
		}
	}
	for i := range wide {
		fmt.Fprintf(&b, "    - name: cmd%d\n      summary: command %d\n      flags:\n", i, i)
		for f := range 3 {
			fmt.Fprintf(&b, "        - name: flag%d\n          identifiers: [--flag%d]\n          schema: { type: string }\n", f, f)
		}
	}
	return b.String()
}

const scaleConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/big/zz_rotini.go
      package: big
  features:
    - type: help
      enabled: true
    - type: man
      enabled: true
    - type: markdown
      enabled: true
`

// generateScale generates spec in a fresh module and returns the generated file's size.
func generateScale(tb testing.TB, spec string) int {
	tb.Helper()
	dir := tb.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.com/big\n\ngo 1.26\n", ".rotini.spec.yaml": spec, ".rotini.conf.yaml": scaleConf} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	tb.Chdir(dir)
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, err error) {
		if err != nil {
			tb.Fatal(err)
		}
	}, func([]error) {}); err != nil {
		tb.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "internal", "cmd", "big", "zz_rotini.go"))
	if err != nil {
		tb.Fatal(err)
	}
	return int(info.Size())
}

// TestGenerateScale_aliasesStayLinear pins that aliases grow the generated file with the
// number of spellings, not with every combination of them: six levels of four aliases each
// stay well under what one spelled-out case per combination (4^6 per resolver) would take.
func TestGenerateScale_aliasesStayLinear(t *testing.T) {
	with := generateScale(t, scaleSpec(6, 4, 0))
	without := generateScale(t, scaleSpec(6, 0, 0))
	if with-without > 8<<10 {
		t.Errorf("six levels of four aliases add %d bytes to the generated file, want under 8 KiB", with-without)
	}
}

// BenchmarkGenerate_largeSpec measures a generate pass over a deep aliased chain plus a few
// hundred commands with flags, with help, man and markdown on.
func BenchmarkGenerate_largeSpec(b *testing.B) {
	spec := scaleSpec(8, 4, 300)
	for b.Loop() {
		generateScale(b, spec)
	}
}
