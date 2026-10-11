package codegen

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
)

// This file holds the rules for planned deprecations (deprecated_since, removed_in) and the
// stream marker, and the release check `rotini validate --release` runs.

// lifecycleItem is one command or input that can be deprecated, with its planned lifecycle.
type lifecycleItem struct {
	ptr, path  string // the item's JSON pointer, and its command's path
	subject    string // how a message names an input (`flag "--conf"`); "" for the command itself
	deprecated string
	since      string
	removedIn  string
	ids        []string          // deprecated_identifiers
	removedIDs map[string]string // deprecated_identifiers_removed_in
}

func (it lifecycleItem) problem(msg, key string) *problem {
	ptr := it.ptr
	if key != "" {
		ptr += "/" + key
	}
	if it.subject != "" {
		msg = it.subject + ": " + msg
	}
	return &problem{kind: "spec", ptr: ptr, loc: "command " + it.path, msg: msg}
}

// eachLifecycleAt visits a command and each of its inputs that can carry a lifecycle.
func eachLifecycleAt(c *Command, path, ptr string, visit func(lifecycleItem)) {
	visit(lifecycleItem{
		ptr: ptr, path: path, deprecated: c.Deprecated, since: c.DeprecatedSince,
		removedIn: c.RemovedIn, ids: c.DeprecatedIdentifiers, removedIDs: c.DeprecatedIdentifiersRemovedIn,
	})
	for i, f := range c.Flags {
		visit(lifecycleItem{
			ptr: fmt.Sprintf("%s/flags/%d", ptr, i), path: path, subject: fmt.Sprintf("flag %q", flagIdentifiers(f)[0]),
			deprecated: f.Deprecated, since: f.DeprecatedSince, removedIn: f.RemovedIn,
			ids: f.DeprecatedIdentifiers, removedIDs: f.DeprecatedIdentifiersRemovedIn,
		})
	}
	for i, a := range c.Arguments {
		visit(lifecycleItem{
			ptr: fmt.Sprintf("%s/arguments/%d", ptr, i), path: path, subject: fmt.Sprintf("argument %q", a.Name),
			deprecated: a.Deprecated, since: a.DeprecatedSince, removedIn: a.RemovedIn,
		})
	}
	for i, e := range c.Env {
		visit(lifecycleItem{
			ptr: fmt.Sprintf("%s/env/%d", ptr, i), path: path, subject: fmt.Sprintf("env %q", e.Name),
			deprecated: e.Deprecated, since: e.DeprecatedSince, removedIn: e.RemovedIn,
		})
	}
	for i, cfg := range c.Config {
		visit(lifecycleItem{
			ptr: fmt.Sprintf("%s/config/%d", ptr, i), path: path, subject: fmt.Sprintf("config %q", cfg.Name),
			deprecated: cfg.Deprecated, since: cfg.DeprecatedSince, removedIn: cfg.RemovedIn,
		})
	}
}

// lintDeprecationLifecycle checks deprecated_since and removed_in: each needs a deprecation
// message, and a removal comes after the deprecation. A per-identifier removal names a
// deprecated identifier and comes after the deprecation too.
func lintDeprecationLifecycle(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachLifecycleAt(c, path, ptr, func(it lifecycleItem) {
			for _, k := range []struct{ key, value string }{{"deprecated_since", it.since}, {"removed_in", it.removedIn}} {
				if k.value != "" && it.deprecated == "" {
					problems = append(problems, it.problem(fmt.Sprintf("sets %#q without `deprecated`; say why it is deprecated, or drop %#q", k.key, k.key), k.key))
				}
			}
			since, hasSince := parseSemver(it.since)
			if removed, ok := parseSemver(it.removedIn); ok && hasSince && !since.olderThan(removed) {
				problems = append(problems, it.problem(fmt.Sprintf("removed_in %s is not later than deprecated_since %s", it.removedIn, it.since), "removed_in"))
			}
			for _, id := range slices.Sorted(maps.Keys(it.removedIDs)) {
				switch removed, ok := parseSemver(it.removedIDs[id]); {
				case !slices.Contains(it.ids, id):
					problems = append(problems, it.problem(fmt.Sprintf("deprecated_identifiers_removed_in names %q, which deprecated_identifiers does not list; deprecate it first", id), "deprecated_identifiers_removed_in"))
				case ok && hasSince && !since.olderThan(removed):
					problems = append(problems, it.problem(fmt.Sprintf("deprecated_identifiers_removed_in plans %q for %s, which is not later than deprecated_since %s", id, it.removedIDs[id], it.since), "deprecated_identifiers_removed_in"))
				}
			}
		})
	})
	return problems
}

