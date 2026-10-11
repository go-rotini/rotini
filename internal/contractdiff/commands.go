package contractdiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// document compares the document-level facts, the shared definitions, then every command.
func (d *differ) document() {
	o, n := d.old, d.new
	if o.Name != n.Name {
		d.add("", Breaking, RuleRootRenamed, o.Name, fmt.Sprintf("the program is renamed %q", n.Name), "")
	}
	if len(o.Errors) > 0 && len(n.Errors) > 0 && !jsonEqual(o.Errors, n.Errors) {
		d.add("", Safe, RuleErrorsChanged, "errors", "the error-line schema changed", "written by a different rotini release")
	}
	d.multicall()
	d.topics()
	d.completion()
	d.responseFiles()
	d.definitions()
	d.commands()
}

func (d *differ) multicall() {
	o, n := d.old.Multicall, d.new.Multicall
	switch {
	case o != nil && n == nil:
		d.add("", Breaking, RuleMulticallNoDelete, "multicall", "the program no longer dispatches on the name it is invoked as", "installed links stop working")
	case o == nil && n != nil:
		d.add("", Safe, RuleMulticallAdded, "multicall", "the program dispatches on the name it is invoked as", "")
	case o != nil && *o != *n:
		d.add("", Breaking, RuleMulticallChanged, "multicall",
			fmt.Sprintf("multicall prefix %q, complete %q → prefix %q, complete %q", o.Prefix, o.Complete, n.Prefix, n.Complete),
			"installed links may stop working")
	}
}

func (d *differ) topics() {
	names := func(ts []topic) []string {
		out := make([]string, 0, len(ts))
		for _, t := range ts {
			out = append(out, t.Name)
		}
		return out
	}
	on, nn := names(d.old.Topics), names(d.new.Topics)
	for _, t := range on {
		if !slices.Contains(nn, t) {
			d.add("", PossiblyBreaking, RuleTopicRemoved, d.old.Name+" help "+t, "help topic removed", "`help "+t+"` now fails")
		}
	}
	for _, t := range nn {
		if !slices.Contains(on, t) {
			d.add("", Safe, RuleTopicAdded, d.old.Name+" help "+t, "help topic added", "")
		}
	}
}

func (d *differ) completion() {
	var o, n completion
	if d.old.Completion != nil {
		o = *d.old.Completion
	}
	if d.new.Completion != nil {
		n = *d.new.Completion
	}
	for _, e := range []struct{ key, o, n string }{
		{"messages_env", o.MessagesEnv, n.MessagesEnv},
		{"descriptions_env", o.DescriptionsEnv, n.DescriptionsEnv},
	} {
		where := "completion." + e.key
		switch {
		case e.o == e.n:
		case e.n == "":
			d.add("", PossiblyBreaking, RuleCompletionEnvRemoved, where, "$"+e.o+" no longer switches completion", "")
		case e.o == "":
			d.add("", Safe, RuleCompletionEnvAdded, where, "$"+e.n+" switches completion", "")
		default:
			d.add("", PossiblyBreaking, RuleCompletionEnvChanged, where, "$"+e.o+" renamed $"+e.n, "")
		}
	}
}

func (d *differ) responseFiles() {
	o, n := d.old.ResponseFiles, d.new.ResponseFiles
	switch {
	case o == nil && n != nil:
		d.add("", Breaking, RuleResponseFilesAdded, "response_files", fmt.Sprintf("an argument starting with %q now names a response file", n.Prefix), "")
	case o != nil && n == nil:
		d.add("", PossiblyBreaking, RuleResponseFilesRemoved, "response_files", fmt.Sprintf("an argument starting with %q is now taken literally", o.Prefix), "")
	case o != nil && o.Prefix != n.Prefix:
		d.add("", Breaking, RuleResponseFilesPrefixChanged, "response_files", fmt.Sprintf("response-file prefix %q → %q", o.Prefix, n.Prefix), "")
	}
}

