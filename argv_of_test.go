package rotini

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The ArgvOf fixture: a root and three sub-commands covering every input kind.

type argvDB struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type argvAppFlags struct {
	Help    bool   `rotini:"help"`
	Verbose bool   `rotini:"verbose"`
	Out     string `rotini:"out"`
}

type argvAppCI struct {
	Flags     argvAppFlags
	Arguments struct {
		Word string `rotini:"word"`
	}
}

type argvAppInputs struct{ App argvAppCI }

type argvRunFlags struct {
	Help      bool                 `rotini:"help"`
	Color     bool                 `rotini:"color"`
	Plain     bool                 `rotini:"plain"`
	Count     int                  `rotini:"count"`
	Verbosity int                  `rotini:"verbosity"`
	Quiet     int                  `rotini:"quiet"`
	Mode      string               `rotini:"mode"`
	Tags      []string             `rotini:"tag"`
	Labels    []string             `rotini:"labels"`
	Ports     []int                `rotini:"port"`
	Env       map[string]string    `rotini:"env"`
	Envs      map[string]string    `rotini:"envs"`
	Set       map[string]any       `rotini:"set"`
	Values    map[string]any       `rotini:"values"`
	DB        argvDB               `rotini:"db"`
	DBs       []argvDB             `rotini:"dbs"`
	Limit     *int                 `rotini:"limit"`
	When      time.Time            `rotini:"when"`
	Date      time.Time            `rotini:"date"`
	Unix      time.Time            `rotini:"unix"`
	Milli     time.Time            `rotini:"milli"`
	Custom    time.Time            `rotini:"custom"`
	Wait      time.Duration        `rotini:"wait"`
	URL       *url.URL             `rotini:"url"`
	Mail      mail.Address         `rotini:"mail"`
	Loc       *time.Location       `rotini:"loc"`
	MAC       net.HardwareAddr     `rotini:"mac"`
	IP        netip.Addr           `rotini:"ip"`
	Size      ByteSize             `rotini:"size"`
	Hex       HexBytes             `rotini:"hex"`
	B64       Base64Bytes          `rotini:"b64"`
	Body      string               `rotini:"body"`
	Token     string               `rotini:"token"`
	New       string               `rotini:"new"`
	Ratio     float64              `rotini:"ratio"`
	Short     string               `rotini:"short"`
	Upper     upperString          `rotini:"upper"`
	Shout     argvShout            `rotini:"shout"`
	Opaque    argvOpaque           `rotini:"opaque"`
	Tint      bool                 `rotini:"tint"`
	Day       time.Time            `rotini:"day"`
	Ago       time.Time            `rotini:"ago"`
	Match     *regexp.Regexp       `rotini:"match"`
	Glob      Glob                 `rotini:"glob"`
	Level     string               `rotini:"level"`
	In        string               `rotini:"in"`
	OutF      string               `rotini:"outf"`
	Dates     map[string]time.Time `rotini:"dates"`
}

type argvRunArgs struct {
	Target string   `rotini:"target" recon:"target" env:"APP_TARGET"`
	Files  []string `rotini:"files"`
}

type argvRunEnv struct {
	Token string         `rotini:"token" recon:"token,secret" env:"APP_TOKEN,TOKEN"`
	Names []string       `rotini:"names" recon:"names" env:"APP_NAMES"`
	Since time.Time      `rotini:"since" recon:"since" env:"APP_SINCE" layout:"2006-01-02"`
	HTTP  map[string]any `rotini:"http" recon:"http" envnest:"APP_HTTP,__"`
	Paths []string       `rotini:"paths" recon:"paths,separator=:" env:"APP_PATHS"`
}

type argvRunConfig struct {
	Region string `rotini:"region" recon:"region"`
}

type argvRunCI struct {
	Flags     argvRunFlags
	Arguments argvRunArgs
	Env       argvRunEnv
	Config    argvRunConfig
	Stdin     *string `stdin:"text"`
}

type argvRunInputs struct {
	App argvAppCI
	Run argvRunCI
}

type argvShCI struct {
	Flags     struct{}
	Arguments struct {
		Words []string `rotini:"words"`
	}
}

type argvShInputs struct {
	App argvAppCI
	Sh  argvShCI
}

type argvExecCI struct {
	Flags struct {
		Dry bool `rotini:"dry"`
	}
	Arguments struct {
		Name string   `rotini:"name"`
		Rest []string `rotini:"rest"`
	}
}

type argvExecInputs struct {
	App  argvAppCI
	Exec argvExecCI
}

// argvShout parses itself but has no MarshalText; its String reads back.
type argvShout string

func (s *argvShout) UnmarshalText(b []byte) error {
	*s = argvShout(strings.ToUpper(string(b)))
	return nil
}
func (s argvShout) String() string { return string(s) }

// argvOpaque parses itself, and has neither MarshalText nor a String that reads back.
type argvOpaque struct{ n int }

func (o *argvOpaque) UnmarshalText(b []byte) error { o.n = len(b); return nil }

const argvDBSchema = `{"type":"object","properties":{"host":{"type":"string"},"port":{"type":"integer"}}}`

func argvDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "help", Identifiers: []string{"--help", "-h"}, Type: "bool", ShortCircuit: true},
			{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"},
			{Name: "out", Identifiers: []string{"-o"}, Type: "string"},
		},
		Arguments: []ArgDef{{Name: "word", Type: "string"}},
		Inputs:    reflect.TypeFor[argvAppInputs](),
		Commands: []CommandDef{
			{
				Name: "run", Handler: "AppRun", Aliases: []string{"r"},
				Inputs: reflect.TypeFor[argvRunInputs](),
				Flags: []FlagDef{
					{Name: "help", Identifiers: []string{"--help", "-h"}, Type: "bool"},
					{Name: "color", Identifiers: []string{"--color"}, Type: "bool", Negatable: true, Default: "true"},
					{Name: "plain", Identifiers: []string{"-p"}, Type: "bool"},
					{Name: "count", Identifiers: []string{"--count", "-c"}, Type: "int"},
					{Name: "verbosity", Identifiers: []string{"--verbosity", "-V"}, Type: "count"},
					{Name: "quiet", Identifiers: []string{"--quiet"}, Type: "count", Default: "1"},
					{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", ImplicitValue: "always"},
					{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string"},
					{Name: "labels", Identifiers: []string{"--labels"}, Type: "[]string", Separator: ","},
					{Name: "port", Identifiers: []string{"--port"}, Type: "[]int"},
					{Name: "env", Identifiers: []string{"--env"}, Type: "map[string]string"},
					{Name: "envs", Identifiers: []string{"--envs"}, Type: "map[string]string", Separator: ","},
					{Name: "set", Identifiers: []string{"--set"}, Type: "map[string]any"},
					{Name: "values", Identifiers: []string{"--values"}, Type: "map[string]any", DottedKeys: true},
					{Name: "db", Identifiers: []string{"--db"}, Type: "argvDB", ObjectSchema: argvDBSchema},
					{Name: "dbs", Identifiers: []string{"--dbs"}, Type: "[]argvDB", ObjectSchema: argvDBSchema},
					{Name: "limit", Identifiers: []string{"--limit"}, Type: "*int"},
					{Name: "when", Identifiers: []string{"--when"}, Type: "time.Time"},
					{Name: "date", Identifiers: []string{"--date"}, Type: "time.Time", Layout: layoutDate},
					{Name: "unix", Identifiers: []string{"--unix"}, Type: "time.Time", Layout: layoutUnix},
					{Name: "milli", Identifiers: []string{"--milli"}, Type: "time.Time", Layout: layoutUnixMilli},
					{Name: "custom", Identifiers: []string{"--custom"}, Type: "time.Time", Layout: "Jan 2 2006 15:04"},
					{Name: "wait", Identifiers: []string{"--wait"}, Type: "time.Duration"},
					{Name: "url", Identifiers: []string{"--url"}, Type: "*url.URL"},
					{Name: "mail", Identifiers: []string{"--mail"}, Type: "mail.Address"},
					{Name: "loc", Identifiers: []string{"--loc"}, Type: "*time.Location"},
					{Name: "mac", Identifiers: []string{"--mac"}, Type: "net.HardwareAddr"},
					{Name: "ip", Identifiers: []string{"--ip"}, Type: "netip.Addr"},
					{Name: "size", Identifiers: []string{"--size"}, Type: "ByteSize"},
					{Name: "hex", Identifiers: []string{"--hex"}, Type: "HexBytes"},
					{Name: "b64", Identifiers: []string{"--b64"}, Type: "Base64Bytes"},
					{Name: "body", Identifiers: []string{"--body"}, Type: "string", From: []string{"file", "stdin"}},
					{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true},
					{Name: "new", Identifiers: []string{"--old", "--new"}, Type: "string", DeprecatedIdentifiers: []string{"--old"}},
					{Name: "ratio", Identifiers: []string{"--ratio"}, Type: "float64"},
					{Name: "short", Identifiers: []string{"-s"}, Type: "string"},
					{Name: "upper", Identifiers: []string{"--upper"}, Type: "upperString"},
					{Name: "shout", Identifiers: []string{"--shout"}, Type: "argvShout"},
					{Name: "opaque", Identifiers: []string{"--opaque"}, Type: "argvOpaque"},
					{Name: "tint", Identifiers: []string{"--tint"}, Type: "bool", Negatable: true, Negation: "--mono", Default: "true"},
					{Name: "day", Identifiers: []string{"--day"}, Type: "time.Time", Layout: "02/01/2006", Layouts: []string{"02/01/2006", layoutDate}},
					{Name: "ago", Identifiers: []string{"--ago"}, Type: "time.Time", Relative: "past"},
					{Name: "match", Identifiers: []string{"--match"}, Type: "*regexp.Regexp"},
					{Name: "glob", Identifiers: []string{"--glob"}, Type: "Glob"},
					{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"}, IgnoreCase: true,
						EnumValues: []EnumValue{{Value: "low", Aliases: []string{"lo"}}, {Value: "high", Aliases: []string{"hi"}}}},
					{Name: "in", Identifiers: []string{"--in"}, Type: "inputfile"},
					{Name: "outf", Identifiers: []string{"--outf"}, Type: "outputfile"},
					{Name: "dates", Identifiers: []string{"--dates"}, Type: "map[string]time.Time", Layout: layoutDate},
				},
				Arguments: []ArgDef{
					{Name: "target", Type: "string", From: []string{"file", "stdin"}},
					{Name: "files", Type: "[]string", Variadic: true, Separator: ","},
				},
				Commands: []CommandDef{{Name: "child", Handler: "AppRunChild"}},
				Plugins:  []PluginDef{{Name: "plug", Binary: "app-plug"}},
			},
			{
				Name: "sh", Handler: "AppSh", Passthrough: true,
				Inputs:    reflect.TypeFor[argvShInputs](),
				Arguments: []ArgDef{{Name: "words", Type: "[]string", Variadic: true}},
			},
			{
				Name: "exec", Handler: "AppExec",
				Inputs:    reflect.TypeFor[argvExecInputs](),
				Flags:     []FlagDef{{Name: "dry", Identifiers: []string{"--dry"}, Type: "bool"}},
				Arguments: []ArgDef{{Name: "name", Type: "string"}, {Name: "rest", Type: "[]string", Variadic: true, Passthrough: true}},
			},
		},
	}
}

// run returns a run inputs value with fill applied and every field fill touched in set.
func argvRun(fill func(*argvRunCI), paths ...string) (argvRunInputs, Presence) {
	var in argvRunInputs
	fill(&in.Run)
	set := Presence{}
	for _, p := range paths {
		set[FieldPath(p)] = InputSource{Layer: "custom"}
	}
	return in, set
}