// lintOutputStream rejects output_stream on a command that declares no output: the marker
// says how the declared output is written.
func lintOutputStream(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.OutputStream && c.Output == nil && c.Ref == "" { // on a $ref node, lintRefNodeKeys reports it
			problems = append(problems, &problem{kind: "spec", ptr: ptr + "/output_stream", loc: "command " + path,
				msg: "sets `output_stream` but declares no `output`; declare the shape of one item as `output`, or drop `output_stream`"})
		}
	})
	return problems
}

// releaseProblems reports every command, input and deprecated identifier the spec still
// declares whose planned removal (removed_in) is at or below release.
func releaseProblems(spec *Spec, release string) []error {
	rel, ok := parseSemver(release)
	if !ok {
		return nil
	}
	due := func(v string) bool {
		removed, ok := parseSemver(v)
		return ok && !rel.olderThan(removed)
	}
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachLifecycleAt(c, path, ptr, func(it lifecycleItem) {
			if due(it.removedIn) {
				since := ""
				if it.since != "" {
					since = " (deprecated since " + it.since + ")"
				}
				problems = append(problems, it.problem(fmt.Sprintf("is planned for removal in %s%s; remove it before releasing %s", it.removedIn, since, release), "removed_in"))
			}
			for _, id := range slices.Sorted(maps.Keys(it.removedIDs)) {
				if due(it.removedIDs[id]) {
					problems = append(problems, it.problem(fmt.Sprintf("%q is planned for removal in %s; remove it from the spec before releasing %s", id, it.removedIDs[id], release), "deprecated_identifiers_removed_in"))
				}
			}
		})
	})
	return problems
}

// releaseCheck runs [releaseProblems] over the spec and every local spec it composes, each
// positioned in its own file. A mod:// child belongs to another module's release, so it is
// skipped.
func releaseCheck(rs *reconciledSpec, release string, seen map[string]bool) []error {
	if rs == nil || rs.spec == nil {
		return nil
	}
	seen[rs.path] = true
	problems := releaseProblems(rs.spec, release)
	locateProblems(problems, rs.path, rs.locate)
	for _, ref := range localSpecRefs(&rs.spec.Command) {
		locator, err := locateRef(filepath.Dir(rs.path), ref)
		if err != nil || seen[locator] {
			continue
		}
		seen[locator] = true
		if child, err := reconcileSpec(displayPath(locator)); err == nil {
			problems = append(problems, releaseCheck(child, release, seen)...)
		}
	}
	return problems
}

// errReleaseFormat is returned for a release that is not X.Y.Z.
var errReleaseFormat = errors.New("want a release as X.Y.Z, such as 2.0.0")

// CheckRelease rejects a release the check could not compare against, naming where it came
// from (the --release flag, or the conf's validate.release_env variable).
func CheckRelease(release, source string) error {
	if _, ok := parseSemver(release); !ok {
		return fmt.Errorf("%s %q: %w", source, release, errReleaseFormat)
	}
	return nil
}

// ReleaseEnv returns the environment variable the conf beside specPath (or at confPath) names
// in validate.release_env, or "" when it names none. A conf that can't be read names none;
// validate then reports the problem itself.
func ReleaseEnv(specPath, confPath string) string {
	rc, err := reconcileConf(specPath, confPath)
	if err != nil || rc.conf.Validate == nil {
		return ""
	}
	return rc.conf.Validate.ReleaseEnv
}
