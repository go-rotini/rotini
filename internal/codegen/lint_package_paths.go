package codegen

import (
	"fmt"
	"go/token"
	"path"
	"strings"
)

// lintPackageNames rejects a target the go tool refuses: a `file` in a directory named after a
// Windows device (CON, AUX, NUL, LPT1 and so on, which the go tool disallows in import paths on
// every system), a file so named (Windows can't create it, and the go tool refuses it in a
// module zip), a `package` name that is a Go keyword, and `package: main` on a target other
// packages import.
func lintPackageNames(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	for i, p := range conf.Generate.Packages {
		file := path.Clean(strings.ReplaceAll(p.File, `\`, "/"))
		if el := windowsDeviceElement(path.Dir(file)); el != "" {
			problems = append(problems, &problem{kind: "conf", ptr: packagePointer(i) + "/file", loc: "generate.packages." + p.Type,
				msg: fmt.Sprintf("file %s has the path element %q, a Windows device name the go tool refuses in an import path on every system; choose another directory", quotePath(p.File), el)})
		} else if el := windowsDeviceElement(path.Base(file)); el != "" {
			device, _, _ := strings.Cut(el, ".")
			problems = append(problems, &problem{kind: "conf", ptr: packagePointer(i) + "/file", loc: "generate.packages." + p.Type,
				msg: fmt.Sprintf("file %s is named after the Windows device %s: Windows can't create it, and the go tool refuses it in a module zip, so the module can't be published; choose another file name", quotePath(p.File), strings.ToUpper(device))})
		}
		switch {
		case token.IsKeyword(p.Package):
			problems = append(problems, &problem{kind: "conf", ptr: packagePointer(i) + "/package", loc: "generate.packages." + p.Type,
				msg: fmt.Sprintf("package %q is a Go keyword, which can't name a package; choose another name (e.g. %q)", p.Package, p.Package+"cli")})
		case p.Package == "main" && p.Type != typeMain:
			problems = append(problems, &problem{kind: "conf", ptr: packagePointer(i) + "/package", loc: "generate.packages." + p.Type,
				msg: fmt.Sprintf("package \"main\" is a program, which the %s package can't be, since other packages import it; choose another name (e.g. \"maincli\")", p.Type)})
		}
	}
	return problems
}

// lintPackagePaths rejects a root command named after a Windows device (see lintPackageNames)
// when the conf leaves the cmd package at its default, internal/cmd/<root>.
func lintPackagePaths(spec *Spec, conf *Conf) []error {
	if conf != nil && conf.Generate != nil {
		if p := conf.Generate.cmdPkg(); p != nil && p.File != "" {
			return nil
		}
	}
	name := spec.Command.Name
	if windowsDeviceElement(name) == "" {
		return nil
	}
	return []error{&problem{kind: "spec", ptr: rootPointer + "/name", loc: rootLabel(spec),
		msg: fmt.Sprintf("the name %q is a Windows device name, and the cmd package defaults to internal/cmd/%s, which the go tool refuses in an import path on every system; set the cmd target's `file` in the conf to another directory, or rename the command", name, name)}}
}

// windowsDeviceElement returns the first element of the slash-separated path p that Windows
// reserves as a device name (the part before any dot, in any case) and the go tool refuses,
// or "". COM0 and LPT0 are reserved by Windows too, but the go tool accepts them.
func windowsDeviceElement(p string) string {
	for el := range strings.SplitSeq(path.Clean(strings.ReplaceAll(p, `\`, "/")), "/") {
		stem, _, _ := strings.Cut(el, ".")
		switch s := strings.ToUpper(stem); {
		case s == "CON" || s == "PRN" || s == "AUX" || s == "NUL":
			return el
		case len(s) == 4 && (strings.HasPrefix(s, "COM") || strings.HasPrefix(s, "LPT")) && s[3] >= '1' && s[3] <= '9':
			return el
		}
	}
	return ""
}
