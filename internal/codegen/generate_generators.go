package codegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// A generator is a program the conf names under generate.generators. Generate runs it with the
// contract document on stdin and writes the files it returns through the planner, so a dry run
// lists them, an unchanged file isn't rewritten, and a file it stops returning is removed.
// What a generator returned is recorded in its directory's manifest, and only the files a
// manifest lists are ever removed.

// generatorProtocol is the version of the document a generator writes to stdout, its
// "rotini_generator" field.
const generatorProtocol = 1

// generatorManifest is the file, in a generator's dir, listing the files it returned.
const generatorManifest = ".rotini-generated"

// generatorManifestHeader starts every manifest; a file without it records nothing.
const generatorManifestHeader = "# rotini generate wrote these files for generator "

// generatorOutput is the document a generator writes to stdout.
type generatorOutput struct {
	Protocol *int            `json:"rotini_generator"`
	Files    []generatorFile `json:"files"`
}

// generatorFile is one file a generator returns.
type generatorFile struct {
	Path    string  `json:"path"`
	Content *string `json:"content"`
}

// runGenerators runs each conf generator in order, before generate writes anything, so a
// generator that fails or returns output rotini can't use leaves every file as it was. The
// files they return are kept for [program.writeGeneratorFiles].
func (p *program) runGenerators() error {
	g := p.conf.Generate
	if g == nil || len(g.Generators) == 0 {
		return nil
	}
	contract, err := p.contract(p.contractNodes())
	if err != nil {
		return err
	}
	for _, gen := range g.Generators {
		files, err := p.runGenerator(gen, contract)
		if err != nil {
			return fmt.Errorf("generator %q: %w", gen.Name, err)
		}
		p.generated = append(p.generated, generatorRun{gen: gen, files: files})
	}
	return nil
}

// runGenerator runs one generator and returns the files it returned, checked.
func (p *program) runGenerator(gen GeneratorConfig, contract []byte) ([]generatorFile, error) {
	stdout, stderr, err := execGenerator(p.module.root, gen.Run, contract)
	if err != nil {
		return nil, err
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		p.agentNotices = append(p.agentNotices, fmt.Errorf("generator %q: %s", gen.Name, msg))
	}
	return parseGeneratorOutput(stdout)
}

// writeGeneratorFiles plans each generator's files: every one it returned, the removal of each
// one the previous run's manifest lists that it no longer returns (unless kept), and the new
// manifest.
func (p *program) writeGeneratorFiles() error {
	for _, run := range p.generated {
		if err := p.writeGenerated(run); err != nil {
			return fmt.Errorf("generator %q: %w", run.gen.Name, err)
		}
	}
	return nil
}

// writeGenerated plans one generator's files.
func (p *program) writeGenerated(run generatorRun) error {
	dir, err := underModule(p.module.root, "dir", run.gen.Dir)
	if err != nil {
		return err
	}
	current := make(map[string]bool, len(run.files))
	owned := make([]string, 0, len(run.files))
	for _, f := range run.files {
		if err := p.plan.write(filepath.Join(dir, filepath.FromSlash(f.Path)), []byte(*f.Content)); err != nil {
			return err
		}
		current[strings.ToLower(f.Path)] = true
		owned = append(owned, f.Path)
	}
	prior, err := p.readGeneratorManifest(dir)
	if err != nil {
		return err
	}
	for _, rel := range prior {
		if current[strings.ToLower(rel)] || slices.Contains(run.gen.Keep, rel) {
			continue
		}
		if p.skipPrune {
			owned = append(owned, rel) // still the generator's, for a later run to remove
			continue
		}
		if err := p.plan.remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("remove %s, which it no longer writes: %w", rel, err)
		}
	}
	return p.plan.write(filepath.Join(dir, generatorManifest), generatorManifestContent(run.gen.Name, owned))
}

// generatorRun is one generator and the files it returned.
type generatorRun struct {
	gen   GeneratorConfig
	files []generatorFile
}

// execGenerator runs argv in dir with stdin on its standard input, and returns what it wrote.
// A failure to start or a non-zero exit is an error that carries its stderr.
func execGenerator(dir string, argv []string, stdin []byte) (stdout, stderr []byte, err error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := fmt.Sprintf("run %s: %v", strings.Join(argv, " "), err)
		if s := strings.TrimSpace(errOut.String()); s != "" {
			msg += "\n" + s
		}
		return nil, nil, errors.New(msg)
	}
	return out.Bytes(), errOut.Bytes(), nil
}

