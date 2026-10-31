package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
	"text/template"
)

// Author stub templates: the cmd target's `stub_template` replaces the built-in handler stub,
// and `stub_test_template` seeds a test beside each new one. Both render templateHandlerData,
// and both only ever shape files generate creates.

// stubSeed is a handler file this generate creates, and the test seeded beside it.
type stubSeed struct {
	path        string
	content     []byte
	testPath    string // "" when no test is seeded
	testContent []byte
}

// seededTestNote is the line rotini writes at the top of a test seeded from stub_test_template.
// It ties the test to its handler file, so pruning retires the two together; deleting it
// claims the test for the author.
const seededTestNote = "// rotini: seeded beside its handler file; delete this line to keep this test if the command leaves the spec."

// seededTestFile names the test seeded beside the handler file stub.
func seededTestFile(stub string) string {
	return strings.TrimSuffix(stub, ".go") + "_test.go"
}

// isSeededTest reports whether body is a test rotini seeded beside a handler file.
func isSeededTest(body []byte) bool {
	return bytes.Contains(body, []byte(seededTestNote))
}

// authorTemplate is a parsed stub_template or stub_test_template.
type authorTemplate struct {
	key  string // the conf key it came from
	path string // its module-root-relative path, as the conf spells it
	tmpl *template.Template
}

// loadAuthorTemplate reads and parses the template at the module-root-relative rel, named by
// that path so its errors carry it with the line.
func loadAuthorTemplate(pl *planner, moduleRoot, key, rel string) (*authorTemplate, error) {
	src, _, exists, err := pl.read(filepath.Join(moduleRoot, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", key, rel, err)
	}
	if !exists {
		return nil, fmt.Errorf("%s: no file at %s (the path is relative to the module root)", key, rel)
	}
	tmpl, err := template.New(rel).Funcs(templateFuncMap()).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return &authorTemplate{key: key, path: rel, tmpl: tmpl}, nil
}

// render renders the template for the file named file: header and top (each "" for none)
// above its output, gofmt-formatted with grouped imports, as a built-in stub is.
func (a *authorTemplate) render(data templateHandlerData, file, top string) ([]byte, error) {
	var buf bytes.Buffer
	if err := a.tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("%s: %w", a.key, err)
	}
	rendered := buf.Bytes()
	if top != "" {
		rendered = append([]byte(top+"\n\n"), rendered...)
	}
	if h := strings.TrimRight(data.Header, "\n"); h != "" {
		rendered = append([]byte(h+"\n\n"), rendered...)
	}
	formatted, err := format.Source(rendered)
	if err != nil {
		return nil, fmt.Errorf("%s %s: what it renders for %s is not valid Go: %w\n--- rendered source ---\n%s", a.key, a.path, file, err, rendered)
	}
	return groupImports(formatted)
}

// renderStubs renders the handler file of every own command that has none yet, and the test
// seeded beside it, into p.stubSeeds. It runs before the cmd file is written, so the wiring
// follows the methods a new file declares, whichever template wrote it. Writing them is
// emitStubs's.
func (p *program) renderStubs() error {
	lay := p.layout
	var stubTmpl, testTmpl *authorTemplate
	var err error
	if lay.stubTemplate != "" {
		if stubTmpl, err = loadAuthorTemplate(p.plan, p.module.root, "stub_template", lay.stubTemplate); err != nil {
			return err
		}
	}
	if lay.stubTestTemplate != "" {
		if testTmpl, err = loadAuthorTemplate(p.plan, p.module.root, "stub_test_template", lay.stubTestTemplate); err != nil {
			return err
		}
	}
	helpOn := featureEnabled(p.conf, "help")
	var unmarked []string
	for _, c := range p.ownCommands() {
		if c.passthrough {
			continue
		}
		file := stubFileFor(lay.cmdDir, c)
		seed := stubSeed{path: filepath.Join(lay.cmdDir, file)}
		exists, err := p.plan.exists(seed.path)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		data := stubBody(p, c, lay.cmdPkgName, lay.cmdHeader, helpOn)
		if stubTmpl == nil {
			seed.content, err = renderHandlerStubFile(data)
		} else {
			seed.content, err = stubTmpl.render(data, file, "")
			if err == nil && !hasStubMarker(seed.content) {
				unmarked = append(unmarked, file)
			}
		}
		if err != nil {
			return err
		}
		if testTmpl != nil {
			testFile := seededTestFile(file)
			testPath := filepath.Join(lay.cmdDir, testFile)
			exists, err := p.plan.exists(testPath)
			if err != nil {
				return err
			}
			if !exists {
				if seed.testContent, err = testTmpl.render(data, testFile, seededTestNote); err != nil {
					return err
				}
				seed.testPath = testPath
			}
		}
		p.stubSeeds = append(p.stubSeeds, seed)
	}
	if len(unmarked) > 0 {
		p.auditWarnings = append(p.auditWarnings, fmt.Errorf(
			"stub_template %s renders no `var _ rotini.HandlerE = (*T)(nil)` or `var _ rotini.Handler = (*T)(nil)` line, so generate will never retire the handler files it seeds (%s) when their commands leave the spec; add the line to the template to let it",
			lay.stubTemplate, strings.Join(unmarked, ", ")))
	}
	return nil
}

// restoreSeededTest re-enables the test seeded beside the handler file at stubPath when pruning
// disabled it with its handler, and reports whether it did.
func restoreSeededTest(pl *planner, stubPath string) (bool, error) {
	path := filepath.Join(filepath.Dir(stubPath), seededTestFile(filepath.Base(stubPath)))
	body, _, exists, err := pl.read(path)
	if err != nil || !exists || !stubDisabled(body) || !isSeededTest(body) {
		return false, err
	}
	return true, pl.write(path, enableStub(body))
}

// retireSeededTest takes the test seeded beside the handler file stub in dir through the step
// pruning just took the handler through: disabled with it, or, when handlerDeleted, deleted
// with it once both are disabled. Each step is reported through onPrune, as the handler's is.
// A test without rotini's note is the author's and is never touched.
func retireSeededTest(pl *planner, dir, stub string, handlerDeleted bool, onPrune func(string)) error {
	name := seededTestFile(stub)
	path := filepath.Join(dir, name)
	body, _, exists, err := pl.read(path)
	if err != nil || !exists || !isSeededTest(body) {
		return err
	}
	note := name
	switch disabled := stubDisabled(body); {
	case handlerDeleted && disabled:
		err = pl.remove(path)
	case !handlerDeleted && !disabled:
		err = pl.write(path, disableStub(body))
		note += " (disabled; removed by the next generate)"
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("prune %s: %w", name, err)
	}
	if onPrune != nil {
		onPrune(note)
	}
	return nil
}
