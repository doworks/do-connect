// Package callhours decides when the clinic takes calls and which staff ring.
// It mirrors frontend/src/lib/callHours.ts, which shows the same answers on the
// settings page while someone is still editing: change both together.
package callhours

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Time zones work on servers without a zoneinfo database.
)

type TimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type DayHours struct {
	Day     string      `json:"day"`
	Enabled bool        `json:"enabled"`
	Ranges  []TimeRange `json:"ranges"`
}

type ClosedDate struct {
	Date  string `json:"date"`
	Label string `json:"label"`
}

type RingMode string

const (
	ModeClinic RingMode = "clinic"
	ModeShift  RingMode = "shift"
	ModeAlways RingMode = "always"
	ModeNever  RingMode = "never"
)

type DeviceMode string

const (
	DeviceFollow DeviceMode = "follow"
	DeviceNever  DeviceMode = "never"
)

type Device struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Kind     string     `json:"kind"`
	Mode     DeviceMode `json:"mode"`
	LastSeen string     `json:"last_seen,omitempty"`
}

type Staff struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	Available bool       `json:"available"`
	Mode      RingMode   `json:"mode"`
	Shift     []DayHours `json:"shift"`
	Devices   []Device   `json:"devices"`
}

// Outside clinic hours the call is declined with a message, the same after a
// recording, or rings as usual.
const (
	OutsideMessage = "message"
	OutsideVoice   = "voice"
	OutsideRing    = "ring"
)

// ByLanguage holds one text per patient language.
type ByLanguage struct {
	EN string `json:"en"`
	AR string `json:"ar"`
}

// Notices are what the patient gets when a call isn't answered.
type Notices struct {
	Closed ByLanguage `json:"closed"`
	Missed ByLanguage `json:"missed"`
}

type Rules struct {
	Timezone        string       `json:"timezone"`
	ClosedNow       bool         `json:"closed_now"`
	Hours           []DayHours   `json:"hours"`
	ClosedDates     []ClosedDate `json:"closed_dates"`
	OutsideAction   string       `json:"outside_action"`
	MessageGapHours float64      `json:"message_gap_hours"`
	Messages        Notices      `json:"messages"`
	// Templates name an approved template per language for when the patient is
	// outside WhatsApp's 24-hour window and a typed message can't go out.
	Templates Notices `json:"templates"`
	Staff     []Staff `json:"staff"`

	// Configured is false until someone saves calling hours. Until then nothing
	// is enforced and every call is handled as before.
	Configured bool `json:"-"`
}

var Week = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

type clinicClock struct {
	day  string
	date string
	time string
}

func (r *Rules) location() *time.Location {
	if loc, err := time.LoadLocation(r.Timezone); err == nil && r.Timezone != "" {
		return loc
	}
	return time.UTC
}

func (r *Rules) clock(at time.Time) clinicClock {
	local := at.In(r.location())
	return clinicClock{
		day:  strings.ToLower(local.Weekday().String()),
		date: local.Format("2006-01-02"),
		time: local.Format("15:04"),
	}
}

func inHours(hours []DayHours, clock clinicClock) bool {
	for _, entry := range hours {
		if entry.Day != clock.day {
			continue
		}
		if !entry.Enabled {
			return false
		}
		// The end time is exclusive: a range to 13:00 has stopped at 13:00.
		for _, span := range entry.Ranges {
			if clock.time >= span.Start && clock.time < span.End {
				return true
			}
		}
		return false
	}
	return false
}

func (r *Rules) closedOn(date string) bool {
	for _, entry := range r.ClosedDates {
		if entry.Date == date {
			return true
		}
	}
	return false
}

// IsOpen reports whether the clinic takes calls at this moment.
func (r *Rules) IsOpen(at time.Time) bool {
	if r.ClosedNow {
		return false
	}
	clock := r.clock(at)
	if r.closedOn(clock.date) {
		return false
	}
	return inHours(r.Hours, clock)
}