func TestArgvOfRules(t *testing.T) {
	ts := time.Date(2026, 10, 8, 14, 30, 0, 500, time.UTC)
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		fill  func(*argvRunCI)
		paths []string
		opts  []ArgvOption
		argv  []string
		env   []string
		err   string // the *ArgvError's Field, when it can't be expressed
	}{
		{name: "nothing set", fill: func(*argvRunCI) {}, argv: []string{"run"}},
		{name: "same flag on root and leaf", fill: func(c *argvRunCI) { c.Flags.Help = true },
			paths: []string{"Run.Flags.Help"}, argv: []string{"run", "--help"}},
		{name: "short-only flag", fill: func(c *argvRunCI) { c.Flags.Short = "x" },
			paths: []string{"Run.Flags.Short"}, argv: []string{"run", "-s=x"}},
		{name: "short-only bool false", fill: func(*argvRunCI) {},
			paths: []string{"Run.Flags.Plain"}, argv: []string{"run", "-p=false"}},
		{name: "implicit value written attached", fill: func(c *argvRunCI) { c.Flags.Mode = "always" },
			paths: []string{"Run.Flags.Mode"}, argv: []string{"run", "--mode=always"}},
		{name: "negatable true", fill: func(c *argvRunCI) { c.Flags.Color = true },
			paths: []string{"Run.Flags.Color"}, argv: []string{"run", "--color"}},
		{name: "negatable false over a true default", fill: func(*argvRunCI) {},
			paths: []string{"Run.Flags.Color"}, argv: []string{"run", "--no-color"}},
		{name: "explicit zero", fill: func(*argvRunCI) {},
			paths: []string{"Run.Flags.Count"}, argv: []string{"run", "--count=0"}},
		{name: "count 3", fill: func(c *argvRunCI) { c.Flags.Verbosity = 3 },
			paths: []string{"Run.Flags.Verbosity"}, argv: []string{"run", "--verbosity", "--verbosity", "--verbosity"}},
		{name: "count 0", fill: func(*argvRunCI) {},
			paths: []string{"Run.Flags.Verbosity"}, argv: []string{"run"}},
		{name: "count 0 over a default", fill: func(*argvRunCI) {},
			paths: []string{"Run.Flags.Quiet"}, err: "Run.Flags.Quiet"},
		{name: "negative count", fill: func(c *argvRunCI) { c.Flags.Verbosity = -1 },
			paths: []string{"Run.Flags.Verbosity"}, err: "Run.Flags.Verbosity"},
		{name: "list without a separator", fill: func(c *argvRunCI) { c.Flags.Tags = []string{"a,b", "", " c"} },
			paths: []string{"Run.Flags.Tags"}, argv: []string{"run", "--tag=a,b", "--tag=", "--tag= c"}},
		{name: "empty list without a separator", fill: func(c *argvRunCI) { c.Flags.Tags = []string{} },
			paths: []string{"Run.Flags.Tags"}, err: "Run.Flags.Tags"},
		{name: "list with a separator", fill: func(c *argvRunCI) { c.Flags.Labels = []string{"a,b", `say "hi"`, " c", "", "d"} },
			paths: []string{"Run.Flags.Labels"},
			argv:  []string{"run", `--labels="a,b"`, `--labels="say ""hi"""`, `--labels=" c"`, `--labels=""`, "--labels=d"}},
		{name: "empty list with a separator", fill: func(c *argvRunCI) { c.Flags.Labels = []string{} },
			paths: []string{"Run.Flags.Labels"}, argv: []string{"run", "--labels="}},
		{name: "int list", fill: func(c *argvRunCI) { c.Flags.Ports = []int{80, -1} },
			paths: []string{"Run.Flags.Ports"}, argv: []string{"run", "--port=80", "--port=-1"}},
		{name: "map, keys sorted", fill: func(c *argvRunCI) { c.Flags.Env = map[string]string{"b": "2=x", "a": "1"} },
			paths: []string{"Run.Flags.Env"}, argv: []string{"run", "--env=a=1", "--env=b=2=x"}},
		{name: "map key with =", fill: func(c *argvRunCI) { c.Flags.Env = map[string]string{"a=b": "1"} },
			paths: []string{"Run.Flags.Env"}, err: "Run.Flags.Env"},
		{name: "map with a separator", fill: func(c *argvRunCI) { c.Flags.Envs = map[string]string{"a": "1,2"} },
			paths: []string{"Run.Flags.Envs"}, argv: []string{"run", `--envs="a=1,2"`}},
		{name: "empty map without a separator", fill: func(c *argvRunCI) { c.Flags.Env = map[string]string{} },
			paths: []string{"Run.Flags.Env"}, err: "Run.Flags.Env"},
		{name: "free-form map", fill: func(c *argvRunCI) { c.Flags.Set = map[string]any{"n": 3.5, "b": true, "z": nil, "s": "text"} },
			paths: []string{"Run.Flags.Set"}, argv: []string{"run", "--set=b=true", "--set=n=3.5", "--set=s=text", "--set=z=null"}},
		{name: "free-form text that reads back as a number", fill: func(c *argvRunCI) { c.Flags.Set = map[string]any{"n": "3"} },
			paths: []string{"Run.Flags.Set"}, err: "Run.Flags.Set"},
		{name: "free-form list value", fill: func(c *argvRunCI) { c.Flags.Set = map[string]any{"l": []any{"a"}} },
			paths: []string{"Run.Flags.Set"}, err: "Run.Flags.Set"},
		{name: "free-form nested map", fill: func(c *argvRunCI) { c.Flags.Set = map[string]any{"a": map[string]any{"b": "c"}} },
			paths: []string{"Run.Flags.Set"}, err: "Run.Flags.Set"},
		{name: "dotted keys", fill: func(c *argvRunCI) {
			c.Flags.Values = map[string]any{"image": map[string]any{"tag": "v2", "pull": false}, "replicas": 3.0}
		}, paths: []string{"Run.Flags.Values"}, argv: []string{"run", "--values=image.pull=false", "--values=image.tag=v2", "--values=replicas=3"}},
		{name: "dotted key holding a dot", fill: func(c *argvRunCI) { c.Flags.Values = map[string]any{"a.b": "c"} },
			paths: []string{"Run.Flags.Values"}, err: "Run.Flags.Values"},
		{name: "object", fill: func(c *argvRunCI) { c.Flags.DB = argvDB{Host: "db", Port: 5432} },
			paths: []string{"Run.Flags.DB"}, argv: []string{"run", `--db={"host":"db","port":5432}`}},
		{name: "list of objects", fill: func(c *argvRunCI) { c.Flags.DBs = []argvDB{{Host: "a"}, {Host: "b", Port: 1}} },
			paths: []string{"Run.Flags.DBs"}, argv: []string{"run", `--dbs={"host":"a","port":0}`, `--dbs={"host":"b","port":1}`}},
		{name: "nullable zero", fill: func(c *argvRunCI) { c.Flags.Limit = new(0) },
			paths: []string{"Run.Flags.Limit"}, argv: []string{"run", "--limit=0"}},
		{name: "nullable nil in set", fill: func(*argvRunCI) {},
			paths: []string{"Run.Flags.Limit"}, err: "Run.Flags.Limit"},
		{name: "RFC 3339", fill: func(c *argvRunCI) { c.Flags.When = ts },
			paths: []string{"Run.Flags.When"}, argv: []string{"run", "--when=2026-10-08T14:30:00.0000005Z"}},
		{name: "date", fill: func(c *argvRunCI) { c.Flags.Date = day },
			paths: []string{"Run.Flags.Date"}, argv: []string{"run", "--date=2026-10-08"}},
		{name: "date the layout can't show", fill: func(c *argvRunCI) { c.Flags.Date = ts },
			paths: []string{"Run.Flags.Date"}, err: "Run.Flags.Date"},
		{name: "unix", fill: func(c *argvRunCI) { c.Flags.Unix = time.Unix(1759104000, 250_000_000) },
			paths: []string{"Run.Flags.Unix"}, argv: []string{"run", "--unix=1759104000.25"}},
		{name: "unix before 1970", fill: func(c *argvRunCI) { c.Flags.Unix = time.Unix(-2, 250_000_000) },
			paths: []string{"Run.Flags.Unix"}, argv: []string{"run", "--unix=-1.75"}},
		{name: "unixmilli", fill: func(c *argvRunCI) { c.Flags.Milli = time.UnixMilli(1759104000123) },
			paths: []string{"Run.Flags.Milli"}, argv: []string{"run", "--milli=1759104000123"}},
		{name: "custom layout", fill: func(c *argvRunCI) { c.Flags.Custom = time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC) },
			paths: []string{"Run.Flags.Custom"}, argv: []string{"run", "--custom=Jan 2 2026 15:04"}},
		{name: "duration with days", fill: func(c *argvRunCI) { c.Flags.Wait = 36 * time.Hour },
			paths: []string{"Run.Flags.Wait"}, argv: []string{"run", "--wait=1d12h"}},
		{name: "value types", fill: func(c *argvRunCI) {
			c.Flags.URL, _ = url.Parse("https://example.com/x?y=1")
			c.Flags.Mail = mail.Address{Name: "Ada", Address: "ada@example.com"}
			c.Flags.Loc, _ = time.LoadLocation("Europe/Berlin")
			c.Flags.MAC, _ = net.ParseMAC("01:23:45:67:89:ab")
			c.Flags.IP = netip.MustParseAddr("10.0.0.1")
			c.Flags.Size = 512 << 20
			c.Flags.Hex = HexBytes{0xde, 0xad}
			c.Flags.B64 = Base64Bytes("hi")
		}, paths: []string{"Run.Flags.URL", "Run.Flags.Mail", "Run.Flags.Loc", "Run.Flags.MAC", "Run.Flags.IP", "Run.Flags.Size", "Run.Flags.Hex", "Run.Flags.B64"},
			argv: []string{"run", "--url=https://example.com/x?y=1", `--mail="Ada" <ada@example.com>`, "--loc=Europe/Berlin",
				"--mac=01:23:45:67:89:ab", "--ip=10.0.0.1", "--size=512Mi", "--hex=dead", "--b64=aGk="}},
		{name: "float", fill: func(c *argvRunCI) { c.Flags.Ratio = 0.1 },
			paths: []string{"Run.Flags.Ratio"}, argv: []string{"run", "--ratio=0.1"}},
		{name: "NaN", fill: func(c *argvRunCI) { c.Flags.Ratio = math.NaN() },
			paths: []string{"Run.Flags.Ratio"}, argv: []string{"run", "--ratio=NaN"}},
		{name: "a custom type's String that reads back", fill: func(c *argvRunCI) { c.Flags.Shout = "HEY" },
			paths: []string{"Run.Flags.Shout"}, argv: []string{"run", "--shout=HEY"}},
		{name: "a custom type that can't be written", fill: func(c *argvRunCI) { c.Flags.Opaque = argvOpaque{n: 1} },
			paths: []string{"Run.Flags.Opaque"}, err: "Run.Flags.Opaque"},
		{name: "a value that starts with @ on a from: file flag", fill: func(c *argvRunCI) { c.Flags.Body = "@alice" },
			paths: []string{"Run.Flags.Body"}, argv: []string{"run", "--body=@@alice"}},
		{name: "- on a from: stdin flag", fill: func(c *argvRunCI) { c.Flags.Body = "-" },
			paths: []string{"Run.Flags.Body"}, err: "Run.Flags.Body"},
		{name: "a value that starts with @ on a from: file argument", fill: func(c *argvRunCI) { c.Arguments.Target = "@alice" },
			paths: []string{"Run.Arguments.Target"}, argv: []string{"run", "--", "@@alice"}},
		{name: "- on a from: stdin argument", fill: func(c *argvRunCI) { c.Arguments.Target = "-" },
			paths: []string{"Run.Arguments.Target"}, err: "Run.Arguments.Target"},
		{name: "deprecated identifier avoided", fill: func(c *argvRunCI) { c.Flags.New = "x" },
			paths: []string{"Run.Flags.New"}, argv: []string{"run", "--new=x"}},
		{name: "secret refused", fill: func(c *argvRunCI) { c.Flags.Token = "s3cret" },
			paths: []string{"Run.Flags.Token"}, err: "Run.Flags.Token"},
		{name: "secret allowed", fill: func(c *argvRunCI) { c.Flags.Token = "s3cret" },
			paths: []string{"Run.Flags.Token"}, opts: []ArgvOption{ArgvSecrets()}, argv: []string{"run", "--token=s3cret"}},
		{name: "positionals after --", fill: func(c *argvRunCI) { c.Arguments.Target = "-5"; c.Arguments.Files = []string{"child", "a,b"} },
			paths: []string{"Run.Arguments.Target", "Run.Arguments.Files"}, argv: []string{"run", "--", "-5", "child", `"a,b"`}},
		{name: "positional named like a plugin", fill: func(c *argvRunCI) { c.Arguments.Target = "plug" },
			paths: []string{"Run.Arguments.Target"}, argv: []string{"run", "--", "plug"}},
		{name: "a gap in the positionals", fill: func(c *argvRunCI) { c.Arguments.Files = []string{"a"} },
			paths: []string{"Run.Arguments.Files"}, err: "Run.Arguments.Files"},
		{name: "an empty variadic", fill: func(c *argvRunCI) { c.Arguments.Target = "t"; c.Arguments.Files = []string{} },
			paths: []string{"Run.Arguments.Target", "Run.Arguments.Files"}, err: "Run.Arguments.Files"},
		{name: "env", fill: func(c *argvRunCI) {
			c.Env.Token = "t"
			c.Env.Names = []string{"a", "b"}
			c.Env.Since = day
			c.Env.HTTP = map[string]any{"retry": map[string]any{"max": "9"}, "timeout": "5s"}
		}, paths: []string{"Run.Env.Token", "Run.Env.Names", "Run.Env.Since", "Run.Env.HTTP"},
			argv: []string{"run"},
			env:  []string{"APP_TOKEN=t", "APP_NAMES=a,b", "APP_SINCE=2026-10-08", "APP_HTTP__RETRY__MAX=9", "APP_HTTP__TIMEOUT=5s"}},
		{name: "env list item with a comma", fill: func(c *argvRunCI) { c.Env.Names = []string{"a,b"} },
			paths: []string{"Run.Env.Names"}, err: "Run.Env.Names"},
		{name: "env list with its own separator", fill: func(c *argvRunCI) { c.Env.Paths = []string{"/a,b", "c"} },
			paths: []string{"Run.Env.Paths"}, argv: []string{"run"}, env: []string{"APP_PATHS=/a,b:c"}},
		{name: "env list item with its own separator", fill: func(c *argvRunCI) { c.Env.Paths = []string{"a:b"} },
			paths: []string{"Run.Env.Paths"}, err: "Run.Env.Paths"},
		{name: "custom negation", fill: func(*argvRunCI) {},
			paths: []string{"Run.Flags.Tint"}, argv: []string{"run", "--mono"}},
		{name: "the first of several layouts", fill: func(c *argvRunCI) { c.Flags.Day = day },
			paths: []string{"Run.Flags.Day"}, argv: []string{"run", "--day=08/10/2026"}},
		{name: "a relative time is written absolute", fill: func(c *argvRunCI) { c.Flags.Ago = ts },
			paths: []string{"Run.Flags.Ago"}, argv: []string{"run", "--ago=2026-10-08T14:30:00.0000005Z"}},
		{name: "regexp and glob", fill: func(c *argvRunCI) { c.Flags.Match = regexp.MustCompile(`^a+\d$`); c.Flags.Glob = "*.go" },
			paths: []string{"Run.Flags.Match", "Run.Flags.Glob"}, argv: []string{"run", `--match=^a+\d$`, "--glob=*.go"}},
		{name: "a nil regexp", fill: func(*argvRunCI) {},
			paths: []string{"Run.Flags.Match"}, err: "Run.Flags.Match"},
		{name: "an enum value with aliases", fill: func(c *argvRunCI) { c.Flags.Level = "high" },
			paths: []string{"Run.Flags.Level"}, argv: []string{"run", "--level=high"}},
		{name: "stdin and stdout as file kinds", fill: func(c *argvRunCI) { c.Flags.In = "-"; c.Flags.OutF = "-" },
			paths: []string{"Run.Flags.In", "Run.Flags.OutF"}, argv: []string{"run", "--in=-", "--outf=-"}},
		{name: "a map of dates", fill: func(c *argvRunCI) { c.Flags.Dates = map[string]time.Time{"start": day} },
			paths: []string{"Run.Flags.Dates"}, argv: []string{"run", "--dates=start=2026-10-08"}},
		{name: "a URL that doesn't read back", fill: func(c *argvRunCI) { c.Flags.URL = &url.URL{Path: "x"} },
			paths: []string{"Run.Flags.URL"}, err: "Run.Flags.URL"},
		{name: "a MAC address of no standard length", fill: func(c *argvRunCI) { c.Flags.MAC = net.HardwareAddr{1, 2} },
			paths: []string{"Run.Flags.MAC"}, err: "Run.Flags.MAC"},
		{name: "nested env key with the separator", fill: func(c *argvRunCI) { c.Env.HTTP = map[string]any{"a__b": "c"} },
			paths: []string{"Run.Env.HTTP"}, err: "Run.Env.HTTP"},
		{name: "config", fill: func(c *argvRunCI) { c.Config.Region = "eu" },
			paths: []string{"Run.Config.Region"}, err: "Run.Config.Region"},
		{name: "stdin", fill: func(c *argvRunCI) { c.Stdin = new("x") },
			paths: []string{"Run.Stdin"}, err: "Run.Stdin"},
		{name: "an ancestor's argument", fill: func(*argvRunCI) {},
			paths: []string{"App.Arguments.Word"}, err: "App.Arguments.Word"},
		{name: "root and leaf flags in their places", fill: func(c *argvRunCI) { c.Flags.Count = 2 },
			paths: []string{"App.Flags.Verbose", "App.Flags.Out", "Run.Flags.Count"},
			argv:  []string{"--verbose=false", "-o=", "run", "--count=2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, set := argvRun(tc.fill, tc.paths...)
			argv, env, err := ArgvOf(argvDef(), in, set, tc.opts...)
			if tc.err != "" {
				var ae *ArgvError
				if !errors.As(err, &ae) || ae.Field != FieldPath(tc.err) {
					t.Fatalf("err = %v, want an *ArgvError on %s", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(argv, tc.argv) {
				t.Errorf("argv = %q\nwant   %q", argv, tc.argv)
			}
			if !slices.Equal(env, tc.env) {
				t.Errorf("env = %q\nwant  %q", env, tc.env)
			}
			argvRoundTrip(t, argvDef(), in, set, argv, env)
		})
	}
}

// argvRoundTrip parses argv and env back and checks every field in set came back equal, and
// nothing else was set.
func argvRoundTrip[T any](t *testing.T, def Definition, in T, set Presence, argv, env []string) {
	t.Helper()
	rtx := NewContextFor(def, argv).WithEnviron(env)
	argvLayer, err := rtx.ArgvInputs[T]()
	if err != nil {
		t.Fatalf("parse %q: %v", argv, err)
	}
	envLayer, err := rtx.EnvInputs[T]()
	if err != nil {
		t.Fatalf("env %q: %v", env, err)
	}
	got := map[FieldPath]bool{}
	for p := range argvLayer.Set {
		got[p] = true
	}
	for p := range envLayer.Set {
		got[p] = true
	}
	for p := range set {
		if !got[p] && !zeroCount(in, p) {
			t.Errorf("%s was not read back from %q %q", p, argv, env)
			continue
		}
		layer := reflect.ValueOf(argvLayer.Values)
		if strings.Contains(string(p), ".Env.") {
			layer = reflect.ValueOf(envLayer.Values)
		}
		want, have := fieldAt(reflect.ValueOf(in), p), fieldAt(layer, p)
		if !argvEquivalent(want, have) {
			t.Errorf("%s: wrote %#v, read back %#v (argv %q)", p, want.Interface(), have.Interface(), argv)
		}
	}
	for p := range got {
		if _, ok := set[p]; !ok {
			t.Errorf("%s was read back but not in set (argv %q)", p, argv)
		}
	}
}

// zeroCount reports whether p is a count flag holding 0, which writes nothing.
func zeroCount(in any, p FieldPath) bool {
	f := fieldAt(reflect.ValueOf(in), p)
	return f.Kind() == reflect.Int && f.Int() == 0 && strings.Contains(string(p), "Verbosity")
}

func fieldAt(v reflect.Value, p FieldPath) reflect.Value {
	for name := range strings.SplitSeq(string(p), ".") {
		v = v.FieldByName(name)
	}
	return v
}

// argvEquivalent compares a written value with the one read back: nil and empty collections
// match, pointers by pointee, times with Equal, NaN equals NaN, and free-form maps through JSON.
func argvEquivalent(a, b reflect.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
	switch v := a.Interface().(type) {
	case time.Time:
		return v.Equal(b.Interface().(time.Time))
	case *url.URL, *time.Location, *regexp.Regexp:
		return a.IsNil() == b.IsNil() && (a.IsNil() || a.Interface().(interface{ String() string }).String() == b.Interface().(interface{ String() string }).String())
	case map[string]any:
		ja, _ := json.Marshal(v)
		jb, _ := json.Marshal(b.Interface())
		var na, nb any
		_ = json.Unmarshal(ja, &na)
		_ = json.Unmarshal(jb, &nb)
		return reflect.DeepEqual(na, nb)
	}
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return argvEquivalent(a.Elem(), b.Elem())
	case reflect.Float32, reflect.Float64:
		x, y := a.Float(), b.Float()
		return x == y || (math.IsNaN(x) && math.IsNaN(y))
	case reflect.Slice:
		if a.Len() != b.Len() {
			return false
		}
		for i := range a.Len() {
			if !argvEquivalent(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.Len() != b.Len() {
			return false
		}
		for _, k := range a.MapKeys() {
			bv := b.MapIndex(k)
			if !bv.IsValid() || !argvEquivalent(a.MapIndex(k), bv) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

func TestArgvOfPassthrough(t *testing.T) {
	t.Run("a passthrough command takes its words raw", func(t *testing.T) {
		var in argvShInputs
		in.App.Flags.Verbose = true
		in.Sh.Arguments.Words = []string{"--", "-x", "ls"}
		argv, _, err := ArgvOf(argvDef(), in, PresenceOf(in))
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"--verbose", "sh", "--", "-x", "ls"}; !slices.Equal(argv, want) {
			t.Fatalf("argv = %q, want %q", argv, want)
		}
		argvRoundTrip(t, argvDef(), in, PresenceOf(in), argv, nil)
	})
	t.Run("a passthrough argument's words follow the dash", func(t *testing.T) {
		var in argvExecInputs
		in.Exec.Flags.Dry = true
		in.Exec.Arguments.Name = "cmd"
		in.Exec.Arguments.Rest = []string{"--", "--dry", "a,b"}
		argv, _, err := ArgvOf(argvDef(), in, PresenceOf(in))
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"exec", "--dry", "--", "cmd", "--", "--dry", "a,b"}; !slices.Equal(argv, want) {
			t.Fatalf("argv = %q, want %q", argv, want)
		}
		argvRoundTrip(t, argvDef(), in, PresenceOf(in), argv, nil)
	})
	t.Run("a passthrough root", func(t *testing.T) {
		type rootInputs struct{ App argvShCI }
		def := Definition{Name: "app", Passthrough: true, Arguments: []ArgDef{{Name: "words", Type: "[]string", Variadic: true}}}
		var in rootInputs
		in.App.Arguments.Words = []string{"--help", "x"}
		argv, _, err := ArgvOf(def, in, PresenceOf(in), ArgvPath())
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"--help", "x"}; !slices.Equal(argv, want) {
			t.Fatalf("argv = %q, want %q", argv, want)
		}
		argvRoundTrip(t, def, in, PresenceOf(in), argv, nil)
	})
}

func TestArgvOfFindsTheCommand(t *testing.T) {
	t.Run("the root's own type", func(t *testing.T) {
		var in argvAppInputs
		in.App.Arguments.Word = "run"
		argv, _, err := ArgvOf(argvDef(), in, PresenceOf(in))
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"--", "run"}; !slices.Equal(argv, want) {
			t.Fatalf("argv = %q, want %q", argv, want)
		}
		argvRoundTrip(t, argvDef(), in, PresenceOf(in), argv, nil)
	})
	t.Run("a composed child's type on its parent", func(t *testing.T) {
		// mono build image, where image's type covers only the child cli's frames.
		type buildCI struct {
			Flags struct {
				Push bool `rotini:"push"`
			}
		}
		type imageCI struct {
			Flags struct {
				Tag string `rotini:"tag"`
			}
		}
		type buildImageInputs struct {
			Build      buildCI
			BuildImage imageCI
		}
		def := Definition{Name: "mono", Commands: []CommandDef{{
			Name: "build", Flags: []FlagDef{{Name: "push", Identifiers: []string{"--push"}, Type: "bool"}},
			Commands: []CommandDef{{
				Name: "image", Inputs: reflect.TypeFor[buildImageInputs](),
				Flags: []FlagDef{{Name: "tag", Identifiers: []string{"--tag"}, Type: "string"}},
			}},
		}}}
		var in buildImageInputs
		in.Build.Flags.Push = true
		in.BuildImage.Flags.Tag = "v1"
		argv, _, err := ArgvOf(def, in, PresenceOf(in))
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"build", "--push", "image", "--tag=v1"}; !slices.Equal(argv, want) {
			t.Fatalf("argv = %q, want %q", argv, want)
		}
		argvRoundTrip(t, def, in, PresenceOf(in), argv, nil)
	})
	t.Run("a hand-built Definition with ArgvPath", func(t *testing.T) {
		def := argvDef()
		def.Commands[0].Inputs = nil
		in, set := argvRun(func(c *argvRunCI) { c.Flags.Count = 1 }, "Run.Flags.Count")
		if _, _, err := ArgvOf(def, in, set); err == nil || !strings.Contains(err.Error(), "ArgvPath") {
			t.Fatalf("err = %v, want one pointing at ArgvPath", err)
		}
		argv, _, err := ArgvOf(def, in, set, ArgvPath("r"))
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"run", "--count=1"}; !slices.Equal(argv, want) {
			t.Fatalf("argv = %q, want %q", argv, want)
		}
	})
	t.Run("wiring mistakes are not ArgvErrors", func(t *testing.T) {
		in, set := argvRun(func(*argvRunCI) {})
		for _, tc := range []struct {
			name string
			err  error
		}{
			{"unknown path", second(ArgvOf(argvDef(), in, set, ArgvPath("nope")))},
			{"the wrong command", second(ArgvOf(argvDef(), in, set, ArgvPath("sh")))},
			{"too deep", second(ArgvOf(Definition{Name: "x"}, in, set, ArgvPath()))},
			{"not a struct", second(ArgvOf(argvDef(), 3, nil, ArgvPath()))},
			{"a path in set naming no field", second(ArgvOf(argvDef(), in, Presence{"Run.Flags.Nope": {}}))},
		} {
			var ae *ArgvError
			if tc.err == nil || errors.As(tc.err, &ae) {
				t.Errorf("%s: err = %v, want a wiring error", tc.name, tc.err)
			}
		}
	})
}

