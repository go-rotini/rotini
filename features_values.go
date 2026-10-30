package rotini

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"time"
)

// Bounds wires numeric, length and item-count bounds.
func Bounds() Feature {
	return Feature{name: "bounds", apply: func(s *featureSet) {
		s.bounds = &boundHooks{items: checkItemCount, value: checkValueBounds}
	}}
}

// UniqueItems wires [Constraints.UniqueItems].
func UniqueItems() Feature {
	return Feature{name: "unique-items", apply: func(s *featureSet) {
		s.unique = &uniqueHooks{
			values:    checkUniqueItems,
			key:       uniqueKeyFor,
			typed:     checkUniqueTyped,
			documents: duplicateDocument,
		}
	}}
}

// checkValueBounds enforces the numeric or length bounds on one value of element type elem,
// read under base when it is an integer.
func checkValueBounds(label, elem, base string, c Constraints, v string, secret bool) error {
	switch {
	case isNumericType(elem) && base == basePrefixed:
		return checkNumericBounds(label, c, v, integerText(elem, prefixedText(v)), secret, prefixedNumber, formatNum)
	case isNumericType(elem):
		return checkNumericBounds(label, c, v, integerText(elem, v), secret, parseNumber, formatNum)
	case measuredFor(elem) != nil:
		m := measuredFor(elem)
		return checkNumericBounds(label, c, v, "", secret, m.parse, m.format)
	case isPathType(elem), elem == "string":
		return checkLengthBounds(label, c, v, secret)
	}
	return nil
}

// integerText is v, a value of numeric type elem in decimal, when elem is an integer type, so
// its bounds compare it exactly; "" for a float.
func integerText(elem, v string) string {
	switch elem {
	case "float32", "float64", "number":
		return ""
	}
	return v
}

// Patterns wires pattern constraints.
func Patterns() Feature {
	return Feature{name: "patterns", apply: func(s *featureSet) {
		s.patterns = &patternHooks{match: func(pattern, v string) (bool, error) {
			re, err := compiledPattern(pattern)
			if err != nil {
				return false, err
			}
			return re.MatchString(v), nil
		}}
	}}
}

// FlagGroups wires [CommandDef.FlagGroups] validation.
func FlagGroups() Feature {
	return Feature{name: "flag-groups", apply: func(s *featureSet) {
		s.groups = &wiredGroupHooks
	}}
}

// FlagDependencies wires [CommandDef.FlagDependencies] validation.
func FlagDependencies() Feature {
	return Feature{name: "flag-dependencies", apply: func(s *featureSet) {
		s.deps = &wiredDependencyHooks
	}}
}

// StreamPaths wires the inputfile and outputfile types.
func StreamPaths() Feature {
	return Feature{name: "stream-paths", apply: func(s *featureSet) {
		s.streamPaths = &wiredStreamPathHooks
	}}
}

// URLValues wires *url.URL inputs (type url).
func URLValues() Feature {
	return Feature{name: "values:url", apply: func(s *featureSet) {
		s.addKind(&valueKind{id: "url", label: "URL", types: []kindType{{pkg: "net/url", name: "URL", ptr: true}}, parse: parseURLValue})
	}}
}

// EmailValues wires mail.Address inputs (type email).
func EmailValues() Feature {
	return Feature{name: "values:email", apply: func(s *featureSet) {
		s.addKind(&valueKind{id: "email", label: "email address", types: []kindType{{pkg: "net/mail", name: "Address"}}, parse: parseEmailValue})
	}}
}

// TimeZoneValues wires *time.Location inputs (type timezone).
func TimeZoneValues() Feature {
	return Feature{name: "values:timezone", apply: func(s *featureSet) {
		s.addKind(&valueKind{id: "timezone", label: "time zone", types: []kindType{{pkg: "time", name: "Location", ptr: true}}, parse: parseTimeZoneValue})
	}}
}

