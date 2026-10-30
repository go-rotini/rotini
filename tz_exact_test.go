package rotini

import (
	"strings"
	"testing"
	"time"
)

// TestTimeZone_exactNames pins that a timezone input matches IANA names exactly on every
// operating system, refuses an empty value, and keeps UTC and Local.
func TestTimeZone_exactNames(t *testing.T) {
	for _, ok := range []string{"UTC", "Local", "Europe/Berlin", "America/New_York"} {
		if _, err := parseTimeZoneValue(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "utc", "local", "europe/berlin", "Europe/berlin", "EUROPE/BERLIN"} {
		_, err := parseTimeZoneValue(bad)
		if err == nil || !strings.HasPrefix(err.Error(), "want an IANA name such as Europe/Berlin") {
			t.Errorf("%q: err = %v, want refused", bad, err)
		}
	}
}

// TestTimeZone_emptyFlag pins that --tz= is a usage error, not UTC.
func TestTimeZone_emptyFlag(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "tz", Identifiers: []string{"--tz"}, Type: "timezone"}}}
	var in struct {
		App struct {
			Flags struct {
				TZ *time.Location `rotini:"tz"`
			}
			Arguments struct{}
		}
	}
	err := NewParser().Parse(NewContextFor(def, []string{"--tz="}), &in)
	if err == nil || !strings.Contains(err.Error(), "--tz") {
		t.Errorf("err = %v, want --tz refused", err)
	}
}
