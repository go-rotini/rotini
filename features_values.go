package rotini

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"regexp/syntax"
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
		return checkNumericBounds(label, c, v, secret, prefixedNumber, formatNum)
	case isNumericType(elem):
		return checkNumericBounds(label, c, v, secret, parseNumber, formatNum)
	case measuredFor(elem) != nil:
		m := measuredFor(elem)
		return checkNumericBounds(label, c, v, secret, m.parse, m.format)
	case isPathType(elem), elem == "string":
		return checkLengthBounds(label, c, v, secret)
	}
	return nil
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

// parseTimeZoneValue reads a *time.Location from an IANA name.
func parseTimeZoneValue(s string) (reflect.Value, error) {
	loc, err := time.LoadLocation(s)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("want an IANA name such as Europe/Berlin: %w", err)
	}
	return reflect.ValueOf(loc), nil
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
		if se, ok := errors.AsType[*syntax.Error](err); ok {
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
