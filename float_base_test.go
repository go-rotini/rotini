package rotini

import (
	"math"
	"strings"
	"testing"
)

// TestFloat_baseOnEveryChannel pins that a float input refuses a hexadecimal float and `_`
// between digits unless it declares base: prefixed, on the command line and in the environment,
// and that inf and nan are accepted either way.
func TestFloat_baseOnEveryChannel(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "rate", Identifiers: []string{"--rate"}, Type: "float64", Maximum: new(100.0)},
			{Name: "hex", Identifiers: []string{"--hex"}, Type: "float64", Base: basePrefixed},
			{Name: "rates", Identifiers: []string{"--rates"}, Type: "[]float32", Base: basePrefixed},
		},
	}
	type in struct {
		App struct {
			Flags struct {
				Rate  float64   `rotini:"rate"`
				Hex   float64   `rotini:"hex"`
				Rates []float32 `rotini:"rates"`
			}
			Arguments struct{}
			Env       struct {
				Ratio  float64 `rotini:"ratio" recon:"ratio" env:"APP_RATIO"`
				Scaled float64 `rotini:"scaled" recon:"scaled" env:"APP_SCALED" base:"prefixed"`
			}
		}
	}
	read := func(env []string, argv ...string) (in, error) {
		var got in
		err := NewInputReader(InputSettings{}).Read(NewContextFor(def, argv).WithEnviron(env), &got)
		return got, err
	}
	for _, v := range []string{"0x1p4", "1_000", "0x10", "-0X1P-2"} {
		if _, err := read(nil, "--rate", v); err == nil || !strings.Contains(err.Error(), `"`+v+`" is not a valid`) {
			t.Errorf("--rate %s: err = %v, want refused", v, err)
		}
		if _, err := read([]string{"APP_RATIO=" + v}); err == nil || !strings.Contains(err.Error(), "APP_RATIO") {
			t.Errorf("APP_RATIO=%s: err = %v, want refused", v, err)
		}
	}
	got, err := read([]string{"APP_SCALED=0x1p4", "APP_RATIO=-Inf"}, "--hex", "1_000.5", "--rates", "0x10", "--rates", "0x1.8p1", "--rates", "NaN")
	if err != nil {
		t.Fatal(err)
	}
	f := got.App.Flags
	if f.Hex != 1000.5 || len(f.Rates) != 3 || f.Rates[0] != 16 || f.Rates[1] != 3 || !math.IsNaN(float64(f.Rates[2])) {
		t.Errorf("flags = %+v", f)
	}
	if e := got.App.Env; e.Scaled != 16 || !math.IsInf(e.Ratio, -1) {
		t.Errorf("env = %+v", e)
	}
	if _, err := read(nil, "--hex", "0x1p8"); err != nil {
		t.Errorf("--hex 0x1p8: %v", err)
	}
	if _, err := read(nil, "--rate", "0x1p8"); err == nil || strings.Contains(err.Error(), "<=") {
		t.Errorf("--rate 0x1p8: err = %v, want a type error, not a bound", err)
	}
}
