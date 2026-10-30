package rotini

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// A differential check of the argv grammar: modelParse is an independent reading of the
// documented grammar for one small program, written from the rules rather than from the
// parser, and every argv is parsed both ways. The two must agree on whether the line parses,
// which command it reaches, and every value it sets.
//
// The grammar it encodes:
//
//   - Words are read left to right. "--" ends flags: every later word is an argument. A word
//     is a flag when it starts with "-", is longer than "-", is not "--", and is not a
//     negative number ("-" then a digit, or "-." then a digit).
//   - Commands are named before the first argument; a word naming a sub-command of the last
//     command reached descends into it. Once an argument is seen, names are arguments.
//   - "--name" or "--name=value" names a flag of the commands reached so far, the nearest
//     first; a negatable bool also answers to "--no-name", which takes no value. A flag that
//     isn't cascading belongs to its own command only: given while a sub-command runs, it is
//     an error.
//   - "-x" or "-x=value" is a short flag when -x is declared. Any other single-dash word is a
//     cluster, one short flag per character: a bool or count flag is set and the next
//     character read; a value-taking flag takes the rest of the word (one leading "=" dropped)
//     and ends the cluster. A cluster's trailing "=value" belongs to its last flag, which must
//     be a bool.
//   - A bool takes no separate word: bare is true, "=value" reads a bool. A count takes no
//     value at all. An optional-value flag takes "=value" or its implicit value, never the
//     next word. Any other flag takes "=value" or else the next word, whatever it is.
//   - A single-value flag keeps its last value; a list appends; a count adds one each time.
//   - Arguments fill the invoked command's arguments in order, the variadic last; a command
//     without arguments takes none.

// modelDef is the program both sides parse.
func modelDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "count"},
			{Name: "color", Identifiers: []string{"--color"}, Type: "bool", Negatable: true},
			{Name: "top", Identifiers: []string{"-T", "--top"}, Type: "string", Local: true},
		},
		Commands: []CommandDef{{
			Name: "run", Handler: "AppRun", Aliases: []string{"r"},
			Flags: []FlagDef{
				{Name: "out", Identifiers: []string{"-o", "--out"}, Type: "string", Local: true},
				{Name: "all", Identifiers: []string{"-a", "--all"}, Type: "bool", Local: true},
				{Name: "big", Identifiers: []string{"-b"}, Type: "bool", Local: true},
				{Name: "count", Identifiers: []string{"-c", "--count"}, Type: "int", Local: true},
				{Name: "tag", Identifiers: []string{"-t", "--tag"}, Type: "[]string", Local: true},
				{Name: "opt", Identifiers: []string{"--opt"}, Type: "string", ImplicitValue: "yes", Local: true},
				{Name: "depth", Identifiers: []string{"-d", "--depth"}, Type: "int"},
			},
			Arguments: []ArgDef{{Name: "first", Type: "string"}, {Name: "rest", Type: "[]string", Variadic: true}},
			Commands: []CommandDef{{
				Name: "deep", Handler: "AppRunDeep",
				Flags: []FlagDef{{Name: "x", Identifiers: []string{"-x", "--x"}, Type: "string", Local: true}},
			}},
		}},
	}
}

type modelRootFrame struct {
	Flags struct {
		Verbose int    `rotini:"verbose"`
		Color   bool   `rotini:"color"`
		Top     string `rotini:"top"`
	}
	Arguments struct{}
}

type modelRunFrame struct {
	Flags struct {
		Out   string   `rotini:"out"`
		All   bool     `rotini:"all"`
		Big   bool     `rotini:"big"`
		Count int      `rotini:"count"`
		Tag   []string `rotini:"tag"`
		Opt   string   `rotini:"opt"`
		Depth int      `rotini:"depth"`
	}
	Arguments struct {
		First string   `rotini:"first"`
		Rest  []string `rotini:"rest"`
	}
}

type modelDeepFrame struct {
	Flags struct {
		X string `rotini:"x"`
	}
	Arguments struct{}
}

// modelResult is what a parse produced: the command path, every value as text, and whether
// it failed.
type modelResult struct {
	Path   string
	Values map[string]string
	Failed bool
}

// modelFlag is one flag of modelDef as the model sees it.
type modelFlag struct {
	name, kind, implicit string // kind: bool, count, string, int, list
	owner                int    // the chain depth that declares it
	local, negatable     bool
	ids                  []string
}