func second(_, _ []string, err error) error { return err }

func TestProgramDefinition(t *testing.T) {
	def := argvDef()
	if got := NewProgram(def, nil).Definition(); got.Name != "app" || got.Inputs != reflect.TypeFor[argvAppInputs]() {
		t.Fatalf("Definition() = %q %v", got.Name, got.Inputs)
	}
}

// argvFuzzPaths are the inputs FuzzArgvOf may put in set, one mask bit each.
var argvFuzzPaths = []string{
	"Run.Flags.Help", "Run.Flags.Color", "Run.Flags.Plain", "Run.Flags.Count", "Run.Flags.Verbosity",
	"Run.Flags.Mode", "Run.Flags.Tags", "Run.Flags.Labels", "Run.Flags.Ports", "Run.Flags.Env",
	"Run.Flags.Envs", "Run.Flags.Set", "Run.Flags.Values", "Run.Flags.DB", "Run.Flags.DBs",
	"Run.Flags.Limit", "Run.Flags.When", "Run.Flags.Date", "Run.Flags.Unix", "Run.Flags.Milli",
	"Run.Flags.Custom", "Run.Flags.Wait", "Run.Flags.Size", "Run.Flags.Hex", "Run.Flags.B64",
	"Run.Flags.Body", "Run.Flags.Token", "Run.Flags.New", "Run.Flags.Ratio", "Run.Flags.Short",
	"Run.Flags.Shout", "Run.Arguments.Target", "Run.Arguments.Files", "Run.Env.Token",
	"Run.Env.Names", "Run.Env.Since", "Run.Env.HTTP", "App.Flags.Verbose", "App.Flags.Out",
	"Run.Flags.URL", "Run.Flags.Mail", "Run.Flags.Loc", "Run.Flags.MAC", "Run.Flags.IP",
	"Run.Flags.Quiet", "Run.Flags.Tint", "Run.Flags.Day", "Run.Flags.Ago",
	"Run.Flags.Match", "Run.Flags.Glob", "Run.Flags.Level", "Run.Flags.In", "Run.Flags.OutF",
	"Run.Flags.Dates", "Run.Env.Paths",
}

