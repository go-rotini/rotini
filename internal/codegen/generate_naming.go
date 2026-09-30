package codegen

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Generic naming helpers: Go casing, import-path rendering, and reserved-filename
// handling for generated identifiers, packages, and stub files.

// fieldImport returns the Go import path backing a field's schema: the explicit
// spec `import:` when set, otherwise the import rotini knows is needed for its own
// built-in type aliases (duration/time/datetime/date → "time"). "" means no import.
func fieldImport(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	if imp := strings.TrimSpace(schema.Import); imp != "" {
		return imp
	}
	// An array's element type carries the import: explicit items.import first,
	// then the built-in vocabulary (items duration → "time").
	if schema.Items != nil && jsonSchemaTypeToGo(schema.Type) == "[]string" {
		if imp := strings.TrimSpace(schema.Items.Import); imp != "" {
			return imp
		}
		return builtinImport(schema.Items.Type)
	}
	return builtinImport(schema.Type)
}

// builtinImport returns the import path rotini's own type vocabulary requires, or
// "" when the type needs none. The rotini-defined types (bytesize, hexbytes, base64bytes)
// need no entry: the generated file already imports the runtime.
func builtinImport(rotiniType string) string {
	if elem, ok := strings.CutPrefix(rotiniType, "[]"); ok {
		return builtinImport(elem)
	}
	if _, val, ok := splitMapType(rotiniType); ok {
		return builtinImport(val)
	}
	switch rotiniType {
	case "duration", "time", "datetime", "date", "timezone":
		return "time"
	case "url":
		return "net/url"
	case "email":
		return "net/mail"
	case "mac":
		return "net"
	case "ip", "cidr", "hostport":
		return "net/netip"
	}
	return ""
}

// parseAliasPath splits the `alias path` external-Go-binding form (shared by an input
// type's `import:` and a command's `handler.import`) into its alias and path;
// a bare path derives its alias from the last segment.
func parseAliasPath(imp string) (alias, importPath string) {
	imp = strings.TrimSpace(imp)
	if a, p, ok := strings.Cut(imp, " "); ok {
		return strings.TrimSpace(a), strings.TrimSpace(p)
	}
	return identAlias(filepath.Base(imp)), imp
}

// renderImports turns a set of spec `import:` values into sorted Go import specs:
// a plain path becomes "path"; the aliased form "alias path" becomes alias "path".
func renderImports(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for imp := range set {
		if alias, path, ok := strings.Cut(imp, " "); ok {
			out = append(out, alias+" "+strconv.Quote(strings.TrimSpace(path)))
		} else {
			out = append(out, strconv.Quote(imp))
		}
	}
	sort.Strings(out)
	return out
}

// toPascalCase converts a name to PascalCase, treating '-', '_' and ' ' as word
// boundaries (e.g. "foo_bar" -> "FooBar", "generate" -> "Generate").
func toPascalCase(s string) string {
	var b strings.Builder
	capitalize := true
	for _, r := range s {
		if r == '-' || r == '_' || r == ' ' {
			capitalize = true
			continue
		}
		if capitalize {
			b.WriteRune(unicode.ToUpper(r))
			capitalize = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// lowerFirst returns s with its first rune lower-cased.
func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// goReservedFilenames are the trailing "_"-separated tokens the go tool reads specially from a
// file's name alone: "test", and the GOOS and GOARCH names, which imply a build constraint.
// One set, since stubFilename needs membership rather than which rule matched.
var goReservedFilenames = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range []string{
		"test",
		// GOOS
		"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos",
		"ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris",
		"wasip1", "windows", "zos",
		// GOARCH
		"386", "amd64", "amd64p32", "arm", "arm64", "arm64be", "armbe", "loong64",
		"mips", "mips64", "mips64le", "mips64p32", "mips64p32le", "mipsle", "ppc",
		"ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64",
		"wasm",
	} {
		m[s] = true
	}
	return m
}()

// reservedTrailingToken reports whether stem's trailing "_"-separated token is one the
// go tool reads specially from a file's name — "test" (a "_test.go" test file) or a
// GOOS/GOARCH (an implicit build constraint).
func reservedTrailingToken(stem string) bool {
	parts := strings.Split(stem, "_")
	return goReservedFilenames[parts[len(parts)-1]]
}

// stubFilename builds a handler-stub file name from base, escaping the names the go tool would
// read specially by appending a trailing underscore. That makes the trailing "_"-separated
// token empty, which matches no rule, so a command named "test" or "windows" still compiles
// into the ordinary build.
func stubFilename(base string) string {
	if reservedTrailingToken(base) {
		base += "_"
	}
	return base + ".go"
}

// commandStubFilename returns a command's stub file name: its explicit `filename` override, or
// the derived "<root>[_<path>].go". Codegen and lintHandlerFilenames share this derivation, so
// naming and uniqueness validation agree.
func commandStubFilename(rootName, path, override string) string {
	if override != "" {
		return override
	}
	base := rootName
	if path != "" {
		base += "_" + path
	}
	return stubFilename(base)
}
