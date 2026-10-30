package rotini

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The conformance rows for flag values merged across several configuration files.

type mrgInputs struct {
	Mrg struct {
		Flags struct {
			Labels map[string]string `rotini:"labels" recon:"labels"`
			Tags   []string          `rotini:"tags" recon:"tags"`
		}
		Arguments struct{}
	}
}

func mrgDef() Definition {
	return Definition{
		Name: "mrg", Handler: "Mrg", Inputs: reflect.TypeFor[mrgInputs](),
		Flags: []FlagDef{
			{Name: "labels", Identifiers: []string{"--label"}, Type: "map[string]string"},
			{Name: "tags", Identifiers: []string{"--tag"}, Type: "[]string"},
		},
	}
}

var mrgFiles = []ConfigFile{
	{Name: "user", Path: "user.yaml", Format: "yaml"},
	{Name: "sys", Path: "sys.toml", Format: "toml"},
	{Name: "base", Path: "base.json", Format: "json"},
}

// mrgContext runs mrgDef in a directory holding the three files of mrgFiles.
func mrgContext(t *testing.T, argv ...string) *Context {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"user.yaml": "labels: {a: ua}\ntags: [u]\n",
		"sys.toml":  "tags = [\"s1\", \"s2\"]\n[labels]\na = \"sa\"\nb = \"sb\"\n",
		"base.json": `{"labels": {"c": "bc"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return NewContextFor(mrgDef(), argv).WithDir(dir).WithInputSettings(InputSettings{ConfigFiles: mrgFiles})
}

type bndInputs struct {
	Bnd struct {
		Flags struct {
			ID   int64  `rotini:"id" recon:"id" env:"BND_ID"`
			Even int64  `rotini:"even"`
			Cap  uint64 `rotini:"cap"`
		}
		Arguments struct{}
	}
}

func bndDef() Definition {
	return Definition{
		Name: "bnd", Handler: "Bnd", Inputs: reflect.TypeFor[bndInputs](),
		Flags: []FlagDef{
			{Name: "id", Identifiers: []string{"--id"}, Type: "int64", Maximum: new(float64(1 << 53))},
			{Name: "even", Identifiers: []string{"--even"}, Type: "int64", Base: basePrefixed, MultipleOf: new(2.0)},
			{Name: "cap", Identifiers: []string{"--cap"}, Type: "uint64", ExclusiveMaximum: new(float64(1 << 63))},
		},
	}
}

func argvConformanceCases() []dataCase {
	return []dataCase{
		{"FLAG-30", func(t *testing.T) { // an integer is compared with its bounds exactly, past the 2^53 a float64 holds
			read := func(env []string, argv ...string) error {
				_, err := NewContextFor(bndDef(), argv).WithEnviron(env).Inputs[bndInputs]()
				return err
			}
			for _, c := range []struct {
				env  []string
				argv []string
				want string
			}{
				{nil, []string{"--id", "9007199254740992"}, ""},
				{nil, []string{"--id", "9007199254740993"}, "--id must be <= "},
				{[]string{"BND_ID=9007199254740993"}, nil, "--id must be <= "},
				{nil, []string{"--even", "9007199254740993"}, "--even must be a multiple of 2"},
				{nil, []string{"--even", "0x20000000000001"}, "--even must be a multiple of 2"},
				{nil, []string{"--even", "9007199254740994"}, ""},
				{nil, []string{"--cap", "9223372036854775807"}, ""},
				{nil, []string{"--cap", "9223372036854775808"}, "--cap must be < "},
			} {
				if err := read(c.env, c.argv...); (c.want == "") != (err == nil) || (err != nil && !strings.HasPrefix(err.Error(), c.want)) {
					t.Errorf("%v %q: err = %v, want %q", c.env, c.argv, err, c.want)
				}
			}
			var typed bndInputs
			typed.Bnd.Flags.ID = 1<<53 + 1
			if err := NewContextFor(bndDef(), nil).CheckInputs(typed, PresenceOf(typed)); err == nil || !strings.HasPrefix(err.Error(), "--id must be <= ") {
				t.Errorf("CheckInputs: err = %v", err)
			}
		}},
		{"PREC-07", func(t *testing.T) { // a flag's map merged from several files names each entry's file and key, and the entries an earlier file replaced
			in, report, err := mrgContext(t).InputsWithReport[mrgInputs]()
			f := in.Mrg.Flags
			if err != nil || !reflect.DeepEqual(f.Labels, map[string]string{"a": "ua", "b": "sb", "c": "bc"}) || !reflect.DeepEqual(f.Tags, []string{"u"}) {
				t.Fatalf("flags = %+v, %v", f, err)
			}
			for path, want := range map[FieldPath]string{
				"Mrg.Flags.Labels": "config:user#labels.a, config:sys#labels.b, config:base#labels.c",
				"Mrg.Flags.Tags":   "config:user#tags",
			} {
				if w, ok := report.Winner(path); !ok || w.Origin != want {
					t.Errorf("Winner(%s) = %+v, %v; want origin %q", path, w, ok, want)
				}
			}
			h := report.History("Mrg.Flags.Labels")
			if len(h) != 2 || h[0].Origin != "config:sys#labels.a" || h[0].Raw != "sa" {
				t.Errorf("History(Mrg.Flags.Labels) = %+v; want the replaced config:sys#labels.a first", h)
			}
			// The command line replaces the merged map whole.
			_, report, err = mrgContext(t, "--label", "z=1").InputsWithReport[mrgInputs]()
			if w, _ := report.Winner("Mrg.Flags.Labels"); err != nil || w.Origin != "argv:--label" {
				t.Errorf("with --label: Winner = %+v, %v", w, err)
			}
		}},
	}
}

func TestConformance_ArgvMatrix(t *testing.T) {
	wiringModes(t, func(t *testing.T) {
		for _, c := range argvConformanceCases() {
			t.Run(c.id, c.check)
		}
	})
}
