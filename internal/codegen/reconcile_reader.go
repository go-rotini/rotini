package codegen

// Reading inputs: spec and conf decoding (format chosen by extension), conversion to JSON
// for schema validation, spec and conf discovery, and module resolution.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

type fileType string

const (
	fileTypeSpec fileType = "spec"
	fileTypeConf fileType = "conf"
)

// fileFormat is a supported spec/conf serialization. Its value is also the canonical file
// extension.
type fileFormat string

const (
	formatUnknown fileFormat = ""
	formatYAML    fileFormat = "yaml"
	formatJSON    fileFormat = "json"
	formatJSONC   fileFormat = "jsonc"
	formatTOML    fileFormat = "toml"
)

var errUnsupportedFormat = errors.New("unsupported file format")

// detectFileFormat maps a path's extension to its serialization format.
func detectFileFormat(path string) fileFormat {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return formatYAML
	case ".json":
		return formatJSON
	case ".jsonc":
		return formatJSONC
	case ".toml":
		return formatTOML
	default:
		return formatUnknown
	}
}

// readRaw detects path's format from its extension and reads the file, erroring on an
// unknown extension or a read failure.
func readRaw(path string) (fileFormat, []byte, error) {
	format := detectFileFormat(path)
	if format == formatUnknown {
		return formatUnknown, nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	if data, ok := seedOverlayRead(path); ok {
		return format, data, nil
	}
	noteRead(path)
	data, err := fs.ReadFile(path)
	if err != nil {
		// The fs error repeats the path; report a missing file plainly.
		if errors.Is(err, os.ErrNotExist) {
			return format, nil, fmt.Errorf("%s: %w", path, os.ErrNotExist)
		}
		return format, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return format, data, nil
}

// readLog collects the files read while recordReads runs, so watch mode can watch every file
// a pass depends on. paths is nil while nothing records.
var readLog struct {
	mu    sync.Mutex
	paths map[string]fileStamp
}

// fileStamp is what a file looked like when a pass read it, so watch mode can tell whether it
// changed before its watcher started.
type fileStamp struct {
	exists  bool
	size    int64
	modTime time.Time
}

// stampFile stats path; a file that can't be stat'ed stamps as missing.
func stampFile(path string) fileStamp {
	fi, err := fs.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: fi.Size(), modTime: fi.ModTime()}
}

// noteRead adds path to the read log when one is recording, stamped as it is now. Call it
// before reading the file, so a change made after the stamp is seen as a change.
func noteRead(path string) {
	readLog.mu.Lock()
	defer readLog.mu.Unlock()
	if readLog.paths == nil {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if _, ok := readLog.paths[path]; ok {
		return
	}
	readLog.paths[path] = stampFile(path)
}

// recordReads runs fn and returns the files read through readRaw or noteRead meanwhile, each
// stamped as it was when first read.
func recordReads(fn func()) map[string]fileStamp {
	readLog.mu.Lock()
	readLog.paths = map[string]fileStamp{}
	readLog.mu.Unlock()
	fn()
	readLog.mu.Lock()
	defer readLog.mu.Unlock()
	paths := readLog.paths
	readLog.paths = nil
	return paths
}

// readFile reads and decodes the file at path into a T, choosing the decoder by extension.
// Every decoder honors the json struct tags on the generated Spec and Conf types.
func readFile[T any](path string) (*T, error) {
	format, data, err := readRaw(path)
	if err != nil {
		return nil, err
	}
	return decodeData[T](format, data, path)
}

// decodeData decodes one read document into a T and normalizes it. path labels a failure.
// Loaders call it directly so one read feeds both the decoded value and bytesToJSON.
func decodeData[T any](format fileFormat, data []byte, path string) (*T, error) {
	out := new(T)
	if err := checkDuplicateKeys(docKind(out), format, data, path); err != nil {
		return nil, err
	}
	var err error
	switch format {
	case formatYAML:
		err = yaml.Unmarshal(data, out)
	case formatJSON:
		err = decodeJSONNumbers(data, out)
	case formatJSONC:
		err = jsonc.UnmarshalWithOptions(data, out, jsonc.WithUseNumber())
	case formatTOML:
		err = toml.Unmarshal(data, out)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, path)
	}
	if err != nil {
		if dup := tomlDuplicateKey(docKind(out), err, path); dup != nil {
			return nil, dup
		}
		return nil, &decodeError{path: path, format: format, data: data, err: err}
	}
	if n, ok := any(out).(normalizer); ok {
		n.normalize()
	}
	return out, nil
}

// decodeJSONNumbers decodes one JSON document into out, keeping numbers as json.Number so an
// integer default keeps its exact spelling instead of passing through float64.
func decodeJSONNumbers(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid character after top-level value")
	}
	return nil
}

