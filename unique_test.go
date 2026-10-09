package rotini

import (
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

// parseOne parses argv against a one-flag root whose Flags struct has a single field V of type
// ft for flag fd, and returns the error.
func parseOne(t *testing.T, fd FlagDef, ft reflect.Type, argv ...string) error {
	t.Helper()
	flags := reflect.StructOf([]reflect.StructField{{Name: "V", Type: ft, Tag: reflect.StructTag(`rotini:"` + fd.Name + `"`)}})
	cmd := reflect.StructOf([]reflect.StructField{
		{Name: "Flags", Type: flags},
		{Name: "Arguments", Type: reflect.TypeFor[struct{}]()},
	})
	out := reflect.New(reflect.StructOf([]reflect.StructField{{Name: "App", Type: cmd}}))
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{fd}}
	return NewParser().Parse(NewContextFor(def, argv), out.Interface())
}

func wantConstraint(t *testing.T, err error, msg string) {
	t.Helper()
	pe, ok := errors.AsType[*ParseError](err)
	if !ok || pe.Kind != ParseKindConstraintViolation || pe.Msg != msg {
		t.Errorf("err = %v, want the constraint violation %q", err, msg)
	}
}

func TestRepeat_singleValueFlagGivenTwice(t *testing.T) {
	fd := FlagDef{Name: "name", Identifiers: []string{"-n", "--name"}, Type: "string", NoRepeat: true}
	st := reflect.TypeFor[string]()

	wantConstraint(t, parseOne(t, fd, st, "--name", "a", "-n", "b", "--name=c"),
		`--name was given more than once ("a", then "c"); it takes one value`)
	if err := parseOne(t, fd, st, "--name", "a"); err != nil {
		t.Errorf("once: %v", err)
	}
	fd.NoRepeat = false
	if err := parseOne(t, fd, st, "--name", "a", "--name", "b"); err != nil {
		t.Errorf("last-wins by default: %v", err)
	}
}

func TestRepeat_secretValuesRedacted(t *testing.T) {
	fd := FlagDef{Name: "token", Identifiers: []string{"--token"}, Type: "string", NoRepeat: true, Secret: true}
	wantConstraint(t, parseOne(t, fd, reflect.TypeFor[string](), "--token", "s1", "--token", "s2"),
		`--token was given more than once ("[redacted]", then "[redacted]"); it takes one value`)
}

func TestRepeat_boolBundleAndNegatedForms(t *testing.T) {
	bt := reflect.TypeFor[bool]()
	v := FlagDef{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool", NoRepeat: true}
	if err := parseOne(t, v, bt, "-vv"); err == nil {
		t.Error("-vv on a non-repeatable bool passed")
	}
	c := FlagDef{Name: "color", Identifiers: []string{"--color"}, Type: "bool", Negatable: true, NoRepeat: true}
	wantConstraint(t, parseOne(t, c, bt, "--color", "--no-color"),
		`--no-color was given more than once ("true", then "false"); it takes one value`)
}

func TestRepeat_cascadingAndRedeclared(t *testing.T) {
	x := FlagDef{Name: "x", Identifiers: []string{"--x"}, Type: "string", NoRepeat: true}
	def := Definition{
		Name: "app", Handler: "App", Flags: []FlagDef{x},
		Commands: []CommandDef{
			{Name: "sub", Handler: "AppSub"},
			{Name: "own", Handler: "AppOwn", Flags: []FlagDef{x}},
		},
	}
	type sub struct {
		Flags     struct{}
		Arguments struct{}
	}
	var cascading struct {
		App struct {
			Flags struct {
				X string `rotini:"x"`
			}
			Arguments struct{}
		}
		AppSub sub
	}
	err := NewParser().Parse(NewContextFor(def, []string{"--x", "a", "sub", "--x", "b"}), &cascading)
	wantConstraint(t, err, `--x was given more than once ("a", then "b"); it takes one value`)

	var redeclared struct {
		App struct {
			Flags struct {
				X string `rotini:"x"`
			}
			Arguments struct{}
		}
		AppOwn struct {
			Flags struct {
				X string `rotini:"x"`
			}
			Arguments struct{}
		}
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"--x", "a", "own", "--x", "b"}), &redeclared); err != nil {
		t.Errorf("a redeclared flag is its own: %v", err)
	}
}