// commands compares every command, matched by path, then lists the commands added.
func (d *differ) commands() {
	renamed := map[string]bool{} // new commands that keep a removed command's name as an alias
	for _, oc := range d.old.Commands {
		nc := d.new.byPath[pathKey(oc.Path)]
		st := d.commandStability(&oc)
		if nc == nil {
			if owner := d.commandRemoved(st, &oc); owner != "" {
				renamed[owner] = true
			}
			continue
		}
		d.command(st, &oc, nc)
	}
	for _, nc := range d.new.Commands {
		if d.old.byPath[pathKey(nc.Path)] != nil || nc.Hidden || renamed[nc.Name] {
			continue
		}
		st := d.parentStability(nc.Path)
		if nc.Plugin {
			d.add(st, Safe, RulePluginAdded, nc.Name, "plugin added", "")
			continue
		}
		d.add(st, Safe, RuleCommandAdded, nc.Name, "command added", "")
	}
}

// commandStability is a command's effective stability in the old contract: its own, or its
// least settled ancestor's.
func (d *differ) commandStability(c *command) stability {
	return leastStable(d.parentStability(c.Path), c.Stability)
}

// parentStability is the least settled stability of the old contract's commands above path.
func (d *differ) parentStability(path []string) stability {
	var st stability
	for i := range path {
		if a := d.old.byPath[pathKey(path[:i])]; a != nil {
			st = leastStable(st, a.Stability)
		}
	}
	return st
}

// commandRemoved reports an old command the new contract lacks: replaced, renamed with its
// old name kept as an alias, or removed. It returns the name of the command that kept the old name
// as an alias, or "".
func (d *differ) commandRemoved(st stability, oc *command) string {
	if oc.Plugin {
		d.removal(st, RulePluginNoDelete, oc.Name, "plugin removed", "", oc.Hidden)
		return ""
	}
	if oc.ReplacedBy != "" {
		if rc := d.new.byPath[oc.ReplacedBy]; rc != nil {
			d.add(st, Expected, RuleCommandReplaced, oc.Name, "command removed; replaced by "+rc.Name, "")
			return ""
		}
	}
	if owner := d.aliasOwner(oc); owner != "" {
		d.add(st, Safe, RuleCommandRenamedAliasKept, oc.Name, "renamed "+owner+"; the old name still runs it as an alias", "")
		return owner
	}
	d.removal(st, RuleCommandNoDelete, oc.Name, "command removed", oc.RemovedIn, oc.Hidden)
	return ""
}

// aliasOwner returns the name of the new command that is oc's sibling and accepts oc's name as
// an alias, or "".
func (d *differ) aliasOwner(oc *command) string {
	if len(oc.Path) == 0 {
		return ""
	}
	parent, last := oc.Path[:len(oc.Path)-1], oc.Path[len(oc.Path)-1]
	for _, nc := range d.new.Commands {
		if len(nc.Path) == len(oc.Path) && slices.Equal(nc.Path[:len(parent)], parent) &&
			(slices.Contains(nc.Aliases, last) || slices.Contains(nc.HiddenAliases, last)) {
			return nc.Name
		}
	}
	return ""
}

// command compares a command present in both contracts.
func (d *differ) command(st stability, oc, nc *command) {
	w := oc.Name
	d.aliases(st, oc, nc)
	d.lifecycle(st, w, lifecycleOf(oc), lifecycleOf(nc))
	d.hidden(st, RuleCommandHidden, RuleCommandUnhidden, w, oc.Hidden, nc.Hidden)
	d.agent(st, w, oc.Agent, nc.Agent)
	if oc.Plugin || nc.Plugin {
		return // a plugin's inputs and output are its own program's
	}
	if oc.OptionsFirst != nc.OptionsFirst {
		d.add(st, Breaking, RuleOptionsFirstChanged, w, onOff("flags must come before the first argument", nc.OptionsFirst), "")
	}
	if oc.Passthrough != nc.Passthrough {
		d.add(st, Breaking, RuleCommandPassthroughChanged, w, onOff("every word after the command is taken as typed", nc.Passthrough), "")
	}
	d.flags(st, oc, nc)
	d.arguments(st, oc, nc)
	d.envs(st, oc, nc)
	d.configs(st, oc, nc)
	d.flagGroups(st, oc, nc)
	d.flagDependencies(st, oc, nc)
	d.configFiles(st, oc, nc)
	d.pluginDiscovery(st, oc, nc)
	d.stdin(st, oc, nc)
	d.output(st, w+" output", oc.Output, nc.Output)
	if oc.Stream != nc.Stream {
		d.add(st, Breaking, RuleOutputStreamChanged, w+" output", onOff("the command writes a stream of items", nc.Stream), "")
	}
	d.exits(st, oc, nc)
	d.effects(st, w, oc.Effects, nc.Effects)
	// Command facts that later contract fields add are compared here.
}