// MACValues wires net.HardwareAddr inputs (type mac).
func MACValues() Feature {
	return Feature{name: "values:mac", apply: func(s *featureSet) {
		s.addKind(&valueKind{
			id: "mac", label: "MAC address", types: []kindType{{pkg: "net", name: "HardwareAddr"}}, parse: parseMACValue,
			unique: func(v string) (string, error) {
				m, err := net.ParseMAC(v)
				return m.String(), err
			},
		})
	}}
}

// IPValues wires netip.Addr, netip.Prefix and netip.AddrPort inputs (types ip, cidr and
// hostport).
func IPValues() Feature {
	return Feature{name: "values:ip", apply: func(s *featureSet) {
		s.addKind(&valueKind{
			id: "ip", label: "IP address", types: []kindType{{pkg: "net/netip", name: "Addr"}},
			unique: func(v string) (string, error) {
				a, err := netip.ParseAddr(v)
				return a.String(), err
			},
		})
		s.addKind(&valueKind{
			id: "ip", label: "CIDR prefix", types: []kindType{{pkg: "net/netip", name: "Prefix"}},
			unique: func(v string) (string, error) {
				p, err := netip.ParsePrefix(v)
				return p.String(), err
			},
		})
		s.addKind(&valueKind{
			id: "ip", label: "address and port", types: []kindType{{pkg: "net/netip", name: "AddrPort"}},
			unique: func(v string) (string, error) {
				p, err := netip.ParseAddrPort(v)
				return p.String(), err
			},
		})
	}}
}

// RegexpValues wires *regexp.Regexp inputs (type regexp).
func RegexpValues() Feature {
	return Feature{name: "values:regexp", apply: func(s *featureSet) {
		s.addKind(&valueKind{id: "regexp", label: "regular expression", types: []kindType{{pkg: "regexp", name: "Regexp", ptr: true}}, parse: parseRegexpValue})
	}}
}

// allKinds is every value kind, for code that runs without a program, such as [ArgvOf].
func allKinds() []*valueKind { return allFeatures().kinds }

// valueKindOf is the wired kind of field type t. A named pointer type is its own type, never
// a kind's pointer type.
func (s *featureSet) valueKindOf(t reflect.Type) *valueKind {
	if t.Kind() == reflect.Pointer && t.Name() != "" {
		return nil
	}
	return s.kindFor(t)
}

// kindNamed is the wired kind whose type a Definition names typ ("netip.Addr", "*url.URL"),
// nil when none is wired.
func (s *featureSet) kindNamed(typ string) *valueKind {
	for _, k := range s.kinds {
		for _, kt := range k.types {
			name := path.Base(kt.pkg) + "." + kt.name
			if kt.ptr {
				name = "*" + name
			}
			if name == typ {
				return k
			}
		}
	}
	return nil
}

// missingKind is the error for an input of value kind id (whose errors call it label), which the program
// does not wire. [coerceFailure] names the input.
func missingKind(id, label string) *WiringError {
	var ctor string
	switch id {
	case "url":
		ctor = "URLValues()"
	case "email":
		ctor = "EmailValues()"
	case "timezone":
		ctor = "TimeZoneValues()"
	case "mac":
		ctor = "MACValues()"
	case "ip":
		ctor = "IPValues()"
	case "regexp":
		ctor = "RegexpValues()"
	}
	return missingFeature("values:"+id, ctor, "input is "+withArticle(label))
}

// withArticle puts "a" or "an" before a value kind's label.
func withArticle(label string) string {
	switch label {
	case "IP address", "email address", "address and port":
		return "an " + label
	}
	return "a " + label
}

