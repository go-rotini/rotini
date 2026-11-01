package codegen

import (
	"fmt"
	"reflect"
	"strings"
)

// This file generates a spec's declared outcome channels (`channels:`): a record type from each
// channel's schema, a rotini.Channel handle for it, the Definition's ChannelDef list, and an
// Error method on an error channel's record type.

// channelRecordType is the generated record type of the channel named name: SkippedRecord.
func channelRecordType(name string) string { return toPascalCase(name) + "Record" }

// channelHandle is the generated handle of the channel named name: SkippedChannel.
func channelHandle(name string) string { return toPascalCase(name) + "Channel" }

// channelClassConst is the rotini.ChannelClass constant for a spec class name.
func channelClassConst(class string) string {
	switch class {
	case "success":
		return rotiniPkgName + ".ChannelClassSuccess"
	case "warning":
		return rotiniPkgName + ".ChannelClassWarning"
	case "error":
		return rotiniPkgName + ".ChannelClassError"
	}
	return rotiniPkgName + ".ChannelClassInfo"
}

// channelRecordDoc is the schema a channel's record type is generated from: its schema, with
// the channel's summary as the type's doc comment when the schema has no description.
func channelRecordDoc(c Channel) Schema {
	s := c.Schema
	if s.Description == "" {
		s.Description = channelDocLine(c)
	}
	return s
}

// channelDocLine says what one of c's records is, for its type's doc comment.
func channelDocLine(c Channel) string {
	if summary := channelSummary(c); summary != "" {
		return fmt.Sprintf("%s is a record of the %q channel: %s", channelRecordType(c.Name), c.Name, summary)
	}
	return fmt.Sprintf("%s is a record of the %q channel.", channelRecordType(c.Name), c.Name)
}

// channelSummary is c's summary as one comment sentence, or "" when it has none.
func channelSummary(c Channel) string {
	summary := strings.Join(strings.Fields(commentText(c.Summary)), " ")
	if summary != "" && !strings.ContainsAny(summary[len(summary)-1:], ".!?") {
		summary += "."
	}
	return summary
}

// channelErrorMethods renders the Error method of each error channel's record type, which
// returns its `message` property (lint requires one), so the type implements error as
// rotini.ChannelClassError requires.
func channelErrorMethods(channels []Channel) string {
	var b strings.Builder
	for _, c := range channels {
		if c.Class != "error" {
			continue
		}
		typ := channelRecordType(c.Name)
		fmt.Fprintf(&b, "\n\n// Error returns the record's message, so a %s is an error.\nfunc (r %s) Error() string { return r.Message }", typ, typ)
	}
	return b.String()
}

// channelHandlesDecl renders the handle of each of the spec's own channels, or "" when it
// declares none.
func channelHandlesDecl(channels []Channel) string {
	if len(channels) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range channels {
		if i > 0 {
			b.WriteString("\n\n")
		}
		h := channelHandle(c.Name)
		if summary := channelSummary(c); summary != "" {
			fmt.Fprintf(&b, "// %s is the %q channel: %s\n", h, c.Name, summary)
		} else {
			fmt.Fprintf(&b, "// %s is the %q channel. Record into it with rtx.Record.\n", h, c.Name)
		}
		fmt.Fprintf(&b, "var %s = %s.NewChannel[%s](%q, %s)", h, rotiniPkgName, channelRecordType(c.Name), c.Name, channelClassConst(c.Class))
	}
	b.WriteString("\n")
	return b.String()
}

// scopedChannel is a declared channel and the spec it comes from (nil scope = the root spec).
type scopedChannel struct {
	channel Channel
	scope   *schemaScope
}

// allChannels lists the program's declared channels: the root spec's, then those of each spec
// it composes, in tree order. A name already listed is left out; lint rejects two specs
// declaring one name.
func (p *program) allChannels() []scopedChannel {
	var out []scopedChannel
	seen := map[string]bool{}
	add := func(scope *schemaScope, channels []Channel) {
		for _, c := range channels {
			if !seen[c.Name] {
				seen[c.Name] = true
				out = append(out, scopedChannel{c, scope})
			}
		}
	}
	add(nil, p.channels)
	var walk func(nodes []rnode)
	walk = func(nodes []rnode) {
		for i := range nodes {
			if s := nodes[i].scope; s != nil {
				add(s, s.channels)
			}
			walk(nodes[i].children)
		}
	}
	walk(p.tree)
	return out
}

// composedChannelClash refuses a channel name two specs of the program declare: records are
// told apart by name, and each spec generates its own record type, so recording into both would
// be a fault. A spec composed twice declares its channels once.
func (p *program) composedChannelClash() error {
	type owner struct {
		spec    string
		channel Channel
	}
	first := map[string]owner{}
	check := func(spec string, channels []Channel) error {
		for _, c := range channels {
			prev, ok := first[c.Name]
			switch {
			case !ok:
				first[c.Name] = owner{spec, c}
			case prev.spec != spec || !reflect.DeepEqual(prev.channel, c):
				return fmt.Errorf("the channel %q is declared by spec %q and by spec %q; a program's channels need distinct names, so rename one", c.Name, prev.spec, spec)
			}
		}
		return nil
	}
	if err := check(p.rootName, p.channels); err != nil {
		return err
	}
	var walk func(nodes []rnode) error
	walk = func(nodes []rnode) error {
		for i := range nodes {
			if s := nodes[i].scope; s != nil {
				if err := check(s.name, s.channels); err != nil {
					return err
				}
			}
			if err := walk(nodes[i].children); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(p.tree)
}

// channelsLiteral renders the Definition's Channels field, or "" when the program declares
// none.
func channelsLiteral(p *program) string {
	all := p.allChannels()
	if len(all) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Channels: []%s.ChannelDef{\n", rotiniPkgName)
	for _, c := range all {
		fmt.Fprintf(&b, "{Name: %q, Class: %s},\n", c.channel.Name, channelClassConst(c.channel.Class))
	}
	b.WriteString("},\n")
	return b.String()
}

// contractChannel is one declared channel in the contract document.
type contractChannel struct {
	Name    string `json:"name"`
	Class   string `json:"class"`
	Summary string `json:"summary,omitempty"`
	Schema  any    `json:"schema"`
}

// contractChannels lists the program's declared channels for the contract document.
func (p *program) contractChannels(defs *contractDefs) []contractChannel {
	all := p.allChannels()
	out := make([]contractChannel, 0, len(all))
	for _, c := range all {
		out = append(out, contractChannel{
			Name: c.channel.Name, Class: c.channel.Class, Summary: c.channel.Summary,
			Schema: defs.schemaDoc(c.scope, c.channel.Schema),
		})
	}
	return out
}