// parseGeneratorOutput decodes and checks a generator's stdout: exactly one document of the
// protocol's version, with fields it knows, whose files each have content and a path
// [generatorPathProblem] accepts, no two naming the same file in any case, and none inside
// another. The files come back sorted by path.
func parseGeneratorOutput(stdout []byte) ([]generatorFile, error) {
	const shape = `{"rotini_generator": 1, "files": [{"path": "...", "content": "..."}]}`
	dec := json.NewDecoder(bytes.NewReader(stdout))
	dec.DisallowUnknownFields()
	var out generatorOutput
	if err := dec.Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("it wrote nothing to stdout; a generator writes %s", shape)
		}
		return nil, fmt.Errorf("its stdout isn't a generator document, %s: %w", shape, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("its stdout holds more than one JSON document; a generator writes one, %s", shape)
	}
	switch {
	case out.Protocol == nil:
		return nil, fmt.Errorf(`its document has no "rotini_generator" version; a generator writes %s`, shape)
	case *out.Protocol != generatorProtocol:
		return nil, fmt.Errorf(`its document is "rotini_generator": %d, and this rotini reads version %d`, *out.Protocol, generatorProtocol)
	}
	files := slices.Clone(out.Files)
	slices.SortFunc(files, func(a, b generatorFile) int { return strings.Compare(a.Path, b.Path) })
	seen := make(map[string]string, len(files))
	for _, f := range files {
		if why := generatorPathProblem(f.Path); why != "" {
			return nil, fmt.Errorf("file path %q %s", f.Path, why)
		}
		if f.Content == nil {
			return nil, fmt.Errorf("file %q has no \"content\"", f.Path)
		}
		key := strings.ToLower(f.Path)
		if other, ok := seen[key]; ok {
			if other == f.Path {
				return nil, fmt.Errorf("file %q is listed twice", f.Path)
			}
			return nil, fmt.Errorf("files %q and %q differ only in case, so they are one file on macOS and Windows", other, f.Path)
		}
		seen[key] = f.Path
	}
	for _, f := range files {
		for d := path.Dir(f.Path); d != "."; d = path.Dir(d) {
			if other, ok := seen[strings.ToLower(d)]; ok {
				return nil, fmt.Errorf("file %q is inside %q, which is also a file", f.Path, other)
			}
		}
	}
	return files, nil
}

// generatorPathProblem says why rel can't name a file under a generator's dir, or "": it must
// be a clean, relative, slash-separated path that stays inside the dir, can be checked out on
// every OS, and isn't the manifest.
func generatorPathProblem(rel string) string {
	switch {
	case rel == "":
		return "is empty"
	case strings.ContainsAny(rel, "\\:\x00"):
		return "must use '/' as its separator and hold no ':' or NUL, which some systems can't put in a file name"
	case path.IsAbs(rel):
		return "must be relative to the generator's dir, not absolute"
	case rel == ".." || strings.HasPrefix(rel, "../"):
		return "must stay inside the generator's dir"
	case path.Clean(rel) != rel || rel == ".":
		return "must be a clean path: no '.' or '..' element, and no repeated or trailing '/'"
	case !filepath.IsLocal(filepath.FromSlash(rel)):
		return "isn't a file name every system accepts"
	case strings.EqualFold(rel, generatorManifest):
		return "is the file rotini records the generator's files in"
	}
	return ""
}

// readGeneratorManifest returns the paths the previous run's manifest in dir lists, as the plan
// so far leaves it. A missing manifest, or a file without the manifest's header, lists none, and
// a line that isn't a path a generator may return is skipped, so a manifest edited by hand can't
// make generate remove a file outside dir.
func (p *program) readGeneratorManifest(dir string) ([]string, error) {
	data, _, exists, err := p.plan.read(filepath.Join(dir, generatorManifest))
	if err != nil || !exists {
		return nil, err
	}
	text := string(data)
	if !strings.HasPrefix(text, generatorManifestHeader) {
		return nil, nil
	}
	var paths []string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || generatorPathProblem(line) != "" {
			continue
		}
		paths = append(paths, line)
	}
	return paths, nil
}

// generatorManifestContent renders the manifest for generator name owning paths: a header,
// then each path once, sorted.
func generatorManifestContent(name string, paths []string) []byte {
	paths = slices.Clone(paths)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	var b strings.Builder
	b.WriteString(generatorManifestHeader)
	fmt.Fprintf(&b, "%q", name)
	b.WriteString(", and removes one it stops writing. Do not edit.\n")
	for _, rel := range paths {
		b.WriteString(rel)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
