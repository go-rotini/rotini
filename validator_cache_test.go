package rotini

import "testing"

func TestValidatorCache_compilesOnce(t *testing.T) {
	const schema = `{"type":"object","required":["cache-probe"]}`
	a, err := schemaValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := schemaValidator(schema); a != b {
		t.Error("schemaValidator compiled the same schema twice")
	}
	if _, err := schemaValidator(`{"type":`); err == nil {
		t.Error("schemaValidator accepted an invalid schema")
	}

	re, err := compiledPattern(`^cache-probe-[0-9]+$`)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := compiledPattern(`^cache-probe-[0-9]+$`); re != again {
		t.Error("compiledPattern compiled the same pattern twice")
	}
	if _, err := compiledPattern(`(`); err == nil {
		t.Error("compiledPattern accepted an invalid pattern")
	}
}