// parseURLValue reads a *url.URL: an absolute URL with a scheme and a host.
func parseURLValue(s string) (reflect.Value, error) {
	u, err := url.Parse(s)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("want a URL such as https://example.com: %w", err)
	}
	if u.Scheme == "" || (u.Host == "" && u.Opaque == "") {
		return reflect.Value{}, errors.New("a URL needs a scheme and a host, e.g. https://example.com")
	}
	// "localhost:8080/api" parses as scheme "localhost" with the opaque part "8080/api": a
	// host, port and path written without a scheme.
	if u.Host == "" && isPortOpaque(u.Opaque) {
		return reflect.Value{}, fmt.Errorf("a host and port need a scheme, as in http://%s", s)
	}
	return reflect.ValueOf(u), nil
}

// parseEmailValue reads a mail.Address.
func parseEmailValue(s string) (reflect.Value, error) {
	a, err := mail.ParseAddress(s)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("want ada@example.com or \"Ada <ada@example.com>\": %w", err)
	}
	return reflect.ValueOf(*a), nil
}

// parseTimeZoneValue reads a *time.Location from an IANA name, matched exactly, or UTC or
// Local. An empty name is refused, though the time package reads it as UTC.
func parseTimeZoneValue(s string) (reflect.Value, error) {
	if s == "" {
		return reflect.Value{}, errors.New("want an IANA name such as Europe/Berlin, or UTC or Local")
	}
	loc, err := time.LoadLocation(s)
	if err == nil && !exactZoneName(s) {
		err = errors.New("unknown time zone " + s)
	}
	if err != nil {
		return reflect.Value{}, fmt.Errorf("want an IANA name such as Europe/Berlin: %w", err)
	}
	return reflect.ValueOf(loc), nil
}

// zoneDirs are the directories the time package reads zone files from, beside $ZONEINFO.
var zoneDirs = []string{"/usr/share/zoneinfo", "/usr/share/lib/zoneinfo", "/usr/lib/locale/TZ", "/etc/zoneinfo"}

// exactZoneName reports whether name, which the time package loaded, is spelled exactly as its
// zone file is: on a case-insensitive file system the time package also loads "utc" or
// "europe/berlin". A zone read from a zip archive or the embedded database is matched exactly
// already.
func exactZoneName(name string) bool {
	if name == "UTC" || name == "Local" {
		return true
	}
	dirs := zoneDirs
	if z := os.Getenv("ZONEINFO"); z != "" {
		dirs = append([]string{z}, dirs...)
	}
	for _, dir := range dirs {
		//nolint:gosec // name is a zone the time package already loaded; this checks only its case
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err != nil || info.IsDir() {
			continue
		}
		return exactPath(dir, strings.Split(name, "/"))
	}
	return true
}

// exactPath reports whether each of parts names an entry below dir in exactly that case.
func exactPath(dir string, parts []string) bool {
	for _, p := range parts {
		entries, err := os.ReadDir(dir)
		if err != nil || !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == p }) {
			return false
		}
		dir = filepath.Join(dir, p)
	}
	return true
}

// parseMACValue reads a net.HardwareAddr.
func parseMACValue(s string) (reflect.Value, error) {
	mac, err := net.ParseMAC(s)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("want hex pairs such as 01:23:45:67:89:ab: %w", err)
	}
	return reflect.ValueOf(mac), nil
}

// parseRegexpValue reads a *regexp.Regexp. It parses itself, but this names the problem
// without Go's prefix.
func parseRegexpValue(s string) (reflect.Value, error) {
	re, err := regexp.Compile(s)
	if err != nil {
		if se, ok := asError[*syntax.Error](err); ok {
			return reflect.Value{}, fmt.Errorf("%s: `%s`", se.Code, se.Expr)
		}
		return reflect.Value{}, fmt.Errorf("want a regular expression: %w", err)
	}
	return reflect.ValueOf(re), nil
}

// The hooks each feature wires. They never change, so every program shares one value.
var (
	wiredGroupHooks      = groupHooks{validate: validateFlagGroups}
	wiredDependencyHooks = dependencyHooks{validate: validateFlagDependencies}
	wiredStreamPathHooks = streamPathHooks{check: checkStreamPath, dashOnce: stdinDashOnce}
)