func onOff(fact string, on bool) string {
	if on {
		return fact + " now"
	}
	return "no longer: " + fact
}

// aliases compares a command's listed and hidden aliases.
func (d *differ) aliases(st stability, oc, nc *command) {
	for _, l := range []struct {
		old, new, other []string
		gone, added     string
		what            string
	}{
		{oc.Aliases, nc.Aliases, nc.HiddenAliases, RuleAliasNoDelete, RuleAliasAdded, "alias"},
		{oc.HiddenAliases, nc.HiddenAliases, nc.Aliases, RuleHiddenAliasNoDelete, RuleHiddenAliasAdded, "hidden alias"},
	} {
		for _, a := range l.old {
			where := oc.Name + " alias " + a
			switch {
			case slices.Contains(l.new, a):
			case slices.Contains(l.other, a):
				d.add(st, Safe, RuleAliasMoved, where, l.what+" "+strconv.Quote(a)+" moved between aliases and hidden_aliases", "")
			default:
				d.removal(st, l.gone, where, l.what+" "+strconv.Quote(a)+" removed", oc.IdentifiersRemovedIn[a], false)
			}
		}
		for _, a := range l.new {
			if !slices.Contains(l.old, a) && !slices.Contains(oldOther(oc, l.what), a) {
				d.add(st, Safe, l.added, oc.Name+" alias "+a, l.what+" "+strconv.Quote(a)+" added", "")
			}
		}
	}
}

// oldOther is the old command's other alias list: hidden aliases for "alias", and listed
// aliases for "hidden alias", so a moved name is reported once.
func oldOther(oc *command, what string) []string {
	if what == "alias" {
		return oc.HiddenAliases
	}
	return oc.Aliases
}

// lifecycleFacts are the facts every command and input may carry about how settled it is.
type lifecycleFacts struct {
	deprecated, since, removedIn, replacedBy, stability string
	ids                                                 []string
	idsRemovedIn                                        map[string]string
}

func lifecycleOf(c *command) lifecycleFacts {
	return lifecycleFacts{c.Deprecated, c.DeprecatedSince, c.RemovedIn, c.ReplacedBy, c.Stability, c.DeprecatedIdentifiers, c.IdentifiersRemovedIn}
}

func inputLifecycle(in *input) lifecycleFacts {
	return lifecycleFacts{in.Deprecated, in.DeprecatedSince, in.RemovedIn, in.ReplacedBy, in.Stability, in.DeprecatedIdentifiers, in.IdentifiersRemovedIn}
}

// lifecycle compares deprecation, planned removal, replacement and stability: all safe, apart
// from a demotion.
func (d *differ) lifecycle(st stability, where string, o, n lifecycleFacts) {
	switch {
	case o.deprecated == n.deprecated:
	case o.deprecated == "":
		d.add(st, Safe, RuleDeprecationAdded, where, "deprecated: "+n.deprecated, "")
	case n.deprecated == "":
		d.add(st, Safe, RuleDeprecationRemoved, where, "no longer deprecated", "")
	default:
		d.add(st, Safe, RuleDeprecationChanged, where, "deprecation message changed", "")
	}
	if o.since != n.since || o.removedIn != n.removedIn || !slices.Equal(o.ids, n.ids) || !maps.Equal(o.idsRemovedIn, n.idsRemovedIn) {
		d.add(st, Safe, RuleLifecycleChanged, where, "deprecation release, planned removal or deprecated identifiers changed", "")
	}
	if o.replacedBy != n.replacedBy && n.replacedBy != "" {
		d.add(st, Safe, RuleReplacedByChanged, where, "replacement is now "+strconv.Quote(n.replacedBy), "")
	}
	switch or, nr := stabilityRank(o.stability), stabilityRank(n.stability); {
	case nr > or:
		d.add(st, Safe, RuleStabilityPromoted, where, "stability "+stabilityName(o.stability)+" → "+stabilityName(n.stability), "")
	case nr < or:
		d.add(st, PossiblyBreaking, RuleStabilityDemoted, where, "stability "+stabilityName(o.stability)+" → "+stabilityName(n.stability), "")
	}
}