func TestRepeat_shortCircuitWaives(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "name", Identifiers: []string{"--name"}, Type: "string", NoRepeat: true},
		{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
	}}
	var in struct {
		App struct {
			Flags struct {
				Name string `rotini:"name"`
				Help bool   `rotini:"help"`
			}
			Arguments struct{}
		}
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"--name", "a", "--name", "b", "--help"}), &in); err != nil {
		t.Errorf("--help with a repeat: %v", err)
	}
}

func TestRepeat_envFallbackIsNotARepeat(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "name", Identifiers: []string{"--name"}, Type: "string", NoRepeat: true},
	}}
	var in struct {
		App struct {
			Flags struct {
				Name string `rotini:"name" recon:"name" env:"NAME"`
			}
			Arguments struct{}
		}
	}
	t.Setenv("NAME", "env")
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(def, []string{"--name", "a"}), &in); err != nil || in.App.Flags.Name != "a" {
		t.Errorf("Read = %v (name %q), want the command line's value", err, in.App.Flags.Name)
	}
}

// TestUniqueItems_comparesParsedValues pins that values compare as their type reads them.
func TestUniqueItems_comparesParsedValues(t *testing.T) {
	for _, tt := range []struct {
		typ    string
		ft     reflect.Type
		layout string
		dup    []string
		ok     []string
	}{
		{"[]int", reflect.TypeFor[[]int](), "", []string{"1", "01"}, []string{"1", "2"}},
		{"[]int64", reflect.TypeFor[[]int64](), "", []string{"9007199254740993", "9007199254740993"}, []string{"9007199254740992", "9007199254740993"}},
		{"[]float64", reflect.TypeFor[[]float64](), "", []string{"1.5", "1.50"}, []string{"1.5", "2"}},
		{"[]bool", reflect.TypeFor[[]bool](), "", []string{"true", "yes"}, []string{"true", "false"}},
		{"[]time.Duration", reflect.TypeFor[[]time.Duration](), "", []string{"1h", "60m"}, []string{"1h", "61m"}},
		{"[]rotini.ByteSize", reflect.TypeFor[[]ByteSize](), "", []string{"1Ki", "1024"}, []string{"1Ki", "1000"}},
		{"[]netip.Addr", reflect.TypeFor[[]netip.Addr](), "", []string{"::1", "0:0:0:0:0:0:0:1"}, []string{"::1", "::2"}},
		{"[]netip.Prefix", reflect.TypeFor[[]netip.Prefix](), "", []string{"10.0.0.0/8", "10.0.0.0/8"}, []string{"10.0.0.0/8", "10.0.0.0/16"}},
		{"[]net.HardwareAddr", nil, "", []string{"01:23:45:67:89:AB", "01-23-45-67-89-ab"}, nil},
		{"[]time.Time", reflect.TypeFor[[]time.Time](), "", []string{"2026-01-01T00:00:00Z", "2026-01-01T01:00:00+01:00"}, []string{"2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z"}},
		{"[]time.Time", reflect.TypeFor[[]time.Time](), "unix", []string{"1759104000", "1759104000.0"}, []string{"1", "2"}},
		{"[]string", reflect.TypeFor[[]string](), "", []string{"a", "a"}, []string{"a", "A"}},
	} {
		fd := FlagDef{Name: "v", Identifiers: []string{"--v"}, Type: tt.typ, Layout: tt.layout, UniqueItems: true}
		key := uniqueKeyFor(tt.typ, timeSpec{layout: tt.layout}, enumSet{})
		if key(tt.dup[0]) != key(tt.dup[1]) {
			t.Errorf("%s %s: %v are different keys (%q, %q)", tt.typ, tt.layout, tt.dup, key(tt.dup[0]), key(tt.dup[1]))
		}
		if tt.ft == nil {
			continue
		}
		var argv []string
		for _, v := range tt.dup {
			argv = append(argv, "--v="+v)
		}
		wantConstraint(t, parseOne(t, fd, tt.ft, argv...), `--v must not repeat a value (got "`+tt.dup[1]+`" twice)`)
		argv = argv[:0]
		for _, v := range tt.ok {
			argv = append(argv, "--v="+v)
		}
		if err := parseOne(t, fd, tt.ft, argv...); err != nil {
			t.Errorf("%s %v: %v", tt.typ, tt.ok, err)
		}
	}
}

