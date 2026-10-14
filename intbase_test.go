package rotini

import (
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var prefixedSeeds = []string{
	"0x1F", "0X1f", "0o17", "0O17", "0b101", "0B101", "-0x10", "+0b1", "-0", "010", "007", "0", "1_000",
	"0xFF_FF", "0x_1F", "0b1_0", "1__0", "_1", "1_", "-_1", "+_0x1", "0x", "0x_", "0x1F_", "0x__1", "0b2",
	"0o8", "0xG", "", "+", "-", " 1", "1 ", "1e3", "1.5", "0x1p3", "abc", "١٢", "18446744073709551615",
	"18446744073709551616", "0xFFFFFFFFFFFFFFFF", "0x10000000000000000", "-9223372036854775808",
}

func TestPrefixedDecimal(t *testing.T) {
	for in, want := range map[string]string{
		"0x1F": "31", "0X1f": "31", "0o17": "15", "0O17": "15", "0b101": "5", "0B101": "5",
		"-0x10": "-16", "+0b1": "1", "-0": "0", "010": "10", "007": "7", "1_000": "1000",
		"0xFF_FF": "65535", "0x_1F": "31", "18446744073709551615": "18446744073709551615",
	} {
		if got, ok := prefixedDecimal(in); !ok || got != want {
			t.Errorf("prefixedDecimal(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"1__0", "_1", "1_", "-_1", "+_0x1", "0x", "0x_", "0x1F_", "0x__1", "0b2", "0o8", "", "-", " 1", "1e3", "0x10000000000000000"} {
		if got, ok := prefixedDecimal(in); ok {
			t.Errorf("prefixedDecimal(%q) = %q, want rejected", in, got)
		}
	}
}

// contractPattern is the pattern the contract and JSON Schemas state for a base: prefixed
// input's text; the runtime must accept exactly what it matches, within 64 bits.
var contractPattern = regexp.MustCompile(`^[+-]?(0[xX](_?[0-9a-fA-F])+|0[oO](_?[0-7])+|0[bB](_?[01])+|[0-9](_?[0-9])*)$`)

func checkPrefixedPattern(t *testing.T, s string) {
	t.Helper()
	_, ok := prefixedDecimal(s)
	match := contractPattern.MatchString(s)
	if ok && !match {
		t.Errorf("prefixedDecimal accepts %q, which the contract pattern rejects", s)
	}
	if !ok && match {
		digits := strings.ReplaceAll(strings.TrimLeft(s, "+-"), "_", "")
		base := 10
		if len(digits) > 2 && strings.ContainsRune("xXoObB", rune(digits[1])) {
			base = 0
		}
		n, _ := new(big.Int).SetString(digits, base)
		if n == nil || n.BitLen() <= 64 {
			t.Errorf("prefixedDecimal rejects %q, which the contract pattern matches and fits 64 bits", s)
		}
	}
}

func TestPrefixedDecimal_matchesContractPattern(t *testing.T) {
	for _, s := range prefixedSeeds {
		checkPrefixedPattern(t, s)
	}
}

func FuzzPrefixedDecimal(f *testing.F) {
	for _, s := range prefixedSeeds {
		f.Add(s)
	}
	f.Fuzz(checkPrefixedPattern)
}

type ibInputs struct {
	App struct {
		Flags struct {
			N    int     `rotini:"n"`
			U8   uint8   `rotini:"u8"`
			Mask []int   `rotini:"mask"`
			Mode uint32  `rotini:"mode"`
			Lvl  int     `rotini:"level"`
			Ptr  *int64  `rotini:"ptr"`
			Raw  int     `rotini:"raw"`
			File int     `rotini:"file"`
			Ids  []uint8 `rotini:"ids"`
		}
		Arguments struct {
			Nums []int `rotini:"nums"`
		}
	}
}

func ibDef() Definition {
	return Definition{
		Name: "app", Handler: "App", Inputs: reflect.TypeFor[ibInputs](),
		Flags: []FlagDef{
			{Name: "n", Identifiers: []string{"--n"}, Type: "int", Base: basePrefixed, Maximum: new(4096.0), MultipleOf: new(2.0)},
			{Name: "u8", Identifiers: []string{"--u8"}, Type: "uint8", Base: basePrefixed},
			{Name: "mask", Identifiers: []string{"--mask"}, Type: "[]int", Base: basePrefixed, Separator: ",", UniqueItems: true},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "uint32", Base: basePrefixed, Default: "0o644"},
			{Name: "level", Identifiers: []string{"--level"}, Type: "int", Base: basePrefixed, Enum: []string{"1", "0x10"}},
			{Name: "ptr", Identifiers: []string{"--ptr"}, Type: "int64", Base: basePrefixed, ExclusiveMinimum: new(0.0)},
			{Name: "raw", Identifiers: []string{"--raw"}, Type: "int"},
			{Name: "file", Identifiers: []string{"--file"}, Type: "int", Base: basePrefixed, From: []string{"file"}},
			{Name: "ids", Identifiers: []string{"--ids"}, Type: "[]uint8", Base: basePrefixed, Separator: ","},
		},
		Arguments: []ArgDef{{Name: "nums", Type: "[]int", Variadic: true, Base: basePrefixed, Minimum: new(-100.0)}},
	}
}

func ibParse(t *testing.T, argv ...string) (ibInputs, error) {
	t.Helper()
	var in ibInputs
	err := NewInputReader(InputSettings{}).Read(NewContextFor(ibDef(), argv), &in)
	return in, err
}

// A base: prefixed integer reads 0x, 0o and 0b literals with a sign and underscores, and a
// value without a prefix as decimal, on every flag and argument shape.
func TestPrefixedBase_parses(t *testing.T) {
	in, err := ibParse(t, "--n", "0x1F_E", "--u8=0b1111_1111", "--mask", "0x1,010", "--mask", "0o7",
		"--level", "16", "--ptr", "+0x2", "--ids", "0xA,0b11", "--", "-0x10", "1_000", "010")
	if err != nil {
		t.Fatal(err)
	}
	f := in.App.Flags
	if f.N != 0x1FE || f.U8 != 255 || !slices.Equal(f.Mask, []int{1, 10, 7}) || f.Mode != 0o644 || f.Lvl != 16 ||
		f.Ptr == nil || *f.Ptr != 2 || !slices.Equal(f.Ids, []uint8{10, 3}) {
		t.Errorf("flags %+v", f)
	}
	if got := in.App.Arguments.Nums; !slices.Equal(got, []int{-16, 1000, 10}) {
		t.Errorf("nums %v", got)
	}
}

// Malformed literals and out-of-range values are errors that name the value as written; an
// input without a base takes decimal only, exactly as before.
func TestPrefixedBase_rejects(t *testing.T) {
	for _, tt := range []struct {
		argv []string
		want string
	}{
		{[]string{"--n", "1__0"}, `--n: "1__0" is not a valid integer`},
		{[]string{"--n", "_1"}, `--n: "_1" is not a valid integer`},
		{[]string{"--n", "1_"}, `--n: "1_" is not a valid integer`},
		{[]string{"--n=-_1"}, `--n: "-_1" is not a valid integer`},
		{[]string{"--n", "0x"}, `--n: "0x" is not a valid integer`},
		{[]string{"--n", "0b2"}, `--n: "0b2" is not a valid integer`},
		{[]string{"--u8", "0x100"}, `--u8: "0x100" is not a valid uint8 (out of range: 0 to 255)`},
		{[]string{"--u8=-0x1"}, `--u8: "-0x1" is not a valid uint8 (out of range: 0 to 255)`},
		{[]string{"--n", "0x10000000000000000"}, `--n: "0x10000000000000000" is not a valid integer`},
		{[]string{"--ids", "0x1,0x1FF"}, `--ids: "0x1FF" is not a valid uint8 (out of range: 0 to 255)`},
		{[]string{"--", "0xZ"}, `<nums>: "0xZ" is not a valid integer`},
		{[]string{"--raw", "0x10"}, `--raw: "0x10" is not a valid integer`},
		{[]string{"--raw", "1_000"}, `--raw: "1_000" is not a valid integer`},
	} {
		if _, err := ibParse(t, tt.argv...); err == nil || err.Error() != tt.want {
			t.Errorf("%q: err = %v, want %s", tt.argv, err, tt.want)
		}
	}
	// 010 without a base is still ten, as it always was.
	if in, err := ibParse(t, "--raw", "010"); err != nil || in.App.Flags.Raw != 10 {
		t.Errorf("--raw 010 = %d, %v", in.App.Flags.Raw, err)
	}
}

// Bounds, enum and uniqueness compare the number a value reads as, and errors show the value
// as written.
func TestPrefixedBase_rules(t *testing.T) {
	for _, tt := range []struct {
		argv []string
		want string
	}{
		{[]string{"--n", "0x1002"}, "--n must be <= 4096 (got 0x1002)"},
		{[]string{"--n", "0b11"}, "--n must be a multiple of 2 (got 0b11)"},
		{[]string{"--ptr=-0x1"}, "--ptr must be > 0 (got -0x1)"},
		{[]string{"--", "-0x65"}, "<nums> must be >= -100 (got -0x65)"},
		{[]string{"--mask", "0x10", "--mask", "16"}, `--mask must not repeat a value (got "16" twice)`},
		{[]string{"--level", "0x2"}, `invalid value "0x2" for --level (one of: 1, 0x10)`},
	} {
		if _, err := ibParse(t, tt.argv...); err == nil || err.Error() != tt.want {
			t.Errorf("%q: err = %v, want %s", tt.argv, err, tt.want)
		}
	}
	if in, err := ibParse(t, "--level", "0x1", "--n", "0x1000"); err != nil || in.App.Flags.Lvl != 1 || in.App.Flags.N != 4096 {
		t.Errorf("enum and bound by value: %+v, %v", in.App.Flags, err)
	}
}

// A short circuit still checks that each value reads, under the base.
func TestPrefixedBase_shortCircuit(t *testing.T) {
	type inputs struct {
		App struct {
			Flags struct {
				Help bool   `rotini:"help"`
				Mode uint32 `rotini:"mode"`
			}
			Arguments struct {
				N int `rotini:"n"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Inputs: reflect.TypeFor[inputs](),
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "uint32", Base: basePrefixed, Default: "0o644"},
		},
		Arguments: []ArgDef{{Name: "n", Type: "int", Base: basePrefixed}},
	}
	var in inputs
	if err := NewParser().Parse(NewContextFor(def, []string{"--help", "0x10"}), &in); err != nil || in.App.Flags.Mode != 0o644 || in.App.Arguments.N != 16 {
		t.Errorf("--help 0x10: %+v, %v", in.App, err)
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"--help", "--mode", "0x"}), &in); err == nil || err.Error() != `--mode: "0x" is not a valid non-negative integer` {
		t.Errorf("--help --mode 0x: %v", err)
	}
}

// A value read from a file (@path) and a flag's environment and configuration fallbacks read
// under the base too.
func TestPrefixedBase_acquiredAndFallbacks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "n")
	if err := os.WriteFile(path, []byte("0x2A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if in, err := ibParse(t, "--file", "@"+path); err != nil || in.App.Flags.File != 42 {
		t.Errorf("--file @%s = %d, %v", path, in.App.Flags.File, err)
	}

	type inputs struct {
		App struct {
			Flags struct {
				N int `rotini:"n" recon:"n"`
				M int `rotini:"m" recon:"m"`
			}
			Arguments struct{}
		}
	}
	def := Definition{Name: "app", Handler: "App", Inputs: reflect.TypeFor[inputs](), Flags: []FlagDef{
		{Name: "n", Identifiers: []string{"--n"}, Type: "int", Base: basePrefixed, Maximum: new(100.0)},
		{Name: "m", Identifiers: []string{"--m"}, Type: "int", Base: basePrefixed},
	}}
	cfg := writeConfig(t, "m: '0b101'\n")
	read := func(env string) (inputs, error) {
		var in inputs
		rtx := NewContextFor(def, nil).WithEnviron([]string{env})
		return in, NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}).Read(rtx, &in)
	}
	if in, err := read("N=0o17"); err != nil || in.App.Flags.N != 15 || in.App.Flags.M != 5 {
		t.Errorf("fallbacks: %+v, %v", in.App.Flags, err)
	}
	if _, err := read("N=0x65"); err == nil || !strings.Contains(err.Error(), "--n must be <= 100 (got 0x65)") {
		t.Errorf("fallback bound: %v", err)
	}
}

type ibChannelInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Env       struct {
			Port  int      `rotini:"port" recon:"port" env:"PORT" max:"1024" base:"prefixed"`
			Masks []uint16 `rotini:"masks" recon:"masks,separator=:" env:"MASKS" base:"prefixed"`
			Plain int      `rotini:"plain" recon:"plain" env:"PLAIN"`
		}
		Config struct {
			Mode  uint32 `rotini:"mode" recon:"mode" base:"prefixed"`
			Bits  []int  `rotini:"bits" recon:"bits" base:"prefixed" unique:"true"`
			Count int    `rotini:"count" recon:"count" base:"prefixed"`
		}
	}
}

// Env inputs and configuration values given as strings read under the base; a number a codec
// decoded is unaffected.
func TestPrefixedBase_envAndConfigInputs(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Inputs: reflect.TypeFor[ibChannelInputs]()}
	read := func(cfgBody string, env ...string) (ibChannelInputs, error) {
		var in ibChannelInputs
		cfg := writeConfig(t, cfgBody)
		rtx := NewContextFor(def, nil).WithEnviron(env)
		return in, NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}).Read(rtx, &in)
	}
	in, err := read("mode: '0o755'\nbits: ['0b1', '010', 3]\ncount: 12\n", "PORT=0x1F", "MASKS=0xF:010:0b1", "PLAIN=7")
	if err != nil {
		t.Fatal(err)
	}
	e, c := in.App.Env, in.App.Config
	if e.Port != 31 || !slices.Equal(e.Masks, []uint16{15, 10, 1}) || e.Plain != 7 || c.Mode != 0o755 || !slices.Equal(c.Bits, []int{1, 10, 3}) || c.Count != 12 {
		t.Errorf("env %+v, config %+v", e, c)
	}
	if _, err := read("mode: 1\n", "PORT=0x401"); err == nil || !strings.Contains(err.Error(), "PORT must be <= 1024") {
		t.Errorf("env bound: %v", err)
	}
	if _, err := read("bits: ['0x10', '16']\n"); err == nil || !strings.Contains(err.Error(), "must not repeat a value") {
		t.Errorf("config uniqueness: %v", err)
	}
	if _, err := read("mode: '1__0'\n"); err == nil {
		t.Error("a malformed config literal was accepted")
	}
}

// Writing a configuration value reads it under the key's base and writes the number.
func TestPrefixedBase_configWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.yaml")
	key := ConfigKey{Key: "mode", Type: "uint32", Base: basePrefixed}
	rtx := cwContext(dir, nil, ConfigFile{Name: "app", Path: path, Keys: []ConfigKey{key}})
	if err := SetConfigValue(rtx, "app", "mode", "0o755"); err != nil {
		t.Fatal(err)
	}
	if got := cwRead(t, path); got != "mode: 493\n" {
		t.Errorf("file = %q", got)
	}
}

func TestPlainDecimalText(t *testing.T) {
	for in, want := range map[string]string{
		"10": "10", "010": "10", "-007": "-7", "+0042": "+42", "000": "0", "-0": "-0",
		"0x10": "0x10", "0o7": "0o7", "1_000": "1_000", "": "", "-": "-", "abc": "abc", " 10": " 10",
	} {
		if got := plainDecimalText(in); got != want {
			t.Errorf("plainDecimalText(%q) = %q, want %q", in, got, want)
		}
	}
}

type ibDecimalInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Env       struct {
			Plain int   `rotini:"plain" recon:"plain" env:"PLAIN" max:"100"`
			List  []int `rotini:"list" recon:"list" env:"LIST"`
		}
		Config struct {
			Count int `rotini:"count" recon:"count"`
		}
	}
}

// Without base: prefixed an env or config integer is decimal, as on the command line: a leading
// zero is decimal, a prefixed literal is an invalid value, and the bounds compare the number.
func TestPlainBase_envAndConfigAreDecimal(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Inputs: reflect.TypeFor[ibDecimalInputs]()}
	read := func(cfgBody string, env ...string) (ibDecimalInputs, error) {
		var in ibDecimalInputs
		cfg := writeConfig(t, cfgBody)
		rtx := NewContextFor(def, nil).WithEnviron(env)
		return in, NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}).Read(rtx, &in)
	}
	in, err := read("count: '010'\n", "PLAIN=010", "LIST=010,-007")
	if err != nil {
		t.Fatal(err)
	}
	if e, c := in.App.Env, in.App.Config; e.Plain != 10 || !slices.Equal(e.List, []int{10, -7}) || c.Count != 10 {
		t.Errorf("env %+v, config %+v", e, c)
	}
	for _, tc := range []struct{ cfg, env, want string }{
		{"count: 1\n", "PLAIN=0x10", `PLAIN: "0x10" is not a valid integer`},
		{"count: 1\n", "PLAIN=0x1000", `PLAIN: "0x1000" is not a valid integer`},
		{"count: 1\n", "LIST=1,0b1", `LIST: "0b1" is not a valid integer`},
		{"count: '0o7'\n", "PLAIN=1", `"0o7" is not a valid integer`},
		{"count: 1\n", "PLAIN=101", "PLAIN must be <= 100 (got 101)"},
	} {
		if _, err := read(tc.cfg, tc.env); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s / %s: err = %v, want %q", strings.TrimSpace(tc.cfg), tc.env, err, tc.want)
		}
	}
}