// argvFuzzZones are the time zones FuzzArgvOf picks from.
var argvFuzzZones = []string{"UTC", "Europe/Berlin", "America/New_York", "Asia/Kolkata"}

// FuzzArgvOf checks that ArgvOf's output parses back to the value it was given: either ArgvOf
// reports an *ArgvError, or argv and env read back every field in set, and only those.
func FuzzArgvOf(f *testing.F) {
	f.Add("hello", "key", int64(3), uint64(7), 1.5, true, int64(1759104000), ^uint64(0))
	f.Add("a,b", "", int64(-1), uint64(0), math.Inf(1), false, int64(0), uint64(0b1010101010101))
	f.Add(`say "hi"`, "x=y", int64(0), uint64(1<<40), math.NaN(), true, int64(-86400), uint64(1<<31|1<<5))
	f.Add("@file", " lead", int64(36*3600*1e9), uint64(512<<20), math.Copysign(0, -1), false, int64(253402300800), uint64(1<<25|1<<26))
	f.Add("-", "true", int64(math.MinInt64), uint64(math.MaxUint64), 1e300, true, int64(math.MaxInt64), uint64(1<<12|1<<11))
	f.Add("--x", "child", int64(9), uint64(3), 0.1, false, int64(1), uint64(1<<31|1<<32))
	f.Add("\f", "0", int64(9), uint64(3), 0.1, false, int64(1), uint64(1<<31|1<<32))
	f.Add(" ", "0", int64(-55), uint64(0), 1.5, true, int64(1759104077), uint64(1<<34))
	f.Add("\xeb", "0", int64(30), uint64(0), 1.5, true, int64(1759104095), uint64(1<<13))
	f.Add("^a+$", "dir/*.go", int64(7), uint64(0x0102030405060708), 2.5, false, int64(1759104000), uint64(0x01ff)<<39)
	f.Add("@@x", "a:b", int64(-7), uint64(1), 0.5, true, int64(-1), uint64(0xffff)<<39|1<<36)
	inPath := filepath.Join(f.TempDir(), "in.txt")
	if err := os.WriteFile(inPath, []byte("x"), 0o600); err != nil {
		f.Fatal(err)
	}
	outPath := filepath.Join(filepath.Dir(inPath), "out.txt")
	f.Fuzz(func(t *testing.T, s, k string, n int64, u uint64, x float64, b bool, ts int64, mask uint64) {
		var in argvRunInputs
		r := &in.Run
		r.Flags = argvRunFlags{
			Help: b, Color: !b, Plain: b, Count: int(n), Verbosity: int(u % 5), Mode: s,
			Tags: []string{s, k}, Labels: []string{k, s}, Ports: []int{int(n)},
			Env: map[string]string{k: s}, Envs: map[string]string{s: k},
			Set:    map[string]any{k: s, "n": x, "b": b},
			Values: map[string]any{k: map[string]any{s: s}},
			DB:     argvDB{Host: s, Port: int(n)}, DBs: []argvDB{{Host: k}},
			Limit: new(int(n)),
			When:  time.Unix(ts, n%1e9).UTC(), Date: time.Unix(ts-ts%86400, 0).UTC(),
			Unix: time.Unix(ts, int64(u%1e9)), Milli: time.UnixMilli(ts),
			Custom: time.Unix(ts-ts%60, 0).UTC(), Wait: time.Duration(n),
			Size: ByteSize(u >> 1), Hex: HexBytes(s), B64: Base64Bytes(k),
			Body: s, Token: k, New: s, Ratio: x, Short: k, Shout: argvShout(strings.ToUpper(s)),
			URL:  &url.URL{Scheme: "https", Host: "example.com", Path: "/" + s, RawQuery: url.QueryEscape(k)},
			Mail: mail.Address{Name: s, Address: "ada@example.com"},
			MAC:  binary.BigEndian.AppendUint64(nil, u)[:6], IP: netip.AddrFrom4([4]byte(binary.BigEndian.AppendUint32(nil, uint32(u)))),
			Quiet: int(u % 3), Tint: b, Day: time.Unix(ts-ts%86400, 0).UTC(), Ago: time.Unix(ts, n%1e9).UTC(),
			Glob: "*.go", Level: []string{"low", "high"}[u%2], In: inPath, OutF: outPath,
			Dates: map[string]time.Time{k: time.Unix(ts-ts%86400, 0).UTC()},
		}
		r.Flags.Loc, _ = time.LoadLocation(argvFuzzZones[u%uint64(len(argvFuzzZones))])
		if re, err := regexp.Compile(s); err == nil {
			r.Flags.Match = re
		}
		if _, err := path.Match(k, ""); err == nil {
			r.Flags.Glob = Glob(k)
		}
		if b {
			r.Flags.In, r.Flags.OutF = "-", "-"
		}
		r.Arguments = argvRunArgs{Target: s, Files: []string{k, s}}
		r.Env = argvRunEnv{Token: s, Names: []string{s, k}, Since: time.Unix(ts-ts%86400, 0).UTC(),
			HTTP: map[string]any{k: s}, Paths: []string{s, k}}
		in.App.Flags = argvAppFlags{Verbose: b, Out: s}
		set := Presence{}
		for i, p := range argvFuzzPaths {
			if mask&(1<<i) != 0 {
				set[FieldPath(p)] = InputSource{Layer: "custom"}
			}
		}
		argvFuzzPassthrough(t, filepath.Dir(inPath), s, k)
		argv, env, err := ArgvOf(argvDef(), in, set, ArgvSecrets())
		if err != nil {
			if _, ok := errors.AsType[*ArgvError](err); !ok {
				t.Fatalf("ArgvOf: %v", err)
			}
			return
		}
		argvRoundTrip(t, argvDef(), in, set, argv, env)
	})
}

