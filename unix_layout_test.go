package rotini

import (
	"testing"
	"time"
)

// TestUnixLayouts_refuseLeadingPlus pins that every unix layout refuses a leading "+" and
// keeps a leading "-".
func TestUnixLayouts_refuseLeadingPlus(t *testing.T) {
	for _, layout := range []string{layoutUnix, layoutUnixMilli} {
		if _, err := parseTimeLayout("+1759104000", layout); err == nil {
			t.Errorf("%s: +1759104000 accepted", layout)
		}
		if _, err := parseTimeLayout("-1000", layout); err != nil {
			t.Errorf("%s: -1000: %v", layout, err)
		}
	}
}

// TestDateUnix_landsOnMidnight pins that a date input read under a unix layout is that day's UTC
// midnight, on the command line and in the environment.
func TestDateUnix_landsOnMidnight(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "day", Identifiers: []string{"--day"}, Type: "time.Time", Layout: layoutUnix, Date: true},
			{Name: "at", Identifiers: []string{"--at"}, Type: "time.Time", Layout: layoutUnixMilli},
		},
	}
	var in struct {
		App struct {
			Flags struct {
				Day time.Time `rotini:"day"`
				At  time.Time `rotini:"at"`
			}
			Arguments struct{}
			Env       struct {
				Due time.Time `rotini:"due" recon:"due" env:"APP_DUE" layout:"unixmilli" date:"true"`
			}
		}
	}
	rtx := NewContextFor(def, []string{"--day", "1759150800.5", "--at", "1759150800000"}).WithEnviron([]string{"APP_DUE=1759150800000"})
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	midnight := time.Date(2025, 9, 29, 0, 0, 0, 0, time.UTC)
	if !in.App.Flags.Day.Equal(midnight) || !in.App.Env.Due.Equal(midnight) {
		t.Errorf("day %v, due %v; want %v", in.App.Flags.Day, in.App.Env.Due, midnight)
	}
	if want := time.UnixMilli(1759150800000).UTC(); !in.App.Flags.At.Equal(want) {
		t.Errorf("at = %v, want the instant %v", in.App.Flags.At, want)
	}
}
