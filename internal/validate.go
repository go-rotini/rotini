package internal

func Validate() error {
	// 1. reads in user's spec file (spec file command argument); if the file is not found, err
	// 2. optionally reads in user's conf file (conf file command flag [-c, --config]); if a file is specified but not found, err
	/* 3. both the spec and config file have jsonschema spec files under schemas/spec.json and schemas.conf.json that are embedded via the schemas/embed.go file.
	 *    - the go-rotini/jsonschema package should be used to compare the users spec file against the spec.json file, collecting all errs if any
	 *    - the go-rotini/jsonschema package should be used to compare the users conf file (if provided) against the conf.json file, collecting all errs if any
	 * 4. all errors are wrapped and returned as the single error returned by the function, the calling rotini cli framework handler function will unwrap the errors for printing if any
	 */
	return nil
}
