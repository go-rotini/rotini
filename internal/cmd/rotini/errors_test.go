package rotini

import "errors"

// Sentinels the CLI tests inject through the codegen doubles.
var (
	errBadSpec  = errors.New("spec is broken")
	errAdvisory = errors.New("an advisory warning")
)