// decodeError reports a document that read but did not decode into rotini's Go types,
// usually a value of the wrong type. It carries the raw bytes so the Processor can run the
// schema validator instead, whose positioned message is clearer than the decoder's.
type decodeError struct {
	path   string
	format fileFormat
	data   []byte
	err    error
}

func (e *decodeError) Error() string { return fmt.Sprintf("decode %s: %v", e.path, e.err) }
func (e *decodeError) Unwrap() error { return e.err }

// bytesToJSON converts a document in any supported format to JSON for the schema validator.
// Converting the raw document, not the decoded struct, keeps unknown fields visible.
func bytesToJSON(format fileFormat, data []byte) ([]byte, error) {
	var (
		out []byte
		err error
	)
	switch format {
	case formatJSON:
		return data, nil
	case formatJSONC:
		out, err = jsonc.ToJSON(data)
	case formatYAML:
		out, err = yaml.ToJSON(data)
	case formatTOML:
		out, err = toml.ToJSON(data)
	default:
		return nil, errUnsupportedFormat
	}
	if err != nil {
		return nil, fmt.Errorf("convert %s to json: %w", format, err)
	}
	return out, nil
}

// nonFiniteNumber reports the first infinite or NaN number in a YAML or TOML document, at its
// position, or nil when there is none. JSON has no spelling for those values, so the document
// can't be converted for schema validation.
func nonFiniteNumber(path string, format fileFormat, data []byte) error {
	var doc any
	var derr error
	switch format {
	case formatYAML:
		derr = yaml.Unmarshal(data, &doc)
	case formatTOML:
		derr = toml.Unmarshal(data, &doc)
	default:
		return nil
	}
	if derr != nil {
		return nil //nolint:nilerr // a document that doesn't decode has no number to point at
	}
	ptr, ok := findNonFinite(doc, "")
	if !ok {
		return nil
	}
	// Place it at the value, or at the nearest enclosing key the locator resolves.
	where, locate := path, newSourceLocator(format, data)
	for p := ptr; p != ""; p = p[:strings.LastIndex(p, "/")] {
		if line, col, ok := locate(p); ok {
			where = fmt.Sprintf("%s:%d:%d", path, line, col)
			break
		}
	}
	return fmt.Errorf("%s: %s is infinite or not a number, which a spec can't hold; write a finite number, or quote the value to mean the text", where, ptr)
}

// findNonFinite returns the JSON pointer of the first infinite or NaN float64 in v, walking
// map keys in sorted order.
func findNonFinite(v any, ptr string) (string, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return ptr, true
		}
	case []any:
		for i, e := range x {
			if p, ok := findNonFinite(e, ptr+"/"+strconv.Itoa(i)); ok {
				return p, true
			}
		}
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			key := strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
			if p, ok := findNonFinite(x[k], ptr+"/"+key); ok {
				return p, true
			}
		}
	}
	return "", false
}

// readSpec reads and decodes the spec at path without schema validation.
func readSpec(path string) (*Spec, error) {
	return readFile[Spec](path)
}

