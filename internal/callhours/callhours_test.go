package callhours

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// These mirror frontend/src/lib/callHours.spec.ts: the same rules must give the
// same answers on the settings page and on the server.

func week(days []string, ranges ...[2]string) []DayHours {
	out := []DayHours{}
	for _, day := range Week {
		entry := DayHours{Day: day, Enabled: slices.Contains(days, day), Ranges: []TimeRange{}}
		if entry.Enabled {
			for _, span := range ranges {
				entry.Ranges = append(entry.Ranges, TimeRange{Start: span[0], End: span[1]})
			}
		}
		out = append(out, entry)
	}
	return out
}

func person(id string, mode RingMode, change ...func(*Staff)) Staff {
	staff := Staff{
		ID: id, Name: id, Available: true, Mode: mode, Shift: []DayHours{},
		Devices: []Device{{ID: id + "-phone", Name: "phone", Kind: "phone", Mode: DeviceFollow}},
	}
	for _, fn := range change {
		fn(&staff)
	}
	return staff
}

func rules(change ...func(*Rules)) *Rules {
	r := &Rules{
		Timezone:        "Asia/Bahrain",
		Hours:           week([]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "saturday"}, [2]string{"09:00", "13:00"}, [2]string{"16:00", "20:00"}),
		ClosedDates:     []ClosedDate{},
		OutsideAction:   OutsideMessage,
		MessageGapHours: 6,
		Staff:           []Staff{},
	}
	for _, fn := range change {
		fn(r)
	}
	return r
}

func when(value string) time.Time {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return at
}

// at is a time on Tuesday 3 Nov 2026 in the clinic's zone.
func at(clock string) time.Time { return when("2026-11-03T" + clock + ":00+03:00") }

func ringIDs(r *Rules, moment time.Time) []string {
	ids := []string{}
	for _, ring := range r.WhoRings(moment) {
		ids = append(ids, ring.Staff.ID)
	}
	return ids
}

func TestClinicHours(t *testing.T) {
	holiday := func(r *Rules) { r.ClosedDates = []ClosedDate{{Date: "2026-11-03", Label: "Holiday"}} }
	cases := []struct {
		name  string
		rules *Rules
		at    time.Time
		open  bool
	}{
		{"morning period", rules(), at("10:00"), true},
		{"between the periods", rules(), at("14:30"), false},
		{"evening period starts", rules(), at("16:00"), true},
		{"just before the end", rules(), at("12:59"), true},
		{"the end time is exclusive", rules(), at("13:00"), false},
		{"closed day", rules(), when("2026-11-06T10:00:00+03:00"), false},
		{"closed date", rules(holiday), at("10:00"), false},
		{"in the clinic's zone, not UTC", rules(), when("2026-11-03T07:00:00Z"), true},
		{"close for everyone", rules(func(r *Rules) { r.ClosedNow = true }), at("10:00"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.open, tc.rules.IsOpen(tc.at))
		})
	}
}

func TestWhoRings(t *testing.T) {
	staff := func() []Staff {
		return []Staff{
			person("clinic", ModeClinic),
			person("evening", ModeShift, func(s *Staff) { s.Shift = week([]string{"tuesday"}, [2]string{"16:00", "20:00"}) }),
			person("oncall", ModeAlways),
			person("never", ModeNever),
		}
	}
	withStaff := func(r *Rules) { r.Staff = staff() }
	cases := []struct {
		name  string
		rules *Rules
		at    time.Time
		want  []string
	}{
		{"clinic hours ring clinic staff", rules(withStaff), at("10:00"), []string{"clinic", "oncall"}},
		{"and whoever is on shift", rules(withStaff), at("17:00"), []string{"clinic", "evening", "oncall"}},
		{"outside clinic hours only on-call staff", rules(withStaff), at("22:00"), []string{"oncall"}},
		{"a shift outside clinic hours stays quiet", rules(func(r *Rules) {
			r.Staff = []Staff{person("late", ModeShift, func(s *Staff) { s.Shift = week([]string{"tuesday"}, [2]string{"21:00", "23:00"}) })}
		}), at("22:00"), []string{}},
		{"close for everyone silences on-call staff too", rules(withStaff, func(r *Rules) { r.ClosedNow = true }), at("10:00"), []string{}},
		{"ring as usual still respects each shift", rules(withStaff, func(r *Rules) { r.OutsideAction = OutsideRing }), at("22:00"), []string{"clinic", "oncall"}},
		{"away people and silenced phones never ring", rules(func(r *Rules) {
			r.Staff = []Staff{
				person("away", ModeAlways, func(s *Staff) { s.Available = false }),
				person("muted", ModeAlways, func(s *Staff) { s.Devices[0].Mode = DeviceNever }),
			}
		}), at("10:00"), []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ringIDs(tc.rules, tc.at))
			assert.Equal(t, tc.want, tc.rules.RingingStaff(tc.at))
		})
	}
}

func TestNextOpenLabel(t *testing.T) {
	thursdayNight := when("2026-11-05T21:00:00+03:00")
	cases := []struct {
		name     string
		rules    *Rules
		at       time.Time
		language string
		want     string
	}{
		{"the second period the same day", rules(), at("14:00"), "en", "today at 4:00 PM"},
		{"tomorrow after closing", rules(), at("21:00"), "en", "tomorrow at 9:00 AM"},
		{"skips the closed Friday", rules(), thursdayNight, "en", "on Saturday at 9:00 AM"},
		{"skips the closed Friday, in Arabic", rules(), thursdayNight, "ar", "يوم السبت الساعة 9:00 صباحاً"},
		{"skips closed dates", rules(func(r *Rules) { r.ClosedDates = []ClosedDate{{Date: "2026-11-04", Label: "Holiday"}} }), at("21:00"), "en", "on Thursday at 9:00 AM"},
		{"never open", rules(func(r *Rules) { r.Hours = week(nil) }), at("21:00"), "en", "soon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.rules.NextOpenLabel(tc.at, tc.language))
		})
	}
}

func TestValidHours(t *testing.T) {
	_, err := validHours(week([]string{"monday"}, [2]string{"09:00", "13:00"}, [2]string{"14:00", "15:00"}, [2]string{"16:00", "17:00"}), "Clinic hours")
	assert.ErrorContains(t, err, "at most two periods")

	_, err = validHours(week([]string{"monday"}, [2]string{"13:00", "09:00"}), "Clinic hours")
	assert.ErrorContains(t, err, "ends before it starts")

	_, err = validHours(week([]string{"monday"}, [2]string{"9am", "13:00"}), "Clinic hours")
	assert.ErrorContains(t, err, "HH:MM")

	hours, err := validHours([]DayHours{{Day: "monday", Enabled: true, Ranges: []TimeRange{{Start: "09:00", End: "13:00"}}}}, "Clinic hours")
	assert.NoError(t, err)
	assert.Len(t, hours, 7, "every day of the week is kept")
}
