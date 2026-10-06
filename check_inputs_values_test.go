package rotini

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// valInputs covers the value kinds CheckInputs reads: pointers, unsigned and float numbers,
// durations, strings with length and pattern rules, object flags, and a stdin document.
type valInputs struct {
	App struct {
		Flags struct {
			Ratio   *float64      `rotini:"ratio"`
			Workers uint          `rotini:"workers"`
			Wait    time.Duration `rotini:"wait"`
			Code    string        `rotini:"code"`
			DB      objDB         `rotini:"db"`
			Mounts  []objMount    `rotini:"mount"`
		}
		Arguments struct{}
		Stdin     *AppStdin
	}
}

type AppStdin struct {
	Name string `json:"name"`
}

const appStdinSchema = `{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":2}}}`

func valDef() Definition {
	one, ten, second := 1.0, 10.0, float64(time.Second)
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "ratio", Identifiers: []string{"--ratio"}, Type: "float64", Constraints: Constraints{Maximum: &one}},
			{Name: "workers", Identifiers: []string{"--workers"}, Type: "uint", Constraints: Constraints{Maximum: &ten}},
			{Name: "wait", Identifiers: []string{"--wait"}, Type: "time.Duration", Constraints: Constraints{Minimum: &second}},
			{Name: "code", Identifiers: []string{"--code"}, Type: "string", Constraints: Constraints{MinLength: 3, Pattern: "^[a-z]+$"}},
			{Name: "db", Identifiers: []string{"--db"}, Type: "DB", ObjectSchema: objDBSchema},
			{Name: "mount", Identifiers: []string{"--mount"}, Type: "[]Mount", ObjectSchema: objMountSchema},
		},
	}
}

func valContext() *Context {
	return NewContextFor(valDef(), nil).WithInputSettings(InputSettings{StdinSchemas: map[string]string{"AppStdin": appStdinSchema}})
}

func TestCheckInputs_valueKinds(t *testing.T) {
	half, two := 0.5, 2.0
	tests := []struct {
		name string
		set  func(*valInputs)
		want string // "" means no error
	}{
		{"pointer within bounds", func(v *valInputs) { v.App.Flags.Ratio = &half }, ""},
		{"pointer over its maximum", func(v *valInputs) { v.App.Flags.Ratio = &two }, "--ratio must be <= 1 (got 2)"},
		{"unsigned over its maximum", func(v *valInputs) { v.App.Flags.Workers = 11 }, "--workers must be <= 10 (got 11)"},
		{"duration under its minimum", func(v *valInputs) { v.App.Flags.Wait = 500 * time.Millisecond }, "--wait"},
		{"duration within bounds", func(v *valInputs) { v.App.Flags.Wait = 2 * time.Second }, ""},
		{"string too short", func(v *valInputs) { v.App.Flags.Code = "ab" }, "--code"},
		{"string off its pattern", func(v *valInputs) { v.App.Flags.Code = "ABC" }, "--code"},
		{"string within its rules", func(v *valInputs) { v.App.Flags.Code = "abc" }, ""},
		{"object flag valid", func(v *valInputs) { v.App.Flags.DB = objDB{Host: "db", Port: 5432} }, ""},
		{"object flag breaks its schema", func(v *valInputs) { v.App.Flags.DB = objDB{Host: "db", Port: -1} }, "--db"},
		// A typed struct always carries every field, so a required property is never absent here;
		// the command line is what reports a missing one.
		{"list of objects", func(v *valInputs) { v.App.Flags.Mounts = []objMount{{Src: "a"}, {Src: "b", Dst: "c"}} }, ""},
		{"stdin document valid", func(v *valInputs) { v.App.Stdin = &AppStdin{Name: "abc"} }, ""},
		{"stdin document breaks its schema", func(v *valInputs) { v.App.Stdin = &AppStdin{Name: "a"} }, "stdin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v valInputs
			tt.set(&v)
			err := valContext().CheckInputs(v, PresenceOf(v))
			if tt.want == "" {
				if err != nil {
					t.Fatalf("CheckInputs = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("CheckInputs = %v, want an error mentioning %q", err, tt.want)
			}
			if CategoryOf(err) != CategoryUsage {
				t.Errorf("CategoryOf = %v, want usage", CategoryOf(err))
			}
		})
	}
}

// TestCheckInputs_stdinErrorIsAnInputError pins the type a stdin failure arrives as.
func TestCheckInputs_stdinErrorIsAnInputError(t *testing.T) {
	var v valInputs
	v.App.Stdin = &AppStdin{}
	err := valContext().CheckInputs(v, PresenceOf(v))
	var ie *InputError
	if !errors.As(err, &ie) || ie.Channel != channelStdin {
		t.Fatalf("err = %#v, want a stdin *InputError", err)
	}
}

// TestTypedText pins how typed values render back to the text a user would type.
func TestTypedText(t *testing.T) {
	var v valInputs
	half := 0.5
	v.App.Flags.Ratio = &half
	v.App.Flags.Workers = 7
	v.App.Flags.Wait = 90 * time.Second
	elems, _, _ := typedElems(rvField(v, "Ratio"))
	if got := typedText(elems[0]); got != "0.5" {
		t.Errorf("ratio = %q", got)
	}
	elems, _, _ = typedElems(rvField(v, "Workers"))
	if got := typedText(elems[0]); got != "7" {
		t.Errorf("workers = %q", got)
	}
	elems, _, _ = typedElems(rvField(v, "Wait"))
	if got := typedText(elems[0]); got != "1m30s" {
		t.Errorf("wait = %q", got)
	}
	var nilRatio valInputs
	if _, _, ok := typedElems(rvField(nilRatio, "Ratio")); ok {
		t.Error("a nil pointer supplied a value")
	}
}

// rvField returns the named App.Flags field of v.
func rvField(v valInputs, name string) reflect.Value {
	return reflect.ValueOf(v).Field(0).FieldByName("Flags").FieldByName(name)
}

// TestInputReport_checksAHandBuiltStdinDocument pins that Validate checks a hand-built stdin
// document against the command's stdin schema, as CheckInputs does.
func TestInputReport_checksAHandBuiltStdinDocument(t *testing.T) {
	rtx := valContext()
	argv, err := rtx.ArgvInputs[valInputs]()
	if err != nil {
		t.Fatal(err)
	}
	var hand valInputs
	hand.App.Stdin = &AppStdin{Name: "a"}
	_, report := MergeInputsWithReport(argv, InputLayer[valInputs]{Name: "fix", Values: hand, Set: PresenceOf(hand)})
	var ie *InputError
	if err := report.Validate(); !errors.As(err, &ie) || ie.Channel != channelStdin {
		t.Fatalf("Validate = %v, want the stdin schema violation", err)
	}
	hand.App.Stdin = &AppStdin{Name: "abc"}
	_, report = MergeInputsWithReport(argv, InputLayer[valInputs]{Name: "fix", Values: hand, Set: PresenceOf(hand)})
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}
