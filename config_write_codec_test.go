package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// encodeINI writes what decodeINI reads: top-level keys first, then a [section] for each
// mapping, keys sorted. A value nested deeper than a section can't be written.
func encodeINI(doc any) ([]byte, error) {
	m, _ := doc.(map[string]any)
	var top, sections bytes.Buffer
	for _, k := range slices.Sorted(maps.Keys(m)) {
		sub, isSection := m[k].(map[string]any)
		if !isSection {
			fmt.Fprintf(&top, "%s = %v\n", k, m[k])
			continue
		}
		fmt.Fprintf(&sections, "[%s]\n", k)
		for _, sk := range slices.Sorted(maps.Keys(sub)) {
			switch v := sub[sk].(type) {
			case map[string]any, []any:
				return nil, fmt.Errorf("%s.%s: INI holds no nested values", k, sk)
			default:
				fmt.Fprintf(&sections, "%s = %v\n", sk, v)
			}
		}
	}
	return append(top.Bytes(), sections.Bytes()...), nil
}

func iniWriteContext(dir string, opts []CodecOption, files ...ConfigFileDef) *Context {
	opts = append([]CodecOption{CodecExtensions(".ini"), CodecLocate(locateINI)}, opts...)
	return wired(cwContext(dir, nil, files...), ConfigFiles(), Schemas(), Codec("ini", decodeINI, opts...))
}

func iniKeys() []ConfigKeyDef {
	return []ConfigKeyDef{
		{Key: "deploy.region", Type: "string"},
		{Key: "deploy.replicas", Type: "int"},
		{Key: "deploy.hosts", Type: "[]string"},
		{Key: "name", Type: "string"},
		{Key: "token", Type: "string", Secret: true},
	}
}

func TestCodecEncode_setAndUnset(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.ini", acINI)
	rtx := iniWriteContext(dir, []CodecOption{CodecEncode(encodeINI)}, ConfigFileDef{Name: "app", Path: path, Keys: iniKeys()})

	if err := SetConfigValue(rtx, "app", "deploy.region", "us"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "app", "name", "web"); err != nil {
		t.Fatal(err)
	}
	// The file is rewritten whole by the encoder: the comment is gone.
	want := "name = web\n[deploy]\nregion = us\nreplicas = 3\n"
	if got := cwRead(t, path); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}

	// Removing a section's last keys removes the section.
	for _, k := range []string{"deploy.region", "deploy.replicas"} {
		if err := UnsetConfigValue(rtx, "app", k); err != nil {
			t.Fatal(err)
		}
	}
	if got := cwRead(t, path); got != "name = web\n" {
		t.Errorf("file:\n%s", got)
	}

	// Removing an absent key, or setting the value the file holds, doesn't touch the file.
	before, _ := os.Stat(path)
	if err := UnsetConfigValue(rtx, "app", "deploy.region"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "app", "name", "web"); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Error("the file was rewritten")
	}
}

func TestCodecEncode_createsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf", "app.ini")
	rtx := iniWriteContext(dir, []CodecOption{CodecEncode(encodeINI)}, ConfigFileDef{Name: "app", Path: path, Keys: iniKeys()})
	if err := UnsetConfigValue(rtx, "app", "deploy.region"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an unset created the file: %v", err)
	}
	if err := SetConfigValue(rtx, "app", "deploy.replicas", "4"); err != nil {
		t.Fatal(err)
	}
	if got := cwRead(t, path); got != "[deploy]\nreplicas = 4\n" {
		t.Errorf("file:\n%s", got)
	}
	var in acInputs
	files := []ConfigFileDef{{Name: "app", Path: path}}
	if err := NewInputReader(InputSettings{ConfigFiles: files}).Read(acContext(dir, files...), &in); err != nil || in.App.Config.Replicas != 4 {
		t.Errorf("read back = %+v, %v", in.App.Config, err)
	}
}

func TestCodecEncode_errors(t *testing.T) {
	failing := CodecEncode(func(any) ([]byte, error) { return nil, errors.New("disk format v1 only") })
	garbage := CodecEncode(func(any) ([]byte, error) { return []byte("[deploy\n"), nil })
	tests := []struct {
		name, body, key, raw string
		opts                 []CodecOption
		kind                 InputErrorKind
		internal             bool
		want                 string
	}{
		{"a value the encoder can't write", acINI, "deploy.hosts", "a,b", []CodecOption{CodecEncode(encodeINI)}, InputErrorKindWriteRefused, false,
			`app.ini: can't write "deploy.hosts": encode ini: deploy.hosts: INI holds no nested values`},
		{"the encoder fails", acINI, "name", "x", []CodecOption{failing}, InputErrorKindWriteRefused, false, "encode ini: disk format v1 only"},
		{"the encoder writes what doesn't decode", acINI, "name", "x", []CodecOption{garbage}, InputErrorKindInternal, true, "the ini encoder wrote a document its decoder can't read"},
		{"the file doesn't decode", "[deploy]\nreplicas\n", "name", "x", []CodecOption{CodecEncode(encodeINI)}, InputErrorKindMalformedDocument, false,
			"could not parse configuration file "},
		{"a path through a value", "deploy = flat\n", "deploy.region", "x", []CodecOption{CodecEncode(encodeINI)}, InputErrorKindWriteRefused, false,
			`can't edit "deploy.region": deploy holds a value, not a mapping; edit the file by hand`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, "app.ini", tt.body)
			rtx := iniWriteContext(dir, tt.opts, ConfigFileDef{Name: "app", Path: path, Keys: iniKeys()})
			err := SetConfigValue(rtx, "app", tt.key, tt.raw)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant it to contain %s", err, tt.want)
			}
			ie, ok := asError[*InputError](err)
			if !ok || ie.Kind != tt.kind || errors.Is(err, ErrInternal) != tt.internal {
				t.Errorf("err = %v (%T), want kind %v (internal %v)", err, err, tt.kind, tt.internal)
			}
			if got := cwRead(t, path); got != tt.body {
				t.Errorf("the file changed:\n%s", got)
			}
		})
	}
	// The decode error names where the file failed, as a read does.
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.ini", "[deploy]\nreplicas\n")
	err := SetConfigValue(iniWriteContext(dir, []CodecOption{CodecEncode(encodeINI)}, ConfigFileDef{Name: "app", Path: path, Keys: iniKeys()}), "app", "name", "x")
	if want := "could not parse configuration file " + path + ":2:1: expected key = value"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v\nwant %s", err, want)
	}
}

