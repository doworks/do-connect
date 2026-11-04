package callhours

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// SettingsKey is where the rules live in the organization's settings.
const SettingsKey = "call_rules"

// DefaultGapHours is how long a number waits before it gets another notice.
const DefaultGapHours = 6

// stored is what the organization's settings keep. People are keyed by user ID,
// and their name, role and Away state always come from their account.
type stored struct {
	ClosedNow       bool                   `json:"closed_now"`
	Hours           []DayHours             `json:"hours"`
	ClosedDates     []ClosedDate           `json:"closed_dates"`
	OutsideAction   string                 `json:"outside_action"`
	MessageGapHours float64                `json:"message_gap_hours"`
	Messages        Notices                `json:"messages"`
	Templates       Notices                `json:"templates"`
	Staff           map[string]storedStaff `json:"staff"`
}

type storedStaff struct {
	Mode    RingMode       `json:"mode"`
	Shift   []DayHours     `json:"shift"`
	Devices []storedDevice `json:"devices"`
}

type storedDevice struct {
	ID   string     `json:"id"`
	Mode DeviceMode `json:"mode"`
}

// Defaults are the rules shown before anyone saves calling hours.
func Defaults() Rules {
	hours := make([]DayHours, 0, len(Week))
	for _, day := range Week {
		weekday := day != "saturday" && day != "sunday"
		entry := DayHours{Day: day, Enabled: weekday, Ranges: []TimeRange{}}
		if weekday {
			entry.Ranges = []TimeRange{{Start: "09:00", End: "17:00"}}
		}
		hours = append(hours, entry)
	}
	return Rules{
		Hours:           hours,
		ClosedDates:     []ClosedDate{},
		OutsideAction:   OutsideMessage,
		MessageGapHours: DefaultGapHours,
		Messages: Notices{
			Closed: ByLanguage{
				EN: "We're closed right now and open again {next_open}. Send us a message here and we'll reply then.",
				AR: "نحن مغلقون الآن ونفتح {next_open}. اترك لنا رسالة هنا وسنرد عليك حينها.",
			},
			Missed: ByLanguage{
				EN: "Sorry we missed your call. Send us a message here and we'll get back to you.",
				AR: "عذراً، فاتنا اتصالك. اترك لنا رسالة هنا وسنعود إليك.",
			},
		},
		Staff: []Staff{},
	}
}

// appDevice stands for the Whatomate app a person is signed in to. Each phone
// and browser gets its own entry once devices are tracked.
func appDevice(userID string) Device {
	return Device{ID: userID + ":app", Name: "Whatomate", Kind: "browser", Mode: DeviceFollow}
}

func copyHours(hours []DayHours) []DayHours {
	out := make([]DayHours, len(hours))
	for i, entry := range hours {
		out[i] = DayHours{Day: entry.Day, Enabled: entry.Enabled, Ranges: append([]TimeRange{}, entry.Ranges...)}
	}
	return out
}

type member struct {
	ID          uuid.UUID
	FullName    string
	IsAvailable bool
	Role        string
}

// Load returns the organization's rules with everyone who can take calls.
func Load(db *gorm.DB, orgID uuid.UUID) (*Rules, error) {
	var org models.Organization
	if err := db.Select("id", "settings").Where("id = ?", orgID).First(&org).Error; err != nil {
		return nil, err
	}

	rules := Defaults()
	saved := stored{}
	if raw, ok := org.Settings[SettingsKey]; ok && raw != nil {
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &saved); err != nil {
			return nil, fmt.Errorf("reading calling hours: %w", err)
		}
		rules.Configured = true
		rules.ClosedNow = saved.ClosedNow
		if len(saved.Hours) > 0 {
			rules.Hours = saved.Hours
		}
		if saved.ClosedDates != nil {
			rules.ClosedDates = saved.ClosedDates
		}
		if saved.OutsideAction != "" {
			rules.OutsideAction = saved.OutsideAction
		}
		rules.MessageGapHours = saved.MessageGapHours
		rules.Messages = saved.Messages
		rules.Templates = saved.Templates
	}
	// The clinic's time zone is the one set for the organization.
	if zone, _ := org.Settings["timezone"].(string); zone != "" {
		rules.Timezone = zone
	} else {
		rules.Timezone = "UTC"
	}

	var members []member
	err := db.Table("users").
		Select("users.id, users.full_name, users.is_available, custom_roles.name AS role").
		Joins("JOIN user_organizations ON user_organizations.user_id = users.id AND user_organizations.organization_id = ? AND user_organizations.deleted_at IS NULL", orgID).
		Joins("LEFT JOIN custom_roles ON custom_roles.id = user_organizations.role_id").
		Where("users.deleted_at IS NULL AND users.is_active = ?", true).
		Order("users.full_name").
		Scan(&members).Error
	if err != nil {
		return nil, err
	}

	for _, person := range members {
		id := person.ID.String()
		staff := Staff{
			ID:        id,
			Name:      person.FullName,
			Role:      person.Role,
			Available: person.IsAvailable,
			Mode:      ModeClinic,
			Shift:     copyHours(rules.Hours),
			Devices:   []Device{appDevice(id)},
		}
		if own, ok := saved.Staff[id]; ok {
			if own.Mode != "" {
				staff.Mode = own.Mode
			}
			if len(own.Shift) > 0 {
				staff.Shift = own.Shift
			}
			for _, device := range own.Devices {
				for i := range staff.Devices {
					if staff.Devices[i].ID == device.ID && device.Mode != "" {
						staff.Devices[i].Mode = device.Mode
					}
				}
			}
		}
		rules.Staff = append(rules.Staff, staff)
	}
	return &rules, nil
}

