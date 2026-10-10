package codegen

import (
	"fmt"
	"strings"
)

// keySite is one place a configuration-file key is read: a config input, or a flag's or
// argument's configuration fallback.
type keySite struct {
	seq     int // pre-order position, so "later" is stable
	key     string
	typ     string
	pin     string // the config_files entry a config input is pinned to; "" reads every file
	ptr     string
	path    string
	channel string
	name    string
}

// keyNode is one command in the walk: its key sites, its parent, and the extent of its
// subtree in walk order.
type keyNode struct {
	cmd    *Command
	parent int
	end    int // one past the last descendant's index
	sites  []keySite
}

// lintConfigKeyCollisions rejects configuration keys that one file can't satisfy: the same key
// read with two types, and a key that is also the parent of another (`db` and `db.host`)
// unless the parent is a map or a named object schema, which holds both.
//
// Keys are compared within two scopes. Each command chain reads its frames from the invoked
// command's files, so every chain is one scope; it is checked even before any config_files
// entry exists, so a clash is reported when it is written. And each config_files entry is read
// by its declaring command's ancestors and its whole subtree, so siblings that disagree about a
// shared key are caught too. A config input pinned to another file is out of a scope.
func lintConfigKeyCollisions(spec *Spec) []error {
	nodes := keyNodes(spec)
	k := &keyChecker{schemas: spec.Command.Schemas, seen: map[[2]int]bool{}, said: map[string]bool{}}
	for i := range nodes {
		var chain []keySite
		for _, a := range keyAncestors(nodes, i) {
			chain = append(chain, nodes[a].sites...)
		}
		k.check(append(chain, nodes[i].sites...), false)
	}
	for i := range nodes {
		for _, cf := range nodes[i].cmd.ConfigFiles {
			var scope []keySite
			add := func(sites []keySite) {
				for _, s := range sites {
					if s.pin == "" || s.pin == cf.Name {
						scope = append(scope, s)
					}
				}
			}
			for _, a := range keyAncestors(nodes, i) {
				add(nodes[a].sites)
			}
			for d := i; d < nodes[i].end; d++ {
				add(nodes[d].sites)
			}
			k.check(scope, true)
		}
	}
	return k.problems
}

// keyNodes walks the command tree in pre-order and collects each command's key sites.
func keyNodes(spec *Spec) []keyNode {
	schemas := spec.Command.Schemas
	var nodes []keyNode
	seq := 0
	var walk func(c *Command, parent int, path, ptr string)
	walk = func(c *Command, parent int, path, ptr string) {
		idx := len(nodes)
		nodes = append(nodes, keyNode{cmd: c, parent: parent})
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			key, pin := "", ""
			switch channel {
			case "flag", "argument":
				key = flagReconKey(name, schema)
			case "config":
				key = configKey(ConfigInput{Name: name, Schema: schema})
				if schema != nil {
					pin = schema.File
				}
			}
			if key == "" {
				return
			}
			typ := getSchemaType(schema)
			if t := namedScalarType(typ, schemas); t != "" {
				typ = t
			}
			nodes[idx].sites = append(nodes[idx].sites, keySite{seq: seq, key: key, typ: typ, pin: pin, ptr: ptr, path: path, channel: channel, name: name})
			seq++
		})
		for i := range c.Commands {
			child := &c.Commands[i]
			seg := child.Name
			if seg == "" {
				seg = child.Ref
			}
			walk(child, idx, path+"/"+seg, fmt.Sprintf("%s/commands/%d", ptr, i))
		}
		nodes[idx].end = len(nodes)
	}
	root := spec.Command.Name
	if root == "" {
		root = "(root)"
	}
	walk(&spec.Command, -1, root, rootPointer)
	return nodes
}

// keyAncestors returns the indexes of node i's ancestors, nearest first.
func keyAncestors(nodes []keyNode, i int) []int {
	var out []int
	for p := nodes[i].parent; p >= 0; p = nodes[p].parent {
		out = append(out, p)
	}
	return out
}

// keyChecker compares key sites scope by scope, reporting each clash once.
type keyChecker struct {
	schemas  map[string]Schema
	seen     map[[2]int]bool // site pairs already reported
	said     map[string]bool // one report per later site and clash, however many sites share it
	problems []error
}

// check compares every pair of sites in one scope. oneFile says the scope is a single file;
// otherwise two sites pinned to different files never meet.
func (k *keyChecker) check(sites []keySite, oneFile bool) {
	for _, a := range sites {
		for _, b := range sites {
			if a.seq >= b.seq || k.seen[[2]int{a.seq, b.seq}] {
				continue
			}
			if !oneFile && a.pin != "" && b.pin != "" && a.pin != b.pin {
				continue
			}
			msg, clash := keyClash(a, b, k.schemas)
			if msg == "" || k.said[fmt.Sprint(b.seq, clash)] {
				continue
			}
			k.seen[[2]int{a.seq, b.seq}], k.said[fmt.Sprint(b.seq, clash)] = true, true
			k.problems = append(k.problems, inputProblem(b.ptr, b.path, b.channel, b.name, msg))
		}
	}
}

// keyClash reports why two key sites can't share one file, phrased for the later site b, and
// names the clash so it is reported once per site; msg is "" when they can share one.
func keyClash(a, b keySite, schemas map[string]Schema) (msg, clash string) {
	other := fmt.Sprintf("%s %q, command %s", a.channel, a.name, a.path)
	switch {
	case a.key == b.key && a.typ != b.typ:
		return fmt.Sprintf("config key %q is read as %s by %s %q (command %s) and as %s here, so one file can't hold both; use one type, or rename one key",
			b.key, a.typ, a.channel, a.name, a.path, b.typ), "type " + a.typ
	case strings.HasPrefix(b.key, a.key+".") && !nests(a.typ, schemas):
		return fmt.Sprintf("config key %q nests under key %q (%s), which is read as %s, so one file can't hold both; make %q a map, or rename one key",
			b.key, a.key, other, a.typ, a.key), "under " + a.key
	case strings.HasPrefix(a.key, b.key+".") && !nests(b.typ, schemas):
		return fmt.Sprintf("config key %q is read as %s here, but key %q (%s) nests under it, so one file can't hold both; make %q a map, or rename one key",
			b.key, b.typ, a.key, other, b.key), "parent"
	}
	return "", ""
}

// nests reports whether a value of typ can hold nested keys: a map, or a named object schema.
func nests(typ string, schemas map[string]Schema) bool {
	if strings.HasPrefix(typ, "map[") {
		return true
	}
	s, ok := schemas[typ]
	return ok && (s.Type == "object" || len(s.Properties) > 0)
}
