package internal

import (
	"fmt"
	"os"
)

// Generate reads the rotini spec at specPath and the conf at confPath, then
// emits the generated program files. confPath may be empty (or point at a file
// that does not exist), in which case the sane rotini conf defaults are used.
//
// A pass produces three things, mirroring the "commands all the way down" model
// where a root command owns sub-commands that own their own sub-commands:
//
//  1. The framework file (default rtg/rotini.go): the ProgramHandlers aggregate
//     interface plus the typed Flags/Arguments/CommandInputs/Inputs structs for
//     the root command and every sub-command. Always (over)written.
//  2. A handler stub per command in the handler package, named after the
//     command path to avoid collisions (rotini.go, rotini_generate.go, …).
//     Created only when missing, since stubs hold user code.
//  3. The handler rollup file (default handlers.go): the handlers struct, the
//     Program var, and one method per command. Always (over)written; orphaned
//     stubs are pruned when generate.cmd.prune is enabled.
func Generate(specPath, confPath string) error {
	spec, err := ReadSpec(specPath)
	if err != nil {
		return err
	}

	conf, err := loadConfOrDefaults(confPath)
	if err != nil {
		return err
	}
	applyConfDefaults(conf)

	return generateAll(spec, conf, specPath)
}

// loadConfOrDefaults reads the conf at confPath, treating an empty path or a
// missing file as "no conf supplied" and returning an empty *Conf so that
// applyConfDefaults can fill in the defaults.
func loadConfOrDefaults(confPath string) (*Conf, error) {
	if confPath == "" {
		return &Conf{}, nil
	}
	if _, err := os.Stat(confPath); err != nil {
		if os.IsNotExist(err) {
			return &Conf{}, nil
		}
		return nil, fmt.Errorf("stat conf %s: %w", confPath, err)
	}
	return ReadConf(confPath)
}
