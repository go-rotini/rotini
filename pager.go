package rotini

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Pager sends long output through the user's pager ($PAGER, else `less`), and passes it
// straight through when there is no terminal to page into — so `mycli list | grep x` and a CI
// job both get plain text on stdout. Like [Spinner] the decision comes from the writer, and
// [Pager.WithEnabled] overrides it.
//
// The zero value is not usable; start from [NewPager].
type Pager struct {
	w       io.Writer
	command string
	enabled bool
}

// NewPager returns a pager writing to w.
func NewPager(w io.Writer) *Pager {
	return &Pager{w: w, enabled: animates(w)}
}

// WithCommand sets the pager command line (e.g. "less -R"). Empty — the default —
// uses $PAGER, falling back to `less -R`.
func (p *Pager) WithCommand(command string) *Pager {
	p.command = command
	return p
}

// WithEnabled forces paging on or off, overriding the terminal decision.
func (p *Pager) WithEnabled(enabled bool) *Pager {
	p.enabled = enabled
	return p
}

// Page writes text through the pager, or straight to the writer when paging is
// off. A pager that cannot be started is NOT an error: the text still reaches the
// writer, because failing to display output is worse than displaying it unpaged.
func (p *Pager) Page(ctx context.Context, text string) error {
	if !p.enabled {
		return p.passthrough(text)
	}
	name, args := p.resolve()
	if name == "" {
		return p.passthrough(text)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout, cmd.Stderr = p.w, p.w
	if err := cmd.Run(); err != nil {
		return p.passthrough(text)
	}
	return nil
}

// passthrough writes the text unpaged, newline-terminated.
func (p *Pager) passthrough(text string) error {
	if text == "" {
		return nil
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if _, err := io.WriteString(p.w, text); err != nil {
		return fmt.Errorf("rotini: write output: %w", err)
	}
	return nil
}

// resolve picks the pager command: the configured one, then $PAGER, then `less -R`
// (-R so ANSI styling survives). A $PAGER of "cat" or "" means the user asked for
// no paging, and resolve reports that as no command.
func (p *Pager) resolve() (name string, args []string) {
	command := p.command
	if command == "" {
		command = os.Getenv("PAGER")
	}
	if command == "" {
		command = "less -R"
	}
	fields := strings.Fields(command)
	if len(fields) == 0 || fields[0] == "cat" {
		return "", nil
	}
	return fields[0], fields[1:]
}
