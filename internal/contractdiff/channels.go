package contractdiff

import "fmt"

// channels compares the declared outcome channels, matched by name. Their records are lines a
// reader parses, so a record's schema is compared as an output is: a channel removed, or a
// record losing a field, breaks the reader.
func (d *differ) channels() {
	st := d.rootStability()
	for _, oc := range d.old.Channels {
		where := "channel " + oc.Name
		nc := d.new.channel(oc.Name)
		if nc == nil {
			d.add(st, Breaking, RuleChannelNoDelete, where, "channel removed", "its records are no longer written")
			continue
		}
		if oc.Class != nc.Class {
			sev := PossiblyBreaking
			if oc.Class == "error" || nc.Class == "error" {
				sev = Breaking // whether a record fails the run, and adds an error line
			}
			d.add(st, sev, RuleChannelClassChanged, where, fmt.Sprintf("class %s → %s", oc.Class, nc.Class), "")
		}
		d.walk(st, out, where, stripLocal(oc.Schema), stripLocal(nc.Schema), nil)
	}
	for _, nc := range d.new.Channels {
		if d.old.channel(nc.Name) == nil {
			d.add(st, Safe, RuleChannelAdded, "channel "+nc.Name, "channel declared", "")
		}
	}
}

// channel returns the channel named name, or nil.
func (doc *document) channel(name string) *channel {
	for i := range doc.Channels {
		if doc.Channels[i].Name == name {
			return &doc.Channels[i]
		}
	}
	return nil
}

// channelUser is the command a channel's schema counts as used by: a stand-in named after the
// channel, as settled as the root command, since channels are program-wide.
func (doc *document) channelUser(ch channel) *command {
	c := &command{Name: "channel " + ch.Name, Path: []string{}}
	if root := doc.byPath[pathKey(nil)]; root != nil {
		c.Stability = root.Stability
	}
	return c
}

// rootStability is the old contract's root command's stability, which a channel shares.
func (d *differ) rootStability() stability {
	if root := d.old.byPath[pathKey(nil)]; root != nil {
		return stability(root.Stability)
	}
	return ""
}
