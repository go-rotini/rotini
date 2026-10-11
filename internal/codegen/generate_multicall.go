package codegen

import (
	"fmt"
	"strings"
)

// multicallOf reads a command's `multicall`: true, or an object with prefix and complete. on is
// false when it is unset or false.
func multicallOf(c *Command) (on bool, prefix, complete string) {
	if c == nil {
		return false, "", ""
	}
	switch v := c.Multicall.(type) {
	case bool:
		return v, "", ""
	case map[string]any:
		prefix, _ = v["prefix"].(string)
		complete, _ = v["complete"].(string)
		return true, prefix, complete
	case *Multicall:
		if v == nil {
			return false, "", ""
		}
		return true, v.Prefix, v.Complete
	case Multicall:
		return true, v.Prefix, v.Complete
	}
	return false, "", ""
}

// multicallLiteral renders the root's Multicall field, or "" when multicall is off.
func multicallLiteral(gp *program) string {
	on, prefix, complete := gp.multicall()
	if !on {
		return ""
	}
	var fields []string
	if prefix != "" {
		fields = append(fields, fmt.Sprintf("Prefix: %q", prefix))
	}
	if complete != "" {
		fields = append(fields, fmt.Sprintf("Complete: %q", complete))
	}
	return fmt.Sprintf("Multicall: &%s.MulticallDef{%s},\n", rotiniPkgName, strings.Join(fields, ", "))
}

// multicall is the root's multicall setting; on is false when it is off or there is no spec.
func (gp *program) multicall() (on bool, prefix, complete string) {
	if gp.spec == nil {
		return false, "", ""
	}
	return multicallOf(&gp.spec.Command)
}

// contractMulticall is how the program dispatches on the name it was invoked as. Both fields
// are empty for busybox style.
type contractMulticall struct {
	Prefix   string `json:"prefix,omitempty"`
	Complete string `json:"complete,omitempty"`
}