func stabilityName(s string) string {
	if s == "" {
		return "stable"
	}
	return s
}

// hidden compares a hidden flag: hiding is possibly breaking, unhiding safe.
func (d *differ) hidden(st stability, hideRule, unhideRule, where string, o, n bool) {
	switch {
	case !o && n:
		d.add(st, PossiblyBreaking, hideRule, where, "now hidden; it still runs", "")
	case o && !n:
		d.add(st, Safe, unhideRule, where, "no longer hidden", "")
	}
}

// pluginDiscovery compares how a command finds plugins.
func (d *differ) pluginDiscovery(st stability, oc, nc *command) {
	o, n, w := oc.PluginDiscovery, nc.PluginDiscovery, oc.Name+" plugin_discovery"
	switch {
	case o != nil && n == nil:
		d.add(st, Breaking, RulePluginDiscoveryNoDelete, w, fmt.Sprintf("programs named %s<word> no longer run as sub-commands", o.Prefix), "")
	case o == nil && n != nil:
		d.add(st, Safe, RulePluginDiscoveryAdded, w, fmt.Sprintf("programs named %s<word> run as sub-commands", n.Prefix), "")
	case o != nil && o.Prefix != n.Prefix:
		d.add(st, Breaking, RulePluginDiscoveryChanged, w, fmt.Sprintf("plugin prefix %q → %q", o.Prefix, n.Prefix), "")
	}
}

// configFiles compares the configuration files a command declares, by name.
func (d *differ) configFiles(st stability, oc, nc *command) {
	for _, of := range oc.ConfigFiles {
		w := oc.Name + " config_files " + of.Name
		i := slices.IndexFunc(nc.ConfigFiles, func(f configFile) bool { return f.Name == of.Name })
		if i < 0 {
			d.add(st, Breaking, RuleConfigFileNoDelete, w, "configuration file removed or renamed", "config moved")
			continue
		}
		nf := nc.ConfigFiles[i]
		if of.As != nf.As {
			d.add(st, Breaking, RuleConfigFileAsChanged, w, onOff("the file supplies environment variables, not keys", nf.As == "env"), "")
		}
		if location(of) != location(nf) {
			d.add(st, Breaking, RuleConfigFileMoved, w, "configuration file moved: "+location(of)+" → "+location(nf), "config moved")
		}
		d.profiles(st, oc, nc, w, of.Profiles, nf.Profiles)
	}
	for _, nf := range nc.ConfigFiles {
		if !slices.ContainsFunc(oc.ConfigFiles, func(f configFile) bool { return f.Name == nf.Name }) {
			d.add(st, Safe, RuleConfigFileAdded, oc.Name+" config_files "+nf.Name, "configuration file added", "")
		}
	}
}

// location describes where a configuration file is read from and how.
func location(f configFile) string {
	s := f.Path
	if f.Discover != nil {
		s = f.Discover.Strategy + ":" + f.Discover.App + "/" + f.Discover.File
	}
	if f.Format != "" {
		s += " (" + f.Format + ")"
	}
	return s
}

