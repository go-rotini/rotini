package rotini

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"
)

// answer builds a step that returns a fixed value and records that it ran.
func answer(ran *[]string, key, value string) WizardStep {
	return WizardStep{Key: key, Ask: func(context.Context, map[string]string) (string, error) {
		*ran = append(*ran, key)
		return value, nil
	}}
}

func TestWizard_collectsAnswersInOrder(t *testing.T) {
	var ran []string
	got, err := NewWizard().
		Add(answer(&ran, "name", "acme")).
		Add(answer(&ran, "region", "us-east")).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"name": "acme", "region": "us-east"}; !reflect.DeepEqual(got, want) {
		t.Errorf("answers = %v, want %v", got, want)
	}
	if want := []string{"name", "region"}; !reflect.DeepEqual(ran, want) {
		t.Errorf("ran %v, want %v", ran, want)
	}
}

// A step sees the answers gathered so far, which is what makes branching and
// context-aware phrasing possible.
func TestWizard_stepSeesEarlierAnswers(t *testing.T) {
	var seen map[string]string
	_, err := NewWizard().
		Step("first", func(context.Context, map[string]string) (string, error) { return "1", nil }).
		Step("second", func(_ context.Context, answers map[string]string) (string, error) {
			seen = map[string]string{}
			maps.Copy(seen, answers)
			return "2", nil
		}).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seen["first"] != "1" {
		t.Errorf("second step saw %v, want the first answer", seen)
	}
}

// When gates a step — that is how a flow branches.
func TestWizard_whenSkipsSteps(t *testing.T) {
	var ran []string
	got, err := NewWizard().
		Add(answer(&ran, "deploy", "no")).
		Add(WizardStep{
			Key:  "region",
			When: func(a map[string]string) bool { return a["deploy"] == "yes" },
			Ask:  func(context.Context, map[string]string) (string, error) { ran = append(ran, "region"); return "x", nil },
		}).
		Add(answer(&ran, "done", "y")).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["region"]; ok {
		t.Errorf("a skipped step recorded an answer: %v", got)
	}
	if want := []string{"deploy", "done"}; !reflect.DeepEqual(ran, want) {
		t.Errorf("ran %v, want the skipped step omitted (%v)", ran, want)
	}
}

// Back returns to the previous step that RAN, and its stale answer is dropped so
// the re-taken value wins.
func TestWizard_backReturnsToPreviousStep(t *testing.T) {
	var firstAsks int
	got, err := NewWizard().
		Step("first", func(context.Context, map[string]string) (string, error) {
			firstAsks++
			if firstAsks == 1 {
				return "original", nil
			}
			return "corrected", nil
		}).
		Step("second", func(_ context.Context, answers map[string]string) (string, error) {
			if answers["first"] == "original" {
				return "", ErrWizardBack // reject and go back
			}
			return "ok", nil
		}).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if firstAsks != 2 {
		t.Errorf("the first step was asked %d times, want 2", firstAsks)
	}
	if got["first"] != "corrected" {
		t.Errorf("answers = %v, want the re-taken value", got)
	}
}

// Going back over a skipped branch skips it again, rather than surfacing a
// question the flow already decided was irrelevant.
func TestWizard_backSkipsOverSkippedSteps(t *testing.T) {
	var branchAsked int
	var thirdAsks int
	_, err := NewWizard().
		Step("first", func(context.Context, map[string]string) (string, error) { return "no", nil }).
		Add(WizardStep{
			Key:  "branch",
			When: func(a map[string]string) bool { return a["first"] == "yes" },
			Ask: func(context.Context, map[string]string) (string, error) {
				branchAsked++
				return "x", nil
			},
		}).
		Step("third", func(context.Context, map[string]string) (string, error) {
			thirdAsks++
			if thirdAsks == 1 {
				return "", ErrWizardBack
			}
			return "done", nil
		}).
		Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if branchAsked != 0 {
		t.Errorf("back surfaced a skipped step %d times", branchAsked)
	}
}

// Back at the very first step re-asks rather than exiting — it is never a way out.
func TestWizard_backAtFirstStepReasks(t *testing.T) {
	var asks int
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewWizard().Step("only", func(context.Context, map[string]string) (string, error) {
			asks++
			if asks == 1 {
				return "", ErrWizardBack
			}
			return "value", nil
		}).Run(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("back at the first step did not re-ask")
	}
	if asks != 2 {
		t.Errorf("asked %d times, want 2", asks)
	}
}

// A canceled flow returns what it collected, so a caller can report partial
// progress instead of losing it.
func TestWizard_cancelReturnsPartialAnswers(t *testing.T) {
	got, err := NewWizard().
		Step("first", func(context.Context, map[string]string) (string, error) { return "kept", nil }).
		Step("second", func(context.Context, map[string]string) (string, error) { return "", ErrWizardCanceled }).
		Run(context.Background())
	if !errors.Is(err, ErrWizardCanceled) {
		t.Fatalf("err = %v, want ErrWizardCanceled", err)
	}
	if got["first"] != "kept" {
		t.Errorf("partial answers lost: %v", got)
	}
}

func TestWizard_contextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewWizard().Step("x", func(context.Context, map[string]string) (string, error) {
		return "", nil
	}).Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Run = %v, want context.Canceled", err)
	}
}

func TestWizard_emptyFlow(t *testing.T) {
	got, err := NewWizard().Run(context.Background())
	if err != nil || len(got) != 0 {
		t.Errorf("empty wizard = (%v, %v), want an empty answer set", got, err)
	}
}