// argvFuzzPassthrough round-trips raw words, on a passthrough command and a passthrough
// argument, with response files on: what a run in dir would expand must read back as written.
func argvFuzzPassthrough(t *testing.T, dir, s, k string) {
	t.Helper()
	def := argvDef()
	def.ResponseFiles = &ResponseFilesDef{Prefix: "@"}
	view := NewContextFor(def, nil).WithDir(dir).view
	check := func(in any, argv []string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("ArgvOf: %v", err)
		}
		expanded, err := expandResponseFiles(argv, "@", view, false)
		if err != nil {
			t.Fatalf("%q expands: %v", argv, err)
		}
		switch in := in.(type) {
		case argvShInputs:
			argvRoundTrip(t, def, in, PresenceOf(in), expanded, nil)
		case argvExecInputs:
			argvRoundTrip(t, def, in, PresenceOf(in), expanded, nil)
		}
	}
	var sh argvShInputs
	sh.Sh.Arguments.Words = []string{s, "@" + k, k, "--", "@" + s}
	argv, _, err := ArgvOf(def, sh, PresenceOf(sh))
	check(sh, argv, err)

	var ex argvExecInputs
	ex.Exec.Arguments.Name = "@" + s
	ex.Exec.Arguments.Rest = []string{"@" + k, s}
	argv, _, err = ArgvOf(def, ex, PresenceOf(ex))
	check(ex, argv, err)
}