func TestUniqueItems_ignoreCaseEnumAndSecret(t *testing.T) {
	fd := FlagDef{Name: "mode", Identifiers: []string{"--mode"}, Type: "[]string", Enum: []string{"fast", "slow"}, IgnoreCase: true,
		UniqueItems: true}
	wantConstraint(t, parseOne(t, fd, reflect.TypeFor[[]string](), "--mode", "fast", "--mode", "FAST"),
		`--mode must not repeat a value (got "FAST" twice)`)

	s := FlagDef{Name: "key", Identifiers: []string{"--key"}, Type: "[]string", Secret: true, UniqueItems: true}
	wantConstraint(t, parseOne(t, s, reflect.TypeFor[[]string](), "--key", "k", "--key", "k"),
		`--key must not repeat a value (got "[redacted]" twice)`)
}

func TestUniqueItems_variadicWithSeparator(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{
		{Name: "ids", Type: "[]int", Variadic: true, Separator: ",", UniqueItems: true},
	}}
	var in struct {
		App struct {
			Flags     struct{}
			Arguments struct {
				IDs []int `rotini:"ids"`
			}
		}
	}
	wantConstraint(t, NewParser().Parse(NewContextFor(def, []string{"1,2", "02"}), &in), `<ids> must not repeat a value (got "02" twice)`)
	if err := NewParser().Parse(NewContextFor(def, []string{"1,2", "3"}), &in); err != nil {
		t.Errorf("distinct: %v", err)
	}
}

type uniqueObj struct {
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
}

func TestUniqueItems_listOfObjects(t *testing.T) {
	fd := FlagDef{Name: "db", Identifiers: []string{"--db"}, Type: "[]DB", UniqueItems: true,
		ObjectSchema: `{"type":"object","properties":{"host":{"type":"string"},"port":{"type":"integer"}}}`}
	ft := reflect.TypeFor[[]uniqueObj]()
	err := parseOne(t, fd, ft, "--db", `{"port":5,"host":"a"}`, "--db", "host=a,port=5")
	wantConstraint(t, err, `--db must not repeat a value (got {"host":"a","port":5} twice)`)
	if err := parseOne(t, fd, ft, "--db", "host=a", "--db", "host=b"); err != nil {
		t.Errorf("distinct objects: %v", err)
	}
}

func TestUniqueItems_envAndConfigLists(t *testing.T) {
	var in struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Ports []int `rotini:"ports" recon:"ports" env:"PORTS" unique:"true"`
			}
		}
	}
	t.Setenv("PORTS", "80,81,80")
	err := NewInputReader(InputSettings{}).Read(NewContextFor(tbAppDef(), nil), &in)
	if err == nil || !strings.Contains(err.Error(), `PORTS must not repeat a value (got "80" twice)`) {
		t.Errorf("env: %v", err)
	}

	cfg := writeConfig(t, "app:\n  hosts: [a, b, a]\n")
	var conf struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Config    struct {
				Hosts []string `rotini:"hosts" recon:"app.hosts" unique:"true"`
			}
		}
	}
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}
	err = NewInputReader(meta).Read(NewContextFor(tbAppDef(), nil), &conf)
	if err == nil || !strings.Contains(err.Error(), `must not repeat a value (got "a" twice)`) {
		t.Errorf("config: %v", err)
	}
}

// TestChannelLists_perElementBounds pins that a list env input's elements meet its numeric
// bounds: the list used to be checked as strings, skipping them.
func TestChannelLists_perElementBounds(t *testing.T) {
	var in struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Ports []int `rotini:"ports" recon:"ports" env:"PORTS" min:"1" max:"10"`
			}
		}
	}
	t.Setenv("PORTS", "5,70000")
	err := NewInputReader(InputSettings{}).Read(NewContextFor(tbAppDef(), nil), &in)
	if err == nil || !strings.Contains(err.Error(), "must be <= 10 (got 70000)") {
		t.Errorf("Read = %v, want the max bound on an element", err)
	}
}

func TestUniqueItems_checkInputs(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "at", Identifiers: []string{"--at"}, Type: "[]time.Time", UniqueItems: true},
	}}
	type inputs struct {
		App struct {
			Flags struct {
				At []time.Time `rotini:"at"`
			}
			Arguments struct{}
		}
	}
	var in inputs
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	in.App.Flags.At = []time.Time{t0, t0.In(time.FixedZone("x", 3600))}
	err := NewContextFor(def, nil).CheckInputs(in, PresenceOf(in))
	if err == nil || !strings.Contains(err.Error(), "--at must not repeat a value") {
		t.Errorf("CheckInputs = %v, want a duplicate instant", err)
	}
	in.App.Flags.At = []time.Time{t0, t0.Add(time.Second)}
	if err := NewContextFor(def, nil).CheckInputs(in, PresenceOf(in)); err != nil {
		t.Errorf("distinct: %v", err)
	}
}
