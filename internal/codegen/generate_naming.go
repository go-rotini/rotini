package codegen

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// This file holds naming helpers: Go identifier casing, import rendering, and stub file names.

// fieldImport returns the Go import path a field's type needs: the explicit spec `import:`,
// else the import a built-in type alias requires (see builtinImport), else "". For an array,
// the element's `import:` or built-in type is used.
func fieldImport(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	if imp := strings.TrimSpace(schema.Import); imp != "" {
		return imp
	}
	if schema.Items != nil && jsonSchemaTypeToGo(schema.Type) == "[]string" {
		if imp := strings.TrimSpace(schema.Items.Import); imp != "" {
			return imp
		}
		return builtinImport(schema.Items.Type)
	}
	return builtinImport(schema.Type)
}

// builtinImport returns the standard-library import a rotini type alias requires, or "" when
// none is needed. rotini's own types (bytesize, hexbytes, base64bytes) use the runtime import.
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

// parseAliasPath splits an "alias path" import (an input's `import:` or a command's
// `handler.import`) into alias and path. A bare path derives its alias from the last segment
// via identAlias.
func parseAliasPath(imp string) (alias, importPath string) {
	imp = strings.TrimSpace(imp)
	if a, p, ok := strings.Cut(imp, " "); ok {
		return strings.TrimSpace(a), strings.TrimSpace(p)
	}
	return identAlias(filepath.Base(imp)), imp
}

// renderImports turns a set of import values into sorted Go import specs: "path" becomes
// `"path"` and "alias path" becomes `alias "path"`.
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

// toPascalCase converts a name to PascalCase, treating '-', '_' and ' ' as word boundaries
// ("foo_bar" -> "FooBar") and capitalizing Go initialisms ("api-resources" -> "APIResources",
// "base-url" -> "BaseURL", "apiKey" -> "APIKey").
func toPascalCase(s string) string {
	var b strings.Builder
	for _, word := range strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == ' ' }) {
		r := []rune(word)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(initialismCase(string(r)))
	}
	return b.String()
}

// initialismCase capitalizes the initialism words of a PascalCase identifier: "ApiVersion" ->
// "APIVersion", "HttpGetUrl" -> "HTTPGetURL". Words split where a non-capital meets a capital,
// so a run of capitals is one word.
func initialismCase(name string) string {
	r := []rune(name)
	var b strings.Builder
	start := 0
	for i := 1; i <= len(r); i++ {
		if i < len(r) && (!unicode.IsUpper(r[i]) || unicode.IsUpper(r[i-1])) {
			continue
		}
		word := string(r[start:i])
		if upper := strings.ToUpper(word); goInitialisms[upper] {
			word = upper
		}
		b.WriteString(word)
		start = i
	}
	return b.String()
}

// goInitialisms are the initialisms written in capitals inside identifiers (golint's
// commonInitialisms).
var goInitialisms = map[string]bool{
	"ACL": true, "API": true, "ASCII": true, "CPU": true, "CSS": true, "DNS": true, "EOF": true,
	"GUID": true, "HTML": true, "HTTP": true, "HTTPS": true, "ID": true, "IP": true, "JSON": true,
	"LHS": true, "QPS": true, "RAM": true, "RHS": true, "RPC": true, "SLA": true, "SMTP": true,
	"SQL": true, "SSH": true, "TCP": true, "TLS": true, "TTL": true, "UDP": true, "UI": true,
	"UID": true, "UUID": true, "URI": true, "URL": true, "UTF8": true, "VM": true, "XML": true,
	"XMPP": true, "XSRF": true, "XSS": true,
}

// lowerFirst returns s as an unexported identifier, lower-casing its first rune or its whole
// leading capital run: "APIResources" -> "apiResources", "URL" -> "url".
func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	n := 0
	for n < len(r) && unicode.IsUpper(r[n]) {
		n++
	}
	switch {
	case n <= 1:
		n = 1
	case n < len(r) && unicode.IsLower(r[n]):
		n-- // the last capital starts the next word: "APIResources" keeps the R
	}
	for i := range n {
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

// goReservedFilenames are the trailing "_"-separated file name tokens the go tool treats
// specially: "test", and the GOOS and GOARCH names, which imply a build constraint.
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

// reservedTrailingToken reports whether stem's trailing "_"-separated token is in
// goReservedFilenames.
func reservedTrailingToken(stem string) bool {
	parts := strings.Split(stem, "_")
	return goReservedFilenames[parts[len(parts)-1]]
}

// stubFilename builds a handler-stub file name from base. A reserved trailing token gets a
// trailing underscore ("app_test_.go"), so a command named "test" or "windows" still builds
// on every platform.
func stubFilename(base string) string {
	if reservedTrailingToken(base) {
		base += "_"
	}
	return base + ".go"
}

// commandStubFilename returns a command's stub file name: its `filename` override, else
// "<root>[_<path>].go" with '-' written as '_' ("config_get_contexts.go"). lintHandlerFilenames
// shares this derivation, so it reports commands differing only by '-' versus '_' as a clash.
func commandStubFilename(rootName, path, override string) string {
	if override != "" {
		return override
	}
	base := rootName
	if path != "" {
		base += "_" + path
	}
	return stubFilename(strings.ReplaceAll(base, "-", "_"))
}

// dashedStubFilename returns the legacy stub name that kept '-' ("config_get-contexts.go"),
// or "" when it equals the current name. An existing stub under the legacy name is still the
// command's handler, so codegen neither seeds a duplicate nor prunes it; see stubFileFor.
func dashedStubFilename(rootName, path, override string) string {
	if override != "" {
		return ""
	}
	base := rootName
	if path != "" {
		base += "_" + path
	}
	if old := stubFilename(base); old != commandStubFilename(rootName, path, override) {
		return old
	}
	return ""
}

// stubFileFor returns the file name of c's stub in dir: the legacy dashed name when only that
// file exists, else the current name.
func stubFileFor(dir string, c genCommand) string {
	if c.dashedFilename == "" {
		return c.filename
	}
	if _, err := os.Stat(filepath.Join(dir, c.filename)); err == nil {
		return c.filename
	}
	if _, err := os.Stat(filepath.Join(dir, c.dashedFilename)); err == nil {
		return c.dashedFilename
	}
	return c.filename
}