// The same checks as a built-in format: the schema, with the position the codec locates.
func TestCodecEncode_schema(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.ini", acINI)
	f := ConfigFileDef{
		Name: "app", Path: path, Keys: iniKeys(),
		Schema: `{"type":"object","properties":{"deploy":{"type":"object","properties":{"region":{"enum":["eu","us"]}}}}}`,
	}
	err := SetConfigValue(iniWriteContext(dir, []CodecOption{CodecEncode(encodeINI)}, f), "app", "deploy.region", "ap")
	want := "config key deploy.region: configuration file " + path + `:2:10 would be invalid: deploy.region: value is not in enum (got "ap")`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	cwpKind(t, err, InputErrorKindSchemaViolation)
	if got := cwRead(t, path); got != acINI {
		t.Errorf("the file changed:\n%s", got)
	}
}

// jsonCodec is an author format with nesting, for profiles: JSON, encoded indented.
func jsonCodec() Feature {
	decode := func(data []byte) (any, error) {
		var v any
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, nil //nolint:nilnil // an empty document
		}
		err := json.Unmarshal(data, &v)
		return v, err
	}
	encode := func(doc any) ([]byte, error) { return json.MarshalIndent(doc, "", "  ") }
	return Codec("doc", decode, CodecExtensions(".doc"), CodecEncode(encode), CodecLocate(locateJSONC))
}

func TestCodecEncode_profiles(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.doc", `{"region":"eu","profiles":{"prod":{"region":"us"}}}`)
	keys := []ConfigKeyDef{{Key: "region", Type: "string"}, {Key: "db.host", Type: "string"}}
	f := ConfigFileDef{Name: "app", Path: path, Profiles: pfProfiles(), Keys: keys}
	rtx := wired(cwContext(dir, []string{"APP_PROFILE=prod"}, f), ConfigFiles(), Schemas(), jsonCodec())

	if err := SetConfigValue(rtx, "app", "db.host", "db.prod"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "app", "region", "ap", ConfigToProfile("stage")); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigValue(rtx, "app", "region", "af", ConfigToSharedKeys()); err != nil {
		t.Fatal(err)
	}
	if err := UnsetConfigValue(rtx, "app", "region", ConfigToProfile("stage")); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(cwRead(t, path)), &got); err != nil {
		t.Fatal(err)
	}
	want := `map[profiles:map[prod:map[db:map[host:db.prod] region:us] stage:map[]] region:af]`
	if fmt.Sprint(got) != want {
		t.Errorf("file = %v\nwant %s", got, want)
	}
	warns := rtx.copyWarnings()
	if len(warns) != 1 || !strings.Contains(warns[0].Error(), `but profile "prod" overrides it`) {
		t.Errorf("warnings = %v", warns)
	}

	// A schema violation in a profile is located in the rewritten file, in the profile.
	f.Schema = `{"type":"object","properties":{"region":{"enum":["eu","us","af"]}}}`
	err := SetConfigValue(wired(cwContext(dir, []string{"APP_PROFILE=prod"}, f), ConfigFiles(), Schemas(), jsonCodec()), "app", "region", "zz")
	want = "config key region: configuration file " + path + `:7:17 (profile prod) would be invalid: region: value is not in enum (got "zz")`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
}

func TestCodecEncode_secrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.ini")
	f := ConfigFileDef{Name: "app", Path: path, Keys: iniKeys()}
	rtx := iniWriteContext(dir, []CodecOption{CodecEncode(encodeINI)}, f)
	err := SetConfigValue(rtx, "app", "token", "s3cret")
	if ie, ok := asError[*InputError](err); !ok || ie.Kind != InputErrorKindWriteRefused {
		t.Fatalf("err = %v, want a write-refused error", err)
	}
	if err := SetConfigValue(rtx, "app", "token", "s3cret", ConfigAllowSecretWrite()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("a new file holding a secret: %v, %v; want mode 0600", info.Mode(), err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	err = SetConfigValue(rtx, "app", "token", "other", ConfigAllowSecretWrite())
	if err == nil || !strings.Contains(err.Error(), "is readable by others") {
		t.Errorf("err = %v", err)
	}
}
