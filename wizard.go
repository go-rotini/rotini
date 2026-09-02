package rotini

import (
	"context"
	"errors"
	"fmt"
	"maps"
)

// ErrWizardBack is returned by a step's Ask to move back to the previous step
// that actually ran (skipped steps are skipped going back too). At the first
// step it re-asks rather than exiting, so "back" is never a way out.
var ErrWizardBack = errors.New("rotini: wizard: go back")

// ErrWizardCanceled reports that a step asked to abandon the flow. A Wizard
// returns it along with the answers gathered so far, so a caller can report what
// was and was not collected.
var ErrWizardCanceled = UsageError(errors.New("rotini: wizard: canceled"))

// WizardStep is one question in a guided flow.
type WizardStep struct {
	// Key names the answer in the result map. Steps with the same key overwrite.
	Key string
	// Ask collects the answer. It receives the answers gathered so far, so a step
	// can phrase itself — or validate — against earlier ones. Returning
	// [ErrWizardBack] steps backwards; [ErrWizardCanceled] abandons the flow.
	Ask func(ctx context.Context, answers map[string]string) (string, error)
	// When gates the step: a step whose When returns false is skipped, which is
	// how a flow branches. Nil means always ask.
	When func(answers map[string]string) bool
}

// Wizard sequences steps into a guided flow, with branching and back navigation.
//
// It owns no streams and does no asking: a step's Ask does that, typically with a [Prompt],
// [Select] or [Confirm] it constructs itself. That keeps the Wizard pure orchestration —
// testable with plain funcs, and equally usable for answers that come from somewhere other
// than a terminal.
//
//	answers, err := rotini.NewWizard().
//	    Step("name", func(ctx context.Context, _ map[string]string) (string, error) {
//	        return rotini.NewPrompt(rtx.Stdin, rtx.Stdout).WithLabel("Name").Ask(ctx)
//	    }).
//	    Add(rotini.WizardStep{
//	        Key:  "region",
//	        When: func(a map[string]string) bool { return a["deploy"] == "yes" },
//	        Ask:  askRegion,
//	    }).
//	    Run(ctx)
//
// The zero value is usable: a Wizard with no steps returns an empty answer set.
type Wizard struct {
	steps []WizardStep
}

// NewWizard returns an empty flow.
func NewWizard() *Wizard { return &Wizard{} }

// Add appends a step.
func (w *Wizard) Add(step WizardStep) *Wizard {
	if step.Ask != nil && step.Key != "" {
		w.steps = append(w.steps, step)
	}
	return w
}

// Step appends an unconditional step — the common case, without the struct.
func (w *Wizard) Step(key string, ask func(ctx context.Context, answers map[string]string) (string, error)) *Wizard {
	return w.Add(WizardStep{Key: key, Ask: ask})
}

// Run walks the steps in order and returns the collected answers.
//
// A step returning [ErrWizardBack] returns to the previous step that ran, so going back over a
// skipped branch skips it again. Any other error ends the flow, with the answers gathered so
// far returned alongside it so a caller can persist partial progress.
func (w *Wizard) Run(ctx context.Context) (map[string]string, error) {
	answers := map[string]string{}
	var history []int // indexes of the steps that actually ran, for "back"

	for i := 0; i < len(w.steps); {
		if err := ctx.Err(); err != nil {
			return maps.Clone(answers), fmt.Errorf("rotini: wizard: %w", err)
		}
		step := w.steps[i]
		if step.When != nil && !step.When(answers) {
			i++
			continue
		}

		value, err := step.Ask(ctx, answers)
		switch {
		case errors.Is(err, ErrWizardBack):
			if len(history) == 0 {
				continue // already at the first step: re-ask rather than exit
			}
			previous := history[len(history)-1]
			history = history[:len(history)-1]
			delete(answers, w.steps[previous].Key) // the answer is being re-taken
			i = previous
			continue
		case err != nil:
			return maps.Clone(answers), err
		}

		answers[step.Key] = value
		history = append(history, i)
		i++
	}
	return answers, nil
}