func (r *Rules) staffOnDuty(staff *Staff, at time.Time) bool {
	switch staff.Mode {
	case ModeAlways:
		return true
	case ModeNever:
		return false
	case ModeShift:
		return inHours(staff.Shift, r.clock(at))
	default:
		return r.IsOpen(at) || r.OutsideAction == OutsideRing
	}
}

// DeviceRings reports whether one of a person's devices rings now. Away always
// wins.
func (r *Rules) DeviceRings(staff *Staff, device *Device, at time.Time) bool {
	if r.ClosedNow || !staff.Available || device.Mode == DeviceNever {
		return false
	}
	return r.staffOnDuty(staff, at)
}

// Ring is one device that rings for a call arriving now.
type Ring struct {
	Staff  *Staff
	Device *Device
}

// WhoRings lists the devices that ring for a call arriving now.
func (r *Rules) WhoRings(at time.Time) []Ring {
	// Outside clinic hours only on-call staff ring, unless the clinic chose to ring as usual.
	open := r.IsOpen(at) || r.OutsideAction == OutsideRing
	result := []Ring{}
	for i := range r.Staff {
		staff := &r.Staff[i]
		if !open && staff.Mode != ModeAlways {
			continue
		}
		for j := range staff.Devices {
			if r.DeviceRings(staff, &staff.Devices[j], at) {
				result = append(result, Ring{Staff: staff, Device: &staff.Devices[j]})
			}
		}
	}
	return result
}

// RingingStaff is the IDs of people with at least one device that rings now.
func (r *Rules) RingingStaff(at time.Time) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, ring := range r.WhoRings(at) {
		if !seen[ring.Staff.ID] {
			seen[ring.Staff.ID] = true
			ids = append(ids, ring.Staff.ID)
		}
	}
	return ids
}

func clockLabel(value string, arabic bool) string {
	parts := strings.SplitN(value, ":", 2)
	hours, _ := strconv.Atoi(parts[0])
	minutes := 0
	if len(parts) > 1 {
		minutes, _ = strconv.Atoi(parts[1])
	}
	twelve := hours % 12
	if twelve == 0 {
		twelve = 12
	}
	label := fmt.Sprintf("%d:%02d", twelve, minutes)
	if arabic {
		if hours >= 12 {
			return label + " مساءً"
		}
		return label + " صباحاً"
	}
	if hours >= 12 {
		return label + " PM"
	}
	return label + " AM"
}

var arabicDays = map[string]string{
	"sunday": "الأحد", "monday": "الاثنين", "tuesday": "الثلاثاء", "wednesday": "الأربعاء",
	"thursday": "الخميس", "friday": "الجمعة", "saturday": "السبت",
}

// NextOpenLabel is when the clinic next opens, in the patient's language:
// "today at 4:00 PM", "tomorrow at 9:00 AM", "on Saturday at 9:00 AM".
func (r *Rules) NextOpenLabel(at time.Time, language string) string {
	arabic := language == "ar"
	for offset := 0; offset < 14; offset++ {
		clock := r.clock(at.Add(time.Duration(offset) * 24 * time.Hour))
		if r.closedOn(clock.date) {
			continue
		}
		var today *DayHours
		for i := range r.Hours {
			if r.Hours[i].Day == clock.day {
				today = &r.Hours[i]
				break
			}
		}
		if today == nil || !today.Enabled {
			continue
		}
		var span *TimeRange
		for i := range today.Ranges {
			if offset > 0 || today.Ranges[i].Start > clock.time {
				span = &today.Ranges[i]
				break
			}
		}
		if span == nil {
			continue
		}
		at := clockLabel(span.Start, arabic)
		switch {
		case offset == 0 && arabic:
			return "اليوم الساعة " + at
		case offset == 0:
			return "today at " + at
		case offset == 1 && arabic:
			return "غداً الساعة " + at
		case offset == 1:
			return "tomorrow at " + at
		case arabic:
			return "يوم " + arabicDays[clock.day] + " الساعة " + at
		default:
			return "on " + strings.ToUpper(clock.day[:1]) + clock.day[1:] + " at " + at
		}
	}
	if arabic {
		return "قريباً"
	}
	return "soon"
}
