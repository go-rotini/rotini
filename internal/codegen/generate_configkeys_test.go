package codegen

import (
	"path/filepath"
	"strings"
	"testing"
)

const configKeysSpec = `version: 0.0.0
command:
  name: acme
  env_prefix: ACME
  config_files:
    - name: user
      path: ~/.acme.yaml
      profiles: { under: profiles, select: profile }
    - name: dotenv
      path: .env
      as: env
  schemas:
    Mount: { type: object, properties: { src: { type: string } } }
  flags:
    - name: profile
      identifiers: [--profile]
      schema: { type: string, variable: ACME_PROFILE }
    - name: config
      identifiers: [--config]
      schema: { type: string, config_source: user }
    - name: timeout
      identifiers: [--timeout]
      schema: { type: duration, key: deploy.timeout, maximum: 1h }
  config:
    - name: level
      schema: { type: string, enum: [debug, info], ignore_case: true }
    - name: token
      hidden: true
      schema: { type: string, secret: true }
    - name: mount
      schema: { $ref: '#/schemas/Mount' }
  env:
    - name: region
      schema: { type: string }
  commands:
    - name: deploy
      hidden: true
      config:
        - name: tags
          schema: { type: array, items: { type: string }, separator: ';', maxItems: 3 }
        - name: replicas
          schema: { type: int, minimum: 1, file: other }
      config_files:
        - name: other
          path: other.yaml
`

func TestFileKeys_rendered(t *testing.T) {
	gp, err := resolveTree(decodeSpecYAML(t, configKeysSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/acme")
	if err != nil {
		t.Fatal(err)
	}
	got := renderInputSettings(gp)
	for _, want := range []string{
		`{Key: "deploy.timeout", Type: "time.Duration", Maximum: rotini.Ptr[float64](3.6e+12)}`,
		`{Key: "level", Type: "string", Enum: []string{"debug", "info"}, IgnoreCase: true}`,
		`{Key: "mount", Type: "Mount", Object: true}`,
		`{Key: "tags", Type: "[]string", Separator: ";", MaxItems: rotini.Ptr(3)}`,
		`{Key: "token", Type: "string", Secret: true}`,
		`{Key: "replicas", Type: "int", Minimum: rotini.Ptr[float64](1)}`,
		`{Key: "ACME_REGION", Type: "string"}`,
		`{Key: "ACME_PROFILE", Type: "string"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("settings lack %s:\n%s", want, got)
		}
	}
	user := got[strings.Index(got, `{Name: "user"`):]
	user = user[:strings.Index(user, "},\n")]
	for _, absent := range []string{`Key: "profile"`, `Key: "config"`, `Key: "replicas"`, `Key: "ACME_`} {
		if strings.Contains(user, absent) {
			t.Errorf("the user file's keys hold %s:\n%s", absent, user)
		}
	}
}
