package rtk

// TokenizeResult is what [Tokenize] returns: the matched command path
// extracted from argv against a ProgramSpec, plus any aliases resolved
// along the way.
type TokenizeResult struct {
	// CommandPath is the matched command path as a slice of segments
	// (the canonical Name of each matched CommandSpec). Empty when no
	// subcommand was matched (i.e., the root command).
	CommandPath []string
}

// Tokenize walks argv against spec's command tree and returns the matched
// command path. It does not apply spec rules (no env/config/default
// resolution, no coercion, no validation) — its sole job is to identify
// which command is active, fast.
//
// Used by generated Program.Execute for routing dispatch before any handler
// runs.
//
// Unknown flag tokens are tolerated (they may belong to deeper scopes the
// router doesn't know about); only obvious structural errors (a `--`
// followed by junk before the first command match) surface as an error.
func Tokenize(argv []string, spec ProgramSpec) (*TokenizeResult, error) {
	result := &TokenizeResult{}

	if len(argv) == 0 {
		return result, nil
	}

	// Stop walking at `--` — everything after is positional.
	doubleDashIdx := -1
	for i, t := range argv {
		if t == "--" {
			doubleDashIdx = i
			break
		}
	}
	tokens := argv
	if doubleDashIdx >= 0 {
		tokens = argv[:doubleDashIdx]
	}

	consumed := make(map[int]bool)
	current := spec.Commands

	startIdx := 0
	for {
		cmd, idx := findMatchedCommand(current, tokens, startIdx, consumed)
		if cmd == nil {
			return result, nil
		}
		result.CommandPath = append(result.CommandPath, cmd.Name)
		consumed[idx] = true
		current = cmd.Commands
		startIdx = idx + 1
	}
}

// parseScopedFlags consumes flag tokens belonging to the given scope's flag
// set, writing values into scope and marking consumed indices.
//
// `args` may be mutated: if a flag's value is a literal `-` stdin marker
// and stdinData is available, the marker is replaced in-place with the
// stdin content so downstream code can treat it as an ordinary value.
//
// (helper) Walks each token in argv with several branches.
func parseScopedFlags(
	flags []FlagSpec,
	scope map[string]any,
	args []string,
	consumed map[int]bool,
	stdinData []byte,
	coerce CoerceValueFn,
) error {
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "-" || !isFlag(token) {
			continue
		}
		flagName, flagValue, hasInlineValue := splitFlagValue(token)
		meta := findFlag(flags, flagName)
		if meta == nil {
			// Token might be a grouped short ("-abc"); expand if all chars
			// are bool flags in this scope.
			if grouped := expandGroupedShortFlags(token, flags); grouped != nil {
				for _, gm := range grouped {
					appendFlagValue(scope, gm.Name, true)
				}
			}
			continue
		}
		// If the next argument is `-` and we have stdin data, substitute
		// the stdin content as the value. Non-bool flags only.
		if !hasInlineValue && meta.Type != "bool" && i+1 < len(args) && args[i+1] == "-" && len(stdinData) > 0 {
			args[i+1] = string(stdinData)
		}
		val, nextConsumed, err := resolveFlagValue(meta, flagName, flagValue, hasInlineValue, args, i, coerce)
		if err != nil {
			return err
		}
		if nextConsumed {
			consumed[i+1] = true
			i++
		}
		appendFlagValue(scope, meta.Name, val)
	}
	return nil
}

