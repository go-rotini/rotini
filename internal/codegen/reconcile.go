package codegen

// Reconcile is the first pipeline stage: read + decode the end-user's spec and conf
// into their in-memory shapes. The spec is REQUIRED; the conf is OPTIONAL and falls
// back to the default shape. Each reconciled value retains the source format/bytes so a
// later validation problem can be located back to file:line:col. Reading and path
// discovery live in reader.go; the embedded schemas the validate stage uses live in
// schema.go.

import (
	"fmt"
	"os"
)

// reconciledSpec is an end-user spec read + decoded, alongside the canonical-JSON
// instance (the same bytes generation consumes, for schema validation) and a locator
// from the source format.
type reconciledSpec struct {
	path   string        // resolved spec path
	spec   *Spec         // decoded spec
	json   []byte        // the spec as canonical JSON, for schema validation
	locate sourceLocator // JSON-pointer → source line:col (nil when the format carries no positions)
}

// reconciledConf is reconciledSpec for the conf. The conf is OPTIONAL: when no file is
// found, path is "", conf is the default &Conf{}, and json is nil (nothing to validate).
type reconciledConf struct {
	path   string        // resolved conf path ("" when none — defaults used)
	conf   *Conf         // decoded conf, or the default
	json   []byte        // the conf as canonical JSON (nil when defaulted)
	locate sourceLocator // JSON-pointer → source line:col (nil when no file / no positions)
}

// reconcileSpec reads + decodes the end-user spec at path (or the first .rotini.spec.* in
// the working directory when path is ""). The spec is REQUIRED: with no path and none
// discovered it returns errSpecPathRequired.
func (p *Processor) reconcileSpec(path string) (*reconciledSpec, error) {
	resolved, err := resolveSpecPath(path)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return nil, errSpecPathRequired
	}
	format, data, err := readRaw(resolved)
	if err != nil {
		return nil, err
	}
	spec, err := decodeData[Spec](format, data, resolved)
	if err != nil {
		return nil, err
	}
	instance, err := bytesToJSON(format, data)
	if err != nil {
		return nil, fmt.Errorf("convert %s to json: %w", resolved, err)
	}
	return &reconciledSpec{path: resolved, spec: spec, json: instance, locate: newSourceLocator(format, data)}, nil
}

// reconcileConf reads + decodes the end-user conf beside the spec (or at confPath). The
// conf is OPTIONAL: no path given and none discovered — or a resolved path that does not
// exist — yields the DEFAULT &Conf{} with an empty path.
func (p *Processor) reconcileConf(specPath, confPath string) (*reconciledConf, error) {
	rc := &reconciledConf{conf: &Conf{}}

	resolved := resolveConfBesideSpec(specPath, confPath)
	if resolved == "" {
		return rc, nil
	}
	if _, statErr := os.Stat(resolved); statErr != nil {
		if os.IsNotExist(statErr) {
			return rc, nil // optional → default when the resolved path doesn't exist
		}
		return nil, fmt.Errorf("stat conf %s: %w", resolved, statErr)
	}

	format, data, err := readRaw(resolved)
	if err != nil {
		return nil, err
	}
	conf, err := decodeData[Conf](format, data, resolved)
	if err != nil {
		return nil, err
	}
	instance, err := bytesToJSON(format, data)
	if err != nil {
		return nil, fmt.Errorf("convert %s to json: %w", resolved, err)
	}
	rc.path, rc.conf, rc.json, rc.locate = resolved, conf, instance, newSourceLocator(format, data)
	return rc, nil
}