var modelFlags = []modelFlag{
	{name: "verbose", kind: "count", owner: 0, ids: []string{"-v", "--verbose"}},
	{name: "color", kind: "bool", owner: 0, negatable: true, ids: []string{"--color"}},
	{name: "top", kind: "string", owner: 0, local: true, ids: []string{"-T", "--top"}},
	{name: "out", kind: "string", owner: 1, local: true, ids: []string{"-o", "--out"}},
	{name: "all", kind: "bool", owner: 1, local: true, ids: []string{"-a", "--all"}},
	{name: "big", kind: "bool", owner: 1, local: true, ids: []string{"-b"}},
	{name: "count", kind: "int", owner: 1, local: true, ids: []string{"-c", "--count"}},
	{name: "tag", kind: "list", owner: 1, local: true, ids: []string{"-t", "--tag"}},
	{name: "opt", kind: "string", owner: 1, local: true, implicit: "yes", ids: []string{"--opt"}},
	{name: "depth", kind: "int", owner: 1, ids: []string{"-d", "--depth"}},
	{name: "x", kind: "string", owner: 2, local: true, ids: []string{"-x", "--x"}},
}

// modelLookup finds the flag word id names among the flags of the first depth commands, and
// whether it named the negated form.
func modelLookup(id string, depth int) (modelFlag, bool, bool) {
	for _, f := range modelFlags {
		if f.owner < depth && slices.Contains(f.ids, id) {
			return f, false, true
		}
	}
	for _, f := range modelFlags {
		if f.owner < depth && f.negatable && id == "--no-"+strings.TrimPrefix(f.ids[len(f.ids)-1], "--") {
			return f, true, true
		}
	}
	return modelFlag{}, false, false
}

// modelIsFlag is the documented flag-word test.
func modelIsFlag(w string) bool {
	if !strings.HasPrefix(w, "-") || w == "-" || w == "--" {
		return false
	}
	if c := w[1]; c >= '0' && c <= '9' {
		return false
	}
	return !(len(w) >= 3 && w[1] == '.' && w[2] >= '0' && w[2] <= '9')
}

func modelBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "t", "1", "yes", "y", "on":
		return true, true
	case "false", "f", "0", "no", "n", "off":
		return false, true
	}
	return false, false
}

// modelParse reads argv by the grammar above.
func modelParse(argv []string) modelResult {
	children := [][]string{{"run", "r"}, {"deep"}, nil}
	fail := modelResult{Failed: true}

	// The command path: names before the first argument, skipping flags and the words they take.
	depth := 1
	for i := 0; i < len(argv); i++ {
		w := argv[i]
		if w == "--" {
			break
		}
		if modelIsFlag(w) {
			i += modelWidth(w, argv, i, depth)
			continue
		}
		if slices.Contains(children[depth-1], w) {
			depth++
			continue
		}
		break
	}
	leaf := depth - 1

	vals := map[string][]string{}
	kinds := map[string]string{}
	var args []string
	set := func(f modelFlag, v string) bool {
		if f.local && f.owner != leaf {
			return false
		}
		vals[f.name] = append(vals[f.name], v)
		kinds[f.name] = f.kind
		return true
	}
	reached, positional, terminated := 1, false, false
	for i := 0; i < len(argv); i++ {
		w := argv[i]
		switch {
		case !terminated && w == "--":
			terminated, positional = true, true
		case !terminated && modelIsFlag(w):
			used, ok := modelFlagWord(w, argv, i, reached, set)
			if !ok {
				return fail
			}
			i += used
		case !positional && reached < depth && slices.Contains(children[reached-1], w):
			reached++
		default:
			positional = true
			args = append(args, w)
		}
	}
	if len(args) > 0 && leaf != 1 {
		return fail // only run takes arguments
	}

	out := modelResult{Path: strings.Join([]string{"app", "app run", "app run deep"}[leaf:leaf+1], ""), Values: map[string]string{}}
	for name, vs := range vals {
		last := vs[len(vs)-1]
		switch kinds[name] {
		case "count":
			last = strconv.Itoa(len(vs))
		case "list":
			last = strings.Join(vs, "\x1f")
		case "bool":
			b, ok := modelBool(last)
			if !ok {
				return fail
			}
			last = strconv.FormatBool(b)
		case "int":
			n, err := strconv.ParseInt(last, 10, 64)
			if err != nil {
				return fail
			}
			last = strconv.FormatInt(n, 10)
		}
		out.Values[name] = last
	}
	if len(args) > 0 {
		out.Values["first"] = args[0]
	}
	if len(args) > 1 {
		out.Values["rest"] = strings.Join(args[1:], "\x1f")
	}
	return out
}

