package rotini

import (
	"reflect"
	"slices"
	"sync"
	"testing"
)

// wiringFixture is what featureNeeds reads for a fixture Definition besides its tree: the
// settings it runs with and the inputs types its commands read.
type wiringFixture struct {
	meta   InputSettings
	inputs []reflect.Type
}

// wiringFixtures maps each Definition name the conformance suites run to its fixture. Every
// definition a wired run builds must have an entry, so a new fixture can't run wired with
// fewer features than its commands need.
// The settings list the shapes of the files each fixture's cases read (format, profiles,
// schema, a path chosen at run time), which is all featureNeeds reads of them. A fixture whose
// inputs type is local to its test records it in [Definition.Inputs] instead.
var wiringFixtures = map[string]wiringFixture{
	"acme": {
		meta: InputSettings{ConfigFiles: []ConfigFile{
			{Name: "main", Format: "yaml", PathFrom: &PathFromDef{}, Schema: "{}"},
			{Name: "local", Format: "jsonc"},
			{Name: "dotenv", Format: "dotenv"},
		}},
		inputs: typesOf(acApplyInputs{}, acCreateInputs{}, acDeleteInputs{}, acDeployInputs{}, acGetInputs{},
			acImportInputs{}, acIngestInputs{}, acLoginInputs{}, acRunInputs{}, acWrapInputs{}),
	},
	"app": {
		meta: InputSettings{
			ConfigFiles: []ConfigFile{
				{Name: "app", Path: "app.yaml", Profiles: &ProfilesDef{}, Keys: wcKeys()},
				{Name: "dotenv", As: "env", Path: ".env"},
			},
			StdinSchemas: map[string]string{"Stdin": "{}"},
		},
		inputs: typesOf(argFileInputs{}, bdCpInputs{}, bdExecInputs{}, bdSSHInputs{}, depInputs{}, exInputs{},
			idEnvInputs{}, idListInputs{}, idSecretInputs{}, outFieldsInputs{}, pfInputs{}, ssBytesInputs{},
			ssCatInputs{}, ssJSONLInputs{}, ssLinesInputs{}, ssNulSliceInputs{}),
	},
	"calc":  {meta: InputSettings{ConfigFiles: []ConfigFile{{Name: "main", Format: "yaml"}}}, inputs: typesOf(acCalcInputs{})},
	"cat":   {},
	"cp":    {inputs: typesOf(cpListInputs{})},
	"files": {inputs: typesOf(fileInputs{})},
	"mrg":   {meta: InputSettings{ConfigFiles: mrgFiles}},
	"bnd":   {},
	"proj":  {meta: InputSettings{ConfigFiles: []ConfigFile{{Name: "proj", Format: "yaml"}}}, inputs: typesOf(pjShowInputs{})},
	"set":   {meta: InputSettings{ConfigFiles: []ConfigFile{{Name: "main", Format: "yaml"}}}, inputs: typesOf(acSetInputs{})},
	"ship":  {meta: InputSettings{ConfigFiles: []ConfigFile{{Name: "ship", Format: "yaml"}}}, inputs: typesOf(acShipInputs{})},
}

// wired gives rtx exactly the features fs.
func wired(rtx *Context, fs ...Feature) *Context {
	rtx.clock.feats = newFeatureSet(fs)
	return rtx
}

// wiredFor gives rtx exactly the features def, meta and inputs need.
func wiredFor(rtx *Context, def Definition, meta InputSettings, inputs ...reflect.Type) *Context {
	return wired(rtx, featureNeeds(def, meta, inputs...)...)
}

// wiringModes runs fn twice: "legacy", where every program and Context has every feature, and
// "wired", where NewProgram, NewProgramFunc and NewContextFor give only the features
// featureNeeds finds for the definition's fixture. Both must pass: a wired program runs every
// command exactly as one with every feature. Each runs fn under [fallbackPaths], so every case
// also reads its fallbacks both ways.
//
// The wired run replaces package state, so fn and the tests it calls must not use t.Parallel
// outside their own subtests.
func wiringModes(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	t.Run("legacy", func(t *testing.T) { fallbackPaths(t, fn) })
	t.Run("wired", func(t *testing.T) {
		var mu sync.Mutex
		var missing []string
		prev := legacyFeatures
		legacyFeatures = func(def Definition) *featureSet {
			fx, ok := wiringFixtures[def.Name]
			if !ok {
				mu.Lock()
				if !slices.Contains(missing, def.Name) {
					missing = append(missing, def.Name)
				}
				mu.Unlock()
			}
			return newFeatureSet(featureNeeds(def, fx.meta, fx.inputs...))
		}
		t.Cleanup(func() {
			legacyFeatures = prev
			for _, name := range missing {
				t.Errorf("Definition %q has no wiringFixtures entry", name)
			}
		})
		fallbackPaths(t, fn)
	})
}

// typesOf is the reflect.Type of each value.
func typesOf(values ...any) []reflect.Type {
	out := make([]reflect.Type, len(values))
	for i, v := range values {
		out[i] = reflect.TypeOf(v)
	}
	return out
}