// readConf reads and decodes the conf at path without schema validation.
func readConf(path string) (*Conf, error) {
	return readFile[Conf](path)
}

// errSpecPathRequired is returned when no spec path is given and none is discovered.
var errSpecPathRequired = errors.New("no .rotini.spec.* file in the working directory; pass the spec's path")

// fallbackExtensions is the discovery precedence: the first .rotini.<type>.<ext> that exists
// wins.
var fallbackExtensions = []string{"yml", "yaml", "toml", "json", "jsonc"}

// discoverFile returns the first existing .rotini.<ft>.<ext> in dir in fallbackExtensions
// order, or ("", false).
func discoverFile(dir string, ft fileType) (string, bool) {
	return fs.FindWithExtensions(dir, ".rotini."+string(ft), fallbackExtensions)
}

// resolveSpecPath returns path when set, otherwise the first .rotini.spec.* in the working
// directory, or "" when none is found.
func resolveSpecPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	found, _ := discoverFile(cwd, fileTypeSpec)
	return found, nil
}

// ResolvePaths resolves the spec and conf paths the pipeline would read. An empty specPath
// discovers the first .rotini.spec.* in the working directory; an empty confPath discovers
// the first .rotini.conf.* beside the spec, and conf is "" when there is none. Given paths
// are returned unchanged and unchecked; discovered paths are made relative for display.
func ResolvePaths(specPath, confPath string) (spec, conf string, err error) {
	spec, err = resolveSpecPath(specPath)
	if err != nil {
		return "", "", err
	}
	if spec == "" {
		return "", "", errSpecPathRequired
	}
	conf = resolveConfBesideSpec(spec, confPath)
	if specPath == "" {
		spec = displayPath(spec)
	}
	if confPath == "" && conf != "" {
		conf = displayPath(conf)
	}
	return spec, conf, nil
}

// resolveConfBesideSpec returns confPath when set, otherwise the first .rotini.conf.* beside
// the spec, or "" when none is found.
func resolveConfBesideSpec(specPath, confPath string) string {
	if confPath != "" {
		return confPath
	}
	found, _ := discoverFile(filepath.Dir(specPath), fileTypeConf)
	return found
}

// discoverConf returns the first .rotini.conf.* in dir, or an error when none exists.
func discoverConf(dir string) (string, error) {
	if found, ok := discoverFile(dir, fileTypeConf); ok {
		return found, nil
	}
	return "", fmt.Errorf("no .rotini.%s.* file found in %s", fileTypeConf, dir)
}

// findModule walks up from the working directory to the nearest go.mod and returns the
// module root directory and module path. A directory that keeps its module in vendor.mod, and
// holds no go.mod, is found by its vendor.mod.
func findModule() (root, name string, err error) {
	root, name, _, err = findModuleFile()
	return root, name, err
}

// findModuleFile is findModule that also returns the file the module was read from: go.mod,
// or vendor.mod in a directory with no go.mod.
func findModuleFile() (root, name, file string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", "", fmt.Errorf("get working directory: %w", err)
	}
	goMod := ""
	for d := dir; goMod == ""; {
		for _, name := range []string{"go.mod", "vendor.mod"} {
			if info, err := os.Stat(filepath.Join(d, name)); err == nil && !info.IsDir() {
				goMod = filepath.Join(d, name)
				break
			}
		}
		parent := filepath.Dir(d)
		if goMod == "" && parent == d {
			return "", "", "", errors.New("go.mod not found in any parent of working directory")
		}
		d = parent
	}
	data, err := os.ReadFile(goMod)
	if err != nil {
		return "", "", "", fmt.Errorf("read %s: %w", goMod, err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "module "); ok {
			return filepath.Dir(goMod), strings.TrimSpace(after), goMod, nil
		}
	}
	return "", "", "", fmt.Errorf("no module path in %s", goMod)
}