// stdin compares what a command reads on stdin.
func (d *differ) stdin(st stability, oc, nc *command) {
	o, n, w := oc.Stdin, nc.Stdin, oc.Name+" stdin"
	switch {
	case o == nil && n == nil:
		return
	case o == nil:
		d.add(st, PossiblyBreaking, RuleStdinAdded, w, "now reads stdin", "a caller that leaves stdin open may block")
		return
	case n == nil:
		d.add(st, Breaking, RuleStdinNoDelete, w, "no longer reads stdin", "")
		return
	}
	if o.Format != n.Format {
		d.add(st, Breaking, RuleStdinFormatChanged, w, fmt.Sprintf("stdin format %q → %q", o.Format, n.Format), "")
	}
	switch {
	case !o.Required && n.Required:
		d.add(st, Breaking, RuleStdinRequiredAdded, w, "stdin is now required", "")
	case o.Required && !n.Required:
		d.add(st, Safe, RuleStdinRequiredRemoved, w, "stdin is no longer required", "")
	}
	if o.Type != "" && n.Type != "" && o.Type != n.Type {
		d.add(st, Breaking, RuleStdinTypeChanged, w, fmt.Sprintf("stdin type %q → %q", o.Type, n.Type), "")
	}
	if o.UnlessArgument != n.UnlessArgument {
		d.add(st, PossiblyBreaking, RuleStdinUnlessChanged, w,
			fmt.Sprintf("the argument that replaces stdin %q → %q", o.UnlessArgument, n.UnlessArgument), "")
	}
	if o.Separator != n.Separator {
		d.add(st, Breaking, RuleStdinSeparatorChanged, w, fmt.Sprintf("stdin separator %q → %q", o.Separator, n.Separator), "")
	}
	d.walk(st, in, w, o.Schema, n.Schema, nil)
}

// exits compares a command's exit statuses, by code.
func (d *differ) exits(st stability, oc, nc *command) {
	for _, oe := range oc.ExitStatus {
		w := oc.Name + " exit " + strconv.Itoa(oe.Code)
		i := slices.IndexFunc(nc.ExitStatus, func(e exitStatus) bool { return e.Code == oe.Code })
		if i < 0 {
			d.add(st, PossiblyBreaking, RuleExitStatusNoDelete, w, "exit status no longer documented", "")
			continue
		}
		ne := nc.ExitStatus[i]
		if oe.Summary != ne.Summary {
			d.add(st, PossiblyBreaking, RuleExitSummaryChanged, w,
				fmt.Sprintf("summary %q → %q", oe.Summary, ne.Summary), "a reword can't be told from a new meaning")
		}
		switch {
		case oe.Name == ne.Name:
		case oe.Name == "":
			d.add(st, Safe, RuleExitNameAdded, w, "named "+ne.Name, "")
		case ne.Name == "":
			d.add(st, PossiblyBreaking, RuleExitNameChanged, w, "name "+oe.Name+" removed", "")
		default:
			d.add(st, PossiblyBreaking, RuleExitNameChanged, w, "name "+oe.Name+" → "+ne.Name, "")
		}
		switch {
		case oe.Retryable && !ne.Retryable:
			d.add(st, PossiblyBreaking, RuleExitRetryableRemoved, w, "no longer retryable", "")
		case !oe.Retryable && ne.Retryable:
			d.add(st, Safe, RuleExitRetryableAdded, w, "now retryable", "")
		}
		d.output(st, w+" output", oe.Output, ne.Output)
	}
	for _, ne := range nc.ExitStatus {
		if !slices.ContainsFunc(oc.ExitStatus, func(e exitStatus) bool { return e.Code == ne.Code }) {
			d.add(st, Safe, RuleExitStatusAdded, oc.Name+" exit "+strconv.Itoa(ne.Code), "exit status documented", "")
		}
	}
}

// output compares a declared output, a command's or an exit status's.
func (d *differ) output(st stability, where string, o, n any) {
	switch {
	case o == nil && n == nil:
	case n == nil:
		d.add(st, Breaking, RuleOutputNoDelete, where, "declared output removed", "")
	case o == nil:
		d.add(st, Safe, RuleOutputAdded, where, "output declared", "")
	default:
		d.walk(st, out, where, o, n, nil)
	}
}

// jsonEqual reports whether two JSON documents hold the same value.
func jsonEqual(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return deepEqual(x, y)
}

// sortedJoin joins a copy of names, sorted, with commas.
func sortedJoin(names []string) string {
	s := slices.Clone(names)
	slices.Sort(s)
	return strings.Join(s, ",")
}