// modelWidth is how many words after argv[i] the flag word w takes, for finding the command
// path; a word that doesn't parse takes none.
func modelWidth(w string, argv []string, i, depth int) int {
	n, ok := modelFlagWord(w, argv, i, depth, func(modelFlag, string) bool { return true })
	if !ok {
		return 0
	}
	return n
}

// modelFlagWord reads one flag word, calling set for each value, and returns how many later
// words it took.
func modelFlagWord(w string, argv []string, i, depth int, set func(modelFlag, string) bool) (int, bool) {
	name, inline, hasInline := strings.Cut(w, "=")
	if f, negated, ok := modelLookup(name, depth); ok {
		switch {
		case negated:
			return 0, !hasInline && set(f, "false")
		case f.kind == "count":
			return 0, !hasInline && set(f, "1")
		case f.kind == "bool":
			if !hasInline {
				inline = "true"
			}
			return 0, set(f, inline)
		case hasInline:
			return 0, set(f, inline)
		case f.implicit != "":
			return 0, set(f, f.implicit)
		case i+1 < len(argv):
			return 1, set(f, argv[i+1])
		}
		return 0, false
	}
	if strings.HasPrefix(w, "--") || len(name) <= 2 {
		return 0, false
	}
	// A cluster: one short flag per character of the word.
	body := w[1:]
	for k := 0; k < len(body); {
		r, size := utf8.DecodeRuneInString(body[k:])
		if r == '=' {
			return 0, false // an "=value" after a count, or after a flag that isn't last
		}
		f, _, ok := modelLookup("-"+body[k:k+size], depth)
		if !ok {
			return 0, false
		}
		rest := body[k+size:]
		switch f.kind {
		case "bool":
			if v, isValue := strings.CutPrefix(rest, "="); isValue {
				return 0, set(f, v)
			}
			if !set(f, "true") {
				return 0, false
			}
		case "count":
			if !set(f, "1") {
				return 0, false
			}
		default:
			if rest != "" {
				return 0, set(f, strings.TrimPrefix(rest, "="))
			}
			if f.implicit != "" {
				return 0, set(f, f.implicit)
			}
			if i+1 < len(argv) {
				return 1, set(f, argv[i+1])
			}
			return 0, false
		}
		k += size
	}
	return 0, true
}

// realParse parses argv with rotini, into the inputs struct for the command it reaches.
func realParse(argv []string) modelResult {
	rtx := NewContextFor(modelDef(), argv)
	out := modelResult{Path: rtx.CommandPath(), Values: map[string]string{}}
	put := func(name string, v reflect.Value) {
		switch {
		case v.Kind() == reflect.Slice && v.Len() > 0:
			out.Values[name] = strings.Join(v.Interface().([]string), "\x1f")
		case v.Kind() != reflect.Slice && !v.IsZero():
			out.Values[name] = textOf(v)
		}
	}
	var frames []reflect.Value
	var err error
	switch len(rtx.CommandChain()) {
	case 1:
		var in struct{ App modelRootFrame }
		err = NewParser().Parse(rtx, &in)
		frames = []reflect.Value{reflect.ValueOf(in.App)}
	case 2:
		var in struct {
			App modelRootFrame
			Run modelRunFrame
		}
		err = NewParser().Parse(rtx, &in)
		frames = []reflect.Value{reflect.ValueOf(in.App), reflect.ValueOf(in.Run)}
	default:
		var in struct {
			App  modelRootFrame
			Run  modelRunFrame
			Deep modelDeepFrame
		}
		err = NewParser().Parse(rtx, &in)
		frames = []reflect.Value{reflect.ValueOf(in.App), reflect.ValueOf(in.Run), reflect.ValueOf(in.Deep)}
	}
	if err != nil {
		if _, ok := asError[*ParseError](err); !ok {
			return modelResult{Path: "not a *ParseError: " + err.Error(), Failed: true}
		}
		return modelResult{Failed: true}
	}
	for _, frame := range frames {
		for _, part := range []string{"Flags", "Arguments"} {
			s := frame.FieldByName(part)
			for j := range s.NumField() {
				put(s.Type().Field(j).Tag.Get("rotini"), s.Field(j))
			}
		}
	}
	return out
}

