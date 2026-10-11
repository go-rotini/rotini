package codegen

import (
	"strconv"
	"strings"
)

// Completion messages: lines a shell shows while a value is being completed and there is
// nothing to offer. The conf's completion feature turns them on with `messages`, and generate
// bakes each input's line into its Completion literal, so the runtime never reads the conf.

// completionMessages returns the completion feature's `messages` mode ("declared" or "all")
// and its `messages_env`, or "" when the feature is off or declares no messages.
func completionMessages(conf *Conf) (mode, env string) {
	if conf == nil || !featureEnabled(conf, "completion") {
		return "", ""
	}
	f := conf.Generate.featureOf("completion")
	if f == nil {
		return "", ""
	}
	return f.Messages, f.MessagesEnv
}

// resolveCompletionMessages sets, or clears, the static message of every flag and argument in
// the tree for the conf's mode: the spec's `complete.message` under "declared"; that, else one
// derived from the input's summary, under "all"; none when messages are off. Hidden inputs and
// flags that take no value get none.
func (p *program) resolveCompletionMessages() {
	mode, _ := completionMessages(p.conf)
	apply := func(in *Inputs) {
		if in == nil {
			return
		}
		for i := range in.Flags {
			f := &in.Flags[i]
			msg := ""
			if !f.Hidden && flagDisplayType(f.Schema) != "" {
				msg = staticMessage(mode, f.Schema, f.Summary, flagMessageName(*f))
			}
			setCompletionMessage(&f.Schema, msg)
		}
		for i := range in.Arguments {
			a := &in.Arguments[i]
			msg := ""
			if !a.Hidden {
				msg = staticMessage(mode, a.Schema, a.Summary, argMessageName(*a))
			}
			setCompletionMessage(&a.Schema, msg)
		}
	}
	apply(p.rootInputs)
	var walk func([]rnode)
	walk = func(nodes []rnode) {
		for _, n := range nodes {
			apply(n.inputs)
			walk(n.children)
		}
	}
	walk(p.tree)
}

// staticMessage is one input's message under mode.
func staticMessage(mode string, schema *InputSchema, summary, name string) string {
	declared := ""
	if schema != nil && schema.Complete != nil {
		declared = schema.Complete.Message
	}
	switch {
	case mode == "" || (mode == "declared" && declared == ""):
		return ""
	case declared != "":
		return declared
	case summary == "":
		return ""
	}
	return name + ": " + summary
}

// flagMessageName names a flag in a derived message as help does: its long identifier and
// value type, `--replicas int`.
func flagMessageName(f FlagInput) string {
	ids := flagIdentifiers(f)
	name := f.Name
	for _, id := range ids {
		if strings.HasPrefix(id, "--") {
			name = id
			break
		}
	}
	if name == f.Name && len(ids) > 0 {
		name = ids[0]
	}
	return name + " " + flagDisplayType(f.Schema)
}

// argMessageName names an argument in a derived message: its placeholder when it declares one,
// else `<name>`.
func argMessageName(a ArgumentInput) string {
	if a.Schema != nil && a.Schema.Placeholder != "" {
		return a.Schema.Placeholder
	}
	return "<" + a.Name + ">"
}

// setCompletionMessage stores msg as the input's resolved message, creating the complete hint
// only when there is a message to hold.
func setCompletionMessage(schema **InputSchema, msg string) {
	switch {
	case *schema == nil && msg == "":
	case *schema == nil:
		*schema = &InputSchema{Complete: &CompletionHint{Message: msg}}
	case (*schema).Complete == nil && msg == "":
	case (*schema).Complete == nil:
		(*schema).Complete = &CompletionHint{Message: msg}
	default:
		(*schema).Complete.Message = msg
	}
}

// completionMessagesLiteral renders the Definition's CompletionMessages field, or "" when
// messages are off, so a CLI without them omits the field.
func completionMessagesLiteral(conf *Conf) string {
	mode, env := completionMessages(conf)
	if mode == "" {
		return ""
	}
	out := "CompletionMessages: &" + rotiniPkgName + ".CompletionMessagesDef{"
	if env != "" {
		out += "Env: " + strconv.Quote(env)
	}
	return out + "},\n"
}

// completionDescriptionsLiteral renders the Definition's CompletionDescriptions field, or ""
// when the conf declares no `descriptions_env`, so a CLI without one omits the field.
func completionDescriptionsLiteral(conf *Conf) string {
	env := completionScriptEnvs(conf).descriptions
	if env == "" {
		return ""
	}
	return "CompletionDescriptions: &" + rotiniPkgName + ".CompletionDescriptionsDef{Env: " + strconv.Quote(env) + "},\n"
}