// parseCommandLevel walks one command level of argv: consumes scoped flag
// tokens, looks for a child-command match, recurses into the child if
// found, or collects the remaining positionals into result.ParsedArgs.
//
// It also applies env/config/default resolution and per-flag/-argument
// validation for the active command's scope before recursing or returning.
//
// (helper) Recursive level-walker is inherently branchy.
func parseCommandLevel(
	cmd *CommandSpec,
	args []string,
	cmdIdx int,
	consumed map[int]bool,
	doubleDashSeen bool,
	extraPositional []string,
	result *Result,
	in *Inputs,
	rootFlags []FlagSpec,
	coerce CoerceValueFn,
) error {
	cmdScope := make(map[string]any)
	result.FlagsByScope[cmd.Name] = cmdScope
	consumed[cmdIdx] = true

	stdinData := readStdin(in)
	if err := parseScopedFlags(cmd.Flags, cmdScope, args, consumed, stdinData, coerce); err != nil {
		return err
	}

	subCmd, subCmdIdx := findMatchedCommand(cmd.Commands, args, cmdIdx+1, consumed)

	// If we're not delegating to a deeper command, surface unknown-flag
	// errors for unconsumed flag tokens in the slice between this command
	// and its first positional / subcommand / end-of-input.
	if !doubleDashSeen {
		end := len(args)
		if subCmd != nil {
			end = subCmdIdx
		}
		for i := cmdIdx + 1; i < end; i++ {
			if consumed[i] {
				continue
			}
			token := args[i]
			if !isFlag(token) || token == "-" {
				continue
			}
			flagName, _, _ := splitFlagValue(token)
			if findFlag(rootFlags, flagName) != nil ||
				findFlag(cmd.Flags, flagName) != nil ||
				expandGroupedShortFlags(token, cmd.Flags) != nil {
				continue
			}
			return &UnknownFlagError{
				Flag:        flagName,
				Command:     cmd.Name,
				Suggestions: suggestFlags(flagName, cmd.Flags),
			}
		}
	}

	if subCmd != nil {
		result.CommandPath = append(result.CommandPath, subCmd.Name)
		return parseCommandLevel(subCmd, args, subCmdIdx, consumed, doubleDashSeen, extraPositional, result, in, rootFlags, coerce)
	}

	// No subcommand — collect positional arguments.
	for i := cmdIdx + 1; i < len(args); i++ {
		if consumed[i] || isFlag(args[i]) {
			continue
		}
		if args[i] == "-" && len(stdinData) > 0 {
			result.ParsedArgs = append(result.ParsedArgs, string(stdinData))
		} else {
			result.ParsedArgs = append(result.ParsedArgs, args[i])
		}
	}

	if len(extraPositional) > 0 {
		if len(cmd.Arguments) == 0 {
			return &UnknownArgumentError{Value: extraPositional[0], Position: len(result.ParsedArgs), Command: cmd.Name}
		}
		lastArg := &cmd.Arguments[len(cmd.Arguments)-1]
		if len(extraPositional) > 1 && !lastArg.Variadic {
			return &UnknownArgumentError{Value: extraPositional[1], Position: len(result.ParsedArgs) + 1, Command: cmd.Name}
		}
		result.ParsedArgs = append(result.ParsedArgs, extraPositional...)
	}

	applyEnvVarValues(cmd.Flags, cmdScope, in.Env, coerce)
	applyConfigKeyValues(cmd.Flags, cmdScope, in.Config, coerce)
	applyFlagDefaults(cmd.Flags, cmdScope, coerce)
	applyArgumentDefaults(cmd.Arguments, &result.ParsedArgs)

	if err := validateRequiredFlags(cmd.Flags, cmdScope); err != nil {
		return err
	}
	if err := validateRequiredArguments(cmd.Arguments, result.ParsedArgs); err != nil {
		return err
	}
	for i := range cmd.Arguments {
		argMeta := &cmd.Arguments[i]
		if argMeta.Variadic {
			for j := i; j < len(result.ParsedArgs); j++ {
				if err := validateArgumentConstraints(argMeta, result.ParsedArgs[j]); err != nil {
					return err
				}
			}
			break
		}
		if i < len(result.ParsedArgs) {
			if err := validateArgumentConstraints(argMeta, result.ParsedArgs[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseProgram is the top-level entry. Given a spec, an Inputs bundle, and
// a CoerceValueFn, it tokenizes argv, walks down the command tree
// resolving flag values per spec rules, and returns a populated Result.
//
// This is the engine the default Parser implementation calls. It does not
// know about Target — Result-to-Target projection is the Parser's job
// (and codegen-emitted PopulateFromArgv).
//
// (helper) Top-level orchestrator; calls into level-walker.
func parseProgram(spec ProgramSpec, in Inputs, coerce CoerceValueFn) (*Result, error) {
	result := &Result{
		FlagsByScope: make(map[string]map[string]any),
	}
	rootScope := make(map[string]any)
	result.FlagsByScope[""] = rootScope

	// Find `--`, if present, and split argv into flag-args and
	// extra-positionals.
	doubleDashIdx := -1
	for i, arg := range in.Argv {
		if arg == "--" {
			doubleDashIdx = i
			break
		}
	}
	flagArgs := in.Argv
	var extraPositional []string
	if doubleDashIdx >= 0 {
		flagArgs = in.Argv[:doubleDashIdx]
		extraPositional = in.Argv[doubleDashIdx+1:]
	}

	consumed := make(map[int]bool)

	stdinData := readStdin(&in)
	if err := parseScopedFlags(spec.Flags, rootScope, flagArgs, consumed, stdinData, coerce); err != nil {
		return nil, err
	}

	matchedCmd, cmdIdx := findMatchedCommand(spec.Commands, flagArgs, 0, consumed)

	applyEnvVarValues(spec.Flags, rootScope, in.Env, coerce)
	applyConfigKeyValues(spec.Flags, rootScope, in.Config, coerce)
	applyFlagDefaults(spec.Flags, rootScope, coerce)

	if err := validateRequiredFlags(spec.Flags, rootScope); err != nil {
		return nil, err
	}

	if matchedCmd == nil {
		// No subcommand. Surface unknown-flag errors for unconsumed flag
		// tokens in the root scope.
		for _, token := range flagArgs {
			if !isFlag(token) || token == "-" {
				continue
			}
			flagName, _, _ := splitFlagValue(token)
			if findFlag(spec.Flags, flagName) == nil && expandGroupedShortFlags(token, spec.Flags) == nil {
				return nil, &UnknownFlagError{
					Flag:        flagName,
					Suggestions: suggestFlags(flagName, spec.Flags),
				}
			}
		}
		return result, nil
	}

	result.CommandPath = append(result.CommandPath, matchedCmd.Name)
	if err := parseCommandLevel(matchedCmd, flagArgs, cmdIdx, consumed, doubleDashIdx >= 0, extraPositional, result, &in, spec.Flags, coerce); err != nil {
		return nil, err
	}
	return result, nil
}

// readStdin reads all of in.Stdin if it's a piped (non-TTY) reader. It
// returns nil if Stdin is nil or appears to be a terminal. The result is
// cached on a per-Inputs basis to avoid re-reading.
//
// At M1.4 this is a thin wrapper that reads everything available. M1.7
// (stdin shaping) extends it to drive [StdinSpec.Format] dispatch.
func readStdin(in *Inputs) []byte {
	if in == nil || in.Stdin == nil {
		return nil
	}
	// We don't have a TTY check at this level — defer that to rtk.IO's
	// helpers in M1.9. For now, read whatever is available.
	if rb, ok := in.Stdin.(interface{ ReadRawBytes() ([]byte, error) }); ok {
		data, err := rb.ReadRawBytes()
		if err != nil {
			return nil
		}
		return data
	}
	// Fall back to nothing — the caller will use the raw reader directly
	// for stdin substitution semantics.
	return nil
}