var (
	clockPattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	datePattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func validHours(hours []DayHours, what string) ([]DayHours, error) {
	out := make([]DayHours, 0, len(Week))
	for _, day := range Week {
		entry := DayHours{Day: day, Ranges: []TimeRange{}}
		for _, given := range hours {
			if given.Day == day {
				entry.Enabled = given.Enabled
				entry.Ranges = append(entry.Ranges, given.Ranges...)
			}
		}
		if len(entry.Ranges) > 2 {
			return nil, fmt.Errorf("%s: %s can have at most two periods", what, day)
		}
		for _, span := range entry.Ranges {
			if !clockPattern.MatchString(span.Start) || !clockPattern.MatchString(span.End) {
				return nil, fmt.Errorf("%s: %s has a time that isn't HH:MM", what, day)
			}
			if span.End <= span.Start {
				return nil, fmt.Errorf("%s: on %s a period ends before it starts", what, day)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// Save checks the rules from the settings page and stores them. Name, role,
// Away and time zone aren't stored here: they come from elsewhere.
func Save(db *gorm.DB, orgID uuid.UUID, rules Rules) error {
	hours, err := validHours(rules.Hours, "Clinic hours")
	if err != nil {
		return ValidationError{err}
	}
	saved := stored{
		ClosedNow:       rules.ClosedNow,
		Hours:           hours,
		ClosedDates:     []ClosedDate{},
		OutsideAction:   rules.OutsideAction,
		MessageGapHours: rules.MessageGapHours,
		Messages:        rules.Messages,
		Templates:       trimNames(rules.Templates),
		Staff:           map[string]storedStaff{},
	}
	if !slices.Contains([]string{OutsideMessage, OutsideVoice, OutsideRing}, saved.OutsideAction) {
		return ValidationError{fmt.Errorf("unknown choice for outside clinic hours: %q", rules.OutsideAction)}
	}
	if saved.MessageGapHours < 0 || saved.MessageGapHours > 72 {
		return ValidationError{fmt.Errorf("the gap between messages must be 0 to 72 hours")}
	}
	for _, entry := range rules.ClosedDates {
		if entry.Date == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", entry.Date); err != nil || !datePattern.MatchString(entry.Date) {
			return ValidationError{fmt.Errorf("closed date %q isn't a date", entry.Date)}
		}
		saved.ClosedDates = append(saved.ClosedDates, entry)
	}
	for _, staff := range rules.Staff {
		if _, err := uuid.Parse(staff.ID); err != nil {
			continue
		}
		if !slices.Contains([]RingMode{ModeClinic, ModeShift, ModeAlways, ModeNever}, staff.Mode) {
			return ValidationError{fmt.Errorf("%s: unknown ring setting %q", staff.Name, staff.Mode)}
		}
		shift, err := validHours(staff.Shift, staff.Name+"'s shift")
		if err != nil {
			return ValidationError{err}
		}
		own := storedStaff{Mode: staff.Mode, Shift: shift, Devices: []storedDevice{}}
		for _, device := range staff.Devices {
			if device.Mode != DeviceFollow && device.Mode != DeviceNever {
				return ValidationError{fmt.Errorf("%s: unknown device setting %q", staff.Name, device.Mode)}
			}
			own.Devices = append(own.Devices, storedDevice{ID: device.ID, Mode: device.Mode})
		}
		saved.Staff[staff.ID] = own
	}

	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	// Only this key changes, so a save on another settings tab can't undo it.
	return db.Model(&models.Organization{}).Where("id = ?", orgID).
		Update("settings", gorm.Expr("jsonb_set(COALESCE(settings, '{}'::jsonb), ?, ?::jsonb)", "{"+SettingsKey+"}", string(data))).Error
}

func trimNames(names Notices) Notices {
	trim := strings.TrimSpace
	names.Closed.EN, names.Closed.AR = trim(names.Closed.EN), trim(names.Closed.AR)
	names.Missed.EN, names.Missed.AR = trim(names.Missed.EN), trim(names.Missed.AR)
	return names
}

// ValidationError is a problem with what was sent, not with the server.
type ValidationError struct{ error }
