package codegen

import (
	"path/filepath"
	"strings"
	"testing"
)

// renderedPages renders every page of one doc feature from the built-in template, keyed by the
// page's invocation ("app", "app deploy", "app help filters").
func renderedPages(t *testing.T, spec string, feat docFeature) map[string]string {
	t.Helper()
	decoded := decodeSpecYAML(t, spec)
	gp, err := resolveTree(decoded, filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.spec = decoded
	nodes := flattenFeature(gp, feat)
	contents, err := docFeatureContents(nil, "", nodes, feat, false)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(nodes))
	for i, n := range nodes {
		out[n.name] = contents[i]
	}
	return out
}

const consistencySpec = `version: 0.0.0
command:
  name: app
  summary: the app
  env_prefix: APP
  config_files:
    - name: user
      path: ~/.app.yaml
  flags:
    - name: token
      summary: the api token
      identifiers: [--token]
      cascading: true
      schema: {type: string, required: true}
    - name: region
      summary: the region
      identifiers: [--region]
      cascading: true
      schema: {type: string, default: eu-1, enum: [eu-1, us-1]}
    - name: trace
      summary: a hidden tracer
      identifiers: [--trace]
      cascading: true
      hidden: true
      schema: {type: bool}
    - name: local
      summary: a root-only flag
      identifiers: [--local]
      schema: {type: int, default: 7}
  commands:
    - name: deploy
      summary: deploy a service
      aliases: [dep]
      group: Core
      flags:
        - name: replicas
          summary: how many copies
          identifiers: [--replicas, -r]
          schema: {type: int, default: 2}
        - name: region
          summary: where this deploy goes
          identifiers: [--region]
          schema: {type: string}
        - name: secretive
          summary: a hidden switch
          identifiers: [--secretive]
          hidden: true
          schema: {type: bool}
        - name: old
          summary: an old switch
          identifiers: [--old]
          deprecated: use --replicas
          schema: {type: bool}
      commands:
        - name: rollback
          summary: undo a deploy
    - name: status
      summary: show the status
      deprecated: use deploy
    - name: internal
      summary: a hidden helper
      hidden: true
      flags:
        - name: dump
          summary: dump state
          identifiers: [--dump]
          schema: {type: bool}
`

// TestPages_agreeWithTheContract pins that help, man and markdown show what the contract says
// each command offers: every visible sub-command and flag (its own, and the cascading ones it
// inherits and does not redeclare) appears on all three pages with its default and required
// marker, and nothing the contract marks hidden appears on any.
func TestPages_agreeWithTheContract(t *testing.T) {
	_, commands := contractJSON(t, consistencySpec)
	pages := map[string]map[string]string{
		"help":     renderedPages(t, consistencySpec, helpFeatureDesc),
		"man":      renderedPages(t, consistencySpec, manFeatureDesc),
		"markdown": renderedPages(t, consistencySpec, markdownFeatureDesc),
	}
	// row returns the text a page gives one flag: its line in help and markdown, and its tag
	// and body lines in man.
	row := func(feature, page, id string) (string, bool) {
		lines := strings.Split(page, "\n")
		needle := id
		if feature == "man" {
			needle = `\fB` + roffEscapeLine(id) + `\fR`
		}
		for i, l := range lines {
			if !strings.Contains(l, needle) {
				continue
			}
			if feature == "man" && i+1 < len(lines) {
				return l + "\n" + lines[i+1], true
			}
			return l, true
		}
		return "", false
	}

	checked := 0
	for name, cmd := range commands {
		if cmd["hidden"] == true {
			continue
		}
		for feature, byName := range pages {
			page, ok := byName[name]
			if !ok {
				t.Errorf("%s has no page for %q", feature, name)
				continue
			}
			flags, _ := cmd["flags"].([]any)
			for _, f := range flags {
				flag := f.(map[string]any)
				ids, _ := flag["identifiers"].([]any)
				long := ids[0].(string)
				got, shown := row(feature, page, long)
				if flag["hidden"] == true {
					if shown {
						t.Errorf("%s page of %q shows the hidden flag %s", feature, name, long)
					}
					continue
				}
				if !shown {
					t.Errorf("%s page of %q does not show %s:\n%s", feature, name, long, page)
					continue
				}
				checked++
				if flag["required"] == true && !strings.Contains(got, "(required)") {
					t.Errorf("%s page of %q does not mark %s required: %q", feature, name, long, got)
				}
				if flag["deprecated"] != nil && !strings.Contains(got, "deprecated") {
					t.Errorf("%s page of %q does not mark %s deprecated: %q", feature, name, long, got)
				}
				schema, _ := flag["schema"].(map[string]any)
				enum, _ := schema["enum"].([]any)
				for _, v := range enum {
					want := v.(string)
					if feature == "man" {
						want = roffEscapeLine(want)
					}
					if !strings.Contains(got, want) {
						t.Errorf("%s page of %q does not list %s's value %s: %q", feature, name, long, want, got)
					}
				}
				if def, ok := schema["default"]; ok {
					want := defaultString(def)
					if feature == "man" {
						want = roffEscapeLine(want)
					}
					if !strings.Contains(got, want) || !strings.Contains(got, "default") {
						t.Errorf("%s page of %q does not show %s's default %s: %q", feature, name, long, want, got)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no flag was checked")
	}

	for feature, byName := range pages {
		for name, page := range byName {
			if name == "app internal" {
				continue // a hidden command still has its own page
			}
			for _, hidden := range []string{"a hidden helper", "a hidden switch", "a hidden tracer"} {
				if strings.Contains(page, hidden) {
					t.Errorf("%s page of %q shows %q, which is hidden", feature, name, hidden)
				}
			}
		}
		root := byName["app"]
		for _, visible := range []string{"deploy a service", "show the status"} {
			if !strings.Contains(root, visible) {
				t.Errorf("the root's %s page does not list %q", feature, visible)
			}
		}
		for l := range strings.SplitSeq(root, "\n") {
			if strings.Contains(l, "show the status") && !strings.Contains(l, "deprecated") {
				t.Errorf("the root's %s page does not mark status deprecated: %q", feature, l)
			}
		}
		// The nearer --region replaces the root's on deploy's page.
		if deploy := byName["app deploy"]; strings.Contains(deploy, "the region") {
			t.Errorf("deploy's %s page shows the root's --region, which deploy redeclares", feature)
		}
		if rollback := byName["app deploy rollback"]; !strings.Contains(rollback, "the region") {
			t.Errorf("rollback's %s page does not show the root's cascading --region", feature)
		}
	}
}

// TestHelpPage_multilineExampleIndented pins that every line of a continued example stays in
// the Examples block's indent, as in man and markdown, instead of falling to column 0.
func TestHelpPage_multilineExampleIndented(t *testing.T) {
	const spec = `version: 0.0.0
command:
  name: app
  summary: the app
  examples:
    - |-
      app deploy web \
      --replicas 3
    - app status
`
	got := renderedPages(t, spec, helpFeatureDesc)["app"]
	want := "Examples:\n  app deploy web \\\n  --replicas 3\n  app status"
	if !strings.Contains(got, want) {
		t.Errorf("help page examples:\n%s\nwant them to contain:\n%s", got, want)
	}
}