// textOf is a bound scalar as the model writes it.
func textOf(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int:
		return strconv.FormatInt(v.Int(), 10)
	}
	return v.String()
}

// modelAgree fails t when rotini and the model disagree about argv. A value the model sets to
// its zero (false, 0, "") reads as unset on the rotini side, since a bound field can't tell.
func modelAgree(t *testing.T, argv []string) {
	t.Helper()
	want, got := modelParse(argv), realParse(argv)
	if !want.Failed {
		for k, v := range want.Values {
			if (v == "false" || v == "0" || v == "") && k != "rest" && k != "tag" {
				delete(want.Values, k)
			}
		}
	}
	if want.Failed != got.Failed || (!want.Failed && (want.Path != got.Path || !reflect.DeepEqual(want.Values, got.Values))) {
		t.Fatalf("argv %q:\n model: %+v\nrotini: %+v", argv, want, got)
	}
}

// modelWords is a vocabulary of argv words for random lines: every spelling the grammar
// distinguishes, with values that read as each type and as none.
var modelWords = []string{
	"run", "r", "deep", "x", "y", "--", "-", "-5", "-.5", "-1x", "-.", "-=x", "--=x",
	"-v", "-vv", "--verbose", "--verbose=1", "-v=1", "-vT", "-vTx",
	"--color", "--color=false", "--color=yes", "--color=", "--no-color", "--no-color=1",
	"-T", "--top", "--top=a", "-Tval", "-T=val",
	"-o", "--out", "--out=a=b", "-o=", "-ov", "-aov", "-ao", "-ab", "-ba=false", "-ab=x", "-abo=x",
	"-a", "--all", "--all=0", "-a=no", "-b", "-b=1", "--b",
	"-c", "--count", "-c5", "-c-5", "-c=7", "--count=+3", "--count=x", "-vc5", "-cv",
	"-t", "--tag", "-ta", "--tag=a,b", "--opt", "--opt=z", "--opt=",
	"-d", "--depth", "-d3", "--depth=2", "-x", "--x", "-xq", "--x=q",
	"--nope", "-z", "-az", "--no-all", "-vvv=2", "-é", "true", "1", "",
}

// TestArgvModel_agrees runs fixed lines and a seeded stream of random ones through both.
func TestArgvModel_agrees(t *testing.T) {
	for _, line := range [][]string{
		{"run", "-aov", "x"}, {"-v", "run", "-v", "deep", "-v"}, {"run", "--out", "--", "--"},
		{"r", "-ab=false"}, {"run", "-ab="}, {"--top", "a", "run"}, {"run", "deep", "-d", "3"},
		{"run", "-d", "3", "deep"}, {"run", "x", "deep"}, {"--", "run"}, {"run", "-c", "-5"},
		{"run", "--opt", "x"}, {"run", "-vvv=2"}, {"-.", "run"}, {"run", "-=x"},
	} {
		modelAgree(t, line)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		line := make([]string, rng.IntN(7))
		for i := range line {
			line[i] = modelWords[rng.IntN(len(modelWords))]
		}
		modelAgree(t, line)
	}
}

// FuzzArgvModel checks the parser against the model on fuzzed lines: each input byte below
// 0x80 picks a vocabulary word, and runs of other bytes are words of their own.
func FuzzArgvModel(f *testing.F) {
	f.Add([]byte{0, 3, 19, 30})
	f.Add([]byte("\x00\x13\xc3\xa9\x05"))
	f.Add([]byte("\x01\x02\x07\x00\x05\x05"))
	f.Fuzz(func(t *testing.T, data []byte) {
		var line []string
		for i := 0; i < len(data) && len(line) < 12; i++ {
			if data[i] < 0x80 {
				line = append(line, modelWords[int(data[i])%len(modelWords)])
				continue
			}
			j := i
			for j < len(data) && data[j] >= 0x80 {
				j++
			}
			line = append(line, "-"+string(data[i:j]))
			i = j - 1
		}
		modelAgree(t, line)
	})
}
