package internal

/*
 * PRE-REQUISITES
 * - make sure the templates used to generate command handler stub files, the collective handlers file and the framework gen types/light logic file are currently correct based on the current state of my "rotini companion cli" structure in rotini/cmd/rotini (handlers pkg, rtg pkg)
 * 1. Read in the specPath and confPath (conf does not have to exist, we will need to come up with the best place to apply "sane rotini conf defaults" when a conf file is not supplied). The spec.go and conf.go files have functions for reading each respective file.
 * 2. Generate the users prefered "rotini gen" dir/package and file name. For the rotini cli companion cli, I am using rotini/cmd/rotini/rtg/rotini.go. So my package is "rtg" (a shortening of rotini gen) and my file name is "rotini.go". These should be the defaults.
 * 3. Generate every command in the end-users spec file into their prefered "rotini handlers" package. They don't get to choose file names. The file names are based on the command path to avoid collisions. They should however be able to supply their prefered handlers package dir/name file. The rotini cli framework works on a "commands all the way down" concept. There is a root command (for my companion cli this is "rotini" -- it serves as the binary/root cmd/wake word "command"), and then commands all the way down where each command can define its own commands.
 * 4. Generate the "handler collecting" file that is placed into the end-users prefered "rotini handlers" package. For my rotini companion cli, this is rotini/cmd/rotini/handlers/handlers.go.
 */

func Generate(specPath, confPath string) error {
	return nil
}
