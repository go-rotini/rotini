package codegen

import (
	"os/exec"
	"strings"
	"testing"
)

const aliasResolverSpec = `version: 0.0.0
command:
  name: acme
  summary: s
  commands:
    - name: deploy
      summary: deploy
      aliases: [d, dep]
      hidden_aliases: [dp]
      commands:
        - name: svc
          summary: svc
          aliases: [s]
          commands:
            - name: up
              summary: up
              aliases: [u]
    - name: list
      summary: list
      aliases: [ls]
`

const aliasResolverConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
  features:
    - type: help
      enabled: true
`

// aliasResolverCheck is a program that prints, for each argument (a space-separated path), what
// Help and Usage return for it, one line each.
const aliasResolverCheck = `package main

import (
	"fmt"
	"os"
	"strings"

	"example.com/acme/internal/cmd/acme"
)

func main() {
	for _, arg := range os.Args[1:] {
		path := strings.Fields(arg)
		if arg == "-" {
			path = nil
		}
		help, herr := acme.Help(path...)
		usage, uerr := acme.Usage(path...)
		fmt.Printf("%q %q %v %v\n", help, usage, herr != nil, uerr != nil)
	}
}
`

// TestAliasResolvers_acceptEverySpelling pins that Help and Usage answer every spelling of a
// command's path, names and aliases mixed at each level, with that command's page, and refuse
// an alias used at another level, while emitting one case per command.
func TestAliasResolvers_acceptEverySpelling(t *testing.T) {
	skipUnlessCompiling(t)
	dir, _ := emitModule(t, aliasResolverSpec, aliasResolverConf)
	writeTestFile(t, dir, "cmd/check/main.go", aliasResolverCheck)
	src := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	if strings.Contains(src, `case "d svc"`) || !strings.Contains(src, `case "deploy svc up":`) {
		t.Fatalf("resolvers still spell out every alias permutation:\n%s", src)
	}
	buildEmitted(t, dir)

	chains := map[string][][]string{
		"-":             nil,
		"deploy":        {{"deploy", "d", "dep", "dp"}},
		"deploy svc":    {{"deploy", "d", "dep", "dp"}, {"svc", "s"}},
		"deploy svc up": {{"deploy", "d", "dep", "dp"}, {"svc", "s"}, {"up", "u"}},
		"list":          {{"list", "ls"}},
	}
	run := func(args []string) []string {
		cmd := exec.Command("go", append([]string{"run", "./cmd/check"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go run: %v\n%s", err, out)
		}
		return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	}
	var args, canon []string
	for name, chain := range chains {
		if chain == nil {
			args, canon = append(args, "-"), append(canon, "-")
			continue
		}
		for _, p := range permute(chain) {
			args, canon = append(args, p), append(canon, name)
		}
	}
	got, want := run(args), run(canon)
	for i := range args {
		if got[i] != want[i] || strings.HasSuffix(got[i], "true") {
			t.Errorf("%q: got %s, want %s (as %q)", args[i], got[i], want[i], canon[i])
		}
	}
	for i, line := range run([]string{"s", "d u", "u", "deploy ls", "ls svc"}) {
		if !strings.HasSuffix(line, "true true") {
			t.Errorf("bad path %d resolved: %s", i, line)
		}
	}
}
