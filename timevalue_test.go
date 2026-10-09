package rotini

import (
	"strings"
	"testing"
	"time"
)

// fixedClock is a runClock reading at, in its location.
func fixedClock(at time.Time) *runClock {
	return &runClock{fn: func() time.Time { return at }}
}

// Relative values: words, offsets and their sign rules per direction, absolute values first,
// and dates landing on a calendar date.
func TestParseTimeValue_relative(t *testing.T) {
	loc := time.FixedZone("X", 2*3600)
	now := time.Date(2026, 10, 8, 14, 30, 0, 0, loc)
	midnight := time.Date(2026, 10, 8, 0, 0, 0, 0, loc)
	day := func(d int) time.Time { return time.Date(2026, 10, 8+d, 0, 0, 0, 0, time.UTC) }
	spec := func(rel string, layouts ...string) timeSpec {
		ts := timeSpec{relative: rel, clock: fixedClock(now)}
		if len(layouts) > 0 {
			ts.layout, ts.layouts = layouts[0], layouts
		}
		return ts
	}
	for _, tt := range []struct {
		in   string
		ts   timeSpec
		want time.Time
		err  string
	}{
		{"now", spec("past"), now, ""},
		{"NOW", spec("past"), now, ""},
		{"today", spec("past"), midnight, ""},
		{"yesterday", spec("past"), midnight.AddDate(0, 0, -1), ""},
		{"tomorrow", spec("past"), time.Time{}, "tomorrow is in the future; this input takes past times"},
		{"tomorrow", spec("future"), midnight.AddDate(0, 0, 1), ""},
		{"yesterday", spec("future"), time.Time{}, "yesterday is in the past; this input takes future times"},
		{"yesterday", spec("both"), midnight.AddDate(0, 0, -1), ""},
		{"2h", spec("past"), now.Add(-2 * time.Hour), ""},
		{"-2h", spec("past"), now.Add(-2 * time.Hour), ""},
		{"+2h", spec("past"), time.Time{}, "+2h is a time from now; this input takes past times, such as 2h"},
		{"3d", spec("future"), now.Add(72 * time.Hour), ""},
		{"+1w", spec("future"), now.Add(168 * time.Hour), ""},
		{"-2h", spec("future"), time.Time{}, "-2h is a time ago"},
		{"-90m", spec("both"), now.Add(-90 * time.Minute), ""},
		{"2h", spec("both"), time.Time{}, "write -2h for 2h ago or +2h for 2h from now"},
		{"--2h", spec("past"), time.Time{}, "write it as RFC 3339 (2026-09-29T14:00:00Z), a time ago such as 2h or 3d, or now, today or yesterday"},
		{"2x", spec("future"), time.Time{}, "write it as RFC 3339 (2026-09-29T14:00:00Z), a time from now such as 2h or 3d, or now, today or tomorrow"},
		{"2026-09-29T14:00:00Z", spec("past"), time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC), ""},
		// Dates land on the calendar date the result falls on, in the clock's zone.
		{"today", spec("past", "2006-01-02"), day(0), ""},
		{"now", spec("past", "2006-01-02"), day(0), ""},
		{"yesterday", spec("past", "2006-01-02"), day(-1), ""},
		{"2d", spec("past", "2006-01-02"), day(-2), ""},
		{"2h", spec("past", "2006-01-02"), time.Time{}, "a date moves in whole days, such as 1d or 2w"},
		{"2026-01-02", spec("past", "2006-01-02"), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), ""},
		{"x", spec("past", "2006-01-02"), time.Time{}, `"x" is not a valid date (write it as 2006-01-02, a time ago such as 3d or 1w, or now, today or yesterday)`},
	} {
		got, err := parseTimeValue(tt.in, tt.ts)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%q (%s): err = %v, want %q", tt.in, tt.ts.relative, err, tt.err)
			}
			continue
		}
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("%q (%s): got %v, %v; want %v", tt.in, tt.ts.relative, got, err, tt.want)
		}
	}
}

// Several layouts are tried in order, the first that parses winning, and an error names them
// all; one layout keeps its own message.
func TestParseTimeValue_layouts(t *testing.T) {
	ts := timeSpec{layout: "2006-01-02 15:04", layouts: []string{"2006-01-02 15:04", "2006-01-02", "unix"}}
	for in, want := range map[string]time.Time{
		"2026-09-29 14:00": time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC),
		"2026-09-29":       time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
		"1759104000":       time.Unix(1759104000, 0).UTC(),
	} {
		if got, err := parseTimeValue(in, ts); err != nil || !got.Equal(want) {
			t.Errorf("%q: got %v, %v; want %v", in, got, err, want)
		}
	}
	_, err := parseTimeValue("soon", ts)
	if err == nil || !strings.Contains(err.Error(), `"soon" is not a valid time (write it as 2006-01-02 15:04, 2006-01-02 or a Unix timestamp in seconds)`) {
		t.Errorf("err = %v", err)
	}
	_, err = parseTimeValue("soon", timeSpec{layout: "unix"})
	if err == nil || !strings.Contains(err.Error(), "want a Unix timestamp in seconds") {
		t.Errorf("single layout err = %v", err)
	}
}

// A map of times reads each value under the input's layouts, as a list does.
func TestParse_mapOfDates(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "days", Identifiers: []string{"--days"}, Type: "map[string]time.Time", Layout: "2006-01-02"},
	}}
	var in struct {
		App struct {
			Flags struct {
				Days map[string]time.Time `rotini:"days"`
			}
		}
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"--days", "start=2026-01-02", "--days", "end=2026-01-09"}), &in); err != nil {
		t.Fatal(err)
	}
	want := map[string]time.Time{"start": time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "end": time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)}
	for k, w := range want {
		if got := in.App.Flags.Days[k]; !got.Equal(w) {
			t.Errorf("days[%s] = %v, want %v", k, got, w)
		}
	}
	err := NewParser().Parse(NewContextFor(def, []string{"--days", "start=2026-01-02T00:00:00Z"}), &in)
	if err == nil || !strings.Contains(err.Error(), `value for key "start"`) {
		t.Errorf("an RFC 3339 value under a date layout: err = %v", err)
	}
}

// A run reads its clock once: every relative value and rtx.Now in it agree, and WithClock(nil)
// restores time.Now.
func TestClock_oneReadingPerRun(t *testing.T) {
	calls := 0
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	rtx := NewContextFor(Definition{Name: "app"}, nil).WithClock(func() time.Time {
		calls++
		return at.Add(time.Duration(calls) * time.Minute)
	})
	first := rtx.Now()
	if second := rtx.Now(); !second.Equal(first) || calls != 1 {
		t.Fatalf("Now = %v then %v after %d reads", first, second, calls)
	}
	view := rtx.osView().clockRef()
	if got := view.now(); !got.Equal(first) {
		t.Errorf("the view's clock reads %v, the run's %v", got, first)
	}
	rtx.WithClock(nil)
	if got := rtx.Now(); time.Until(got).Abs() > time.Minute {
		t.Errorf("WithClock(nil) reads %v", got)
	}
}

// A program's clock reaches every run, and each run takes its own reading.
func TestProgram_withClock(t *testing.T) {
	reads := 0
	p := &Program{}
	p.WithClock(func() time.Time { reads++; return time.Unix(int64(reads), 0) })
	a, b := p.newRunContext(), p.newRunContext()
	if a.Now().Unix() == b.Now().Unix() || reads != 2 {
		t.Errorf("runs read %v and %v after %d reads", a.Now(), b.Now(), reads)
	}
}
