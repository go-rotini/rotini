package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-rotini/rotini/internal/contractdiff"
)

// This file backs `rotini diff`: it builds the current contract in memory and reads the
// contracts to compare from a file, a git revision or the module cache.

// Contract builds the contract document for the spec and conf in memory, exactly as `rotini
// generate` writes it, whether or not the conf asks for a contract file. The spec and conf are
// validated first; problems are returned joined.
func (p *Processor) Contract(specPath, confPath string) ([]byte, error) {
	rs, rc, err := p.reconcile(specPath, confPath)
	if err != nil {
		return nil, err
	}
	return p.contractOf(rs, rc)
}

func (p *Processor) contractOf(rs *reconciledSpec, rc *reconciledConf) ([]byte, error) {
	if _, err := p.validateDocuments(rs, rc, ""); err != nil {
		return nil, err
	}
	applyConfDefaults(rc.conf, rs.spec.Command.Name)
	prog, err := resolveProgram(rs.spec, rc.conf, rs.path, newPlanner(true))
	if err != nil {
		return nil, err
	}
	return prog.contract(prog.contractNodes())
}

// DiffFn is the signature of [Processor.Diff].
type DiffFn = func(specPath, confPath, oldSource, newSource, release string) (contractdiff.Report, error)

// Diff compares the contract at oldSource with the one at newSource, or, when newSource is "",
// with the contract built from the spec and conf. A source is a file path, git:<ref> (the
// conf's generate.contract.file at that revision), git:<ref>:<path> (a module-root-relative
// file at that revision) or mod://<module>@<version>/<path>. release (X.Y.Z, optional) makes a
// removal the old contract planned for it or earlier expected. The conf's diff.accept entries
// acknowledge findings. Without newSource the spec must exist; the conf is read when there is
// one.
func (p *Processor) Diff(specPath, confPath, oldSource, newSource, release string) (contractdiff.Report, error) {
	if release != "" {
		if err := CheckRelease(release, "release"); err != nil {
			return contractdiff.Report{}, err
		}
	}
	var current []byte
	var rc *reconciledConf
	if newSource == "" {
		rs, conf, err := p.reconcile(specPath, confPath)
		if err != nil {
			return contractdiff.Report{}, err
		}
		if current, err = p.contractOf(rs, conf); err != nil {
			return contractdiff.Report{}, err
		}
		rc = conf
	} else {
		conf, err := reconcileConf(specPath, confPath)
		if err != nil {
			return contractdiff.Report{}, p.explainDecodeFailure("conf", err)
		}
		if conf.path != "" {
			if problems, _ := splitProblems(p.validateAndLintConf(conf)); len(problems) > 0 {
				return contractdiff.Report{}, errors.Join(problems...)
			}
		}
		rc = conf
	}
	contractFile := ""
	if g := rc.conf.Generate; g != nil && g.Contract != nil {
		contractFile = g.Contract.File
	}
	oldDoc, err := readContract(oldSource, contractFile)
	if err != nil {
		return contractdiff.Report{}, err
	}
	newDoc := current
	if newSource != "" {
		if newDoc, err = readContract(newSource, contractFile); err != nil {
			return contractdiff.Report{}, err
		}
	}
	opts := contractdiff.Options{Release: release}
	if rc.conf.Diff != nil {
		for _, a := range rc.conf.Diff.Accept {
			opts.Accept = append(opts.Accept, contractdiff.Accept{Rule: a.Rule, Where: a.Where, Reason: a.Reason})
		}
	}
	report, err := contractdiff.Diff(oldDoc, newDoc, opts)
	if err != nil {
		return contractdiff.Report{}, fmt.Errorf("compare the contracts: %w", err)
	}
	return report, nil
}

// gitSource prefixes a contract read from a git revision.
const gitSource = "git:"

// readContract reads a contract from a file path, a git revision or the module cache.
// contractFile is the conf's generate.contract.file, which a bare git:<ref> reads.
func readContract(source, contractFile string) ([]byte, error) {
	switch {
	case strings.HasPrefix(source, modScheme):
		return readModContract(source)
	case strings.HasPrefix(source, gitSource) && !strings.HasPrefix(source, gitScheme):
		return readGitContract(strings.TrimPrefix(source, gitSource), contractFile)
	}
	b, err := os.ReadFile(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("contract %s: no such file", source)
		}
		return nil, fmt.Errorf("read contract %s: %w", source, err)
	}
	return b, nil
}

// errNoContractFile is returned for a bare git:<ref> when the conf names no contract file.
var errNoContractFile = errors.New("git:<ref> reads the committed contract file; set generate.contract.file, or name the file with git:<ref>:<path>")

// readGitContract reads a contract committed at a revision, with `git show` in the module
// root, so a path is module-root-relative wherever the command runs. It never fetches.
func readGitContract(spec, contractFile string) ([]byte, error) {
	ref, file, hasPath := strings.Cut(spec, ":")
	if !hasPath {
		file = contractFile
	}
	if ref == "" {
		return nil, errors.New("git:<ref> names no revision")
	}
	if file == "" {
		return nil, errNoContractFile
	}
	file = path.Clean(filepath.ToSlash(file))
	if path.IsAbs(file) || file == ".." || strings.HasPrefix(file, "../") {
		return nil, fmt.Errorf("git:%s: the contract path must be module-root-relative", spec)
	}
	root, _, err := findModule()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd := exec.Command("git", "-C", root, "show", ref+":./"+file)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("git:<ref> needs git, which is not installed")
		}
		msg := strings.TrimSpace(stderr.String())
		switch {
		case strings.Contains(msg, "not a git repository"):
			return nil, fmt.Errorf("git:%s: %s is not in a git repository", spec, root)
		case strings.Contains(msg, "does not exist in") || strings.Contains(msg, "exists on disk, but not in"):
			return nil, fmt.Errorf("%s doesn't exist at %s; commit a contract first", file, ref)
		case msg != "":
			return nil, fmt.Errorf("git show %s:%s: %s", ref, file, msg)
		}
		return nil, fmt.Errorf("git show %s:%s: %w", ref, file, err)
	}
	return out, nil
}

// readModContract reads a contract from a module version in the module cache, downloading it
// first when it isn't cached. go.sum verifies it, or the checksum database for a version go.sum
// doesn't list.
func readModContract(source string) ([]byte, error) {
	module, version, sub, err := parseModLocator(source)
	if err != nil {
		return nil, err
	}
	if sub == "." {
		return nil, fmt.Errorf("%s names no contract file; want mod://<module>@<version>/<path>", source)
	}
	dir, err := moduleDirFunc(module, version)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(sub)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s doesn't exist in %s@%s", sub, module, version)
		}
		return nil, fmt.Errorf("read %s: %w", source, err)
	}
	return b, nil
}
