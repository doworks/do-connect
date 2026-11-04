package handlers

import (
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// First replies within this are on target; past the slow mark they need a look.
const (
	staffReportTargetSecs = 15 * 60
	staffReportSlowSecs   = 60 * 60
	staffReportMaxDays    = 366
	fallbackTimezone      = "Asia/Bahrain"
)

// What sent an outgoing message that no person did.
const (
	AutomaticKindLabelled = "labelled" // API key send, named by its sent_by_label
	AutomaticKindCampaign = "campaign"
	AutomaticKindOther    = "other" // chatbot replies and messages sent from the phone app
)

// StaffDayStats is one person's (or the team's) work on one day.
type StaffDayStats struct {
	Date          string   `json:"date"`
	ChatsAnswered int64    `json:"chats_answered"`
	MessagesSent  int64    `json:"messages_sent"`
	FirstReplies  int64    `json:"first_replies"`
	OnTarget      int64    `json:"on_target"`
	MedianSecs    *float64 `json:"median_first_reply_secs"`
	P90Secs       *float64 `json:"p90_first_reply_secs"`
}

// StaffReportRow is a person, or the whole team, over the selected days.
type StaffReportRow struct {
	UserID        string          `json:"user_id,omitempty"`
	Name          string          `json:"name"`
	IsActive      bool            `json:"is_active"`
	IsAvailable   bool            `json:"is_available"`
	ChatsAnswered int64           `json:"chats_answered"`
	MessagesSent  int64           `json:"messages_sent"`
	FirstReplies  int64           `json:"first_replies"`
	OnTarget      int64           `json:"on_target"`
	MedianSecs    *float64        `json:"median_first_reply_secs"`
	P90Secs       *float64        `json:"p90_first_reply_secs"`
	WaitingNow    int64           `json:"waiting_now"`
	OldestWaiting *time.Time      `json:"oldest_waiting_since,omitempty"`
	OwnedChats    int64           `json:"owned_chats"`
	Daily         []StaffDayStats `json:"daily"`
}

// AutomaticSource is one kind of automated sender, such as a reminder label.
type AutomaticSource struct {
	Kind         string `json:"kind"`
	Label        string `json:"label,omitempty"`
	MessagesSent int64  `json:"messages_sent"`
	ChatsReached int64  `json:"chats_reached"`
}

// AutomaticReportRow is everything sent without a person behind it.
type AutomaticReportRow struct {
	MessagesSent int64             `json:"messages_sent"`
	ChatsReached int64             `json:"chats_reached"`
	Sources      []AutomaticSource `json:"sources"`
	Daily        []StaffDayStats   `json:"daily"`
}

// StaffReportResponse is the staff report for a date range.
type StaffReportResponse struct {
	From       string             `json:"from"`
	To         string             `json:"to"`
	Timezone   string             `json:"timezone"`
	Days       []string           `json:"days"`
	TargetSecs int                `json:"target_secs"`
	SlowSecs   int                `json:"slow_secs"`
	Team       StaffReportRow     `json:"team"`
	People     []StaffReportRow   `json:"people"`
	Unassigned StaffReportRow     `json:"unassigned"`
	Automatic  AutomaticReportRow `json:"automatic"`
}

// GetStaffReport returns per-person and per-day work for managers: chats answered,
// messages sent, time to first reply, and the chats each person owns and leaves
// waiting. Messages sent by API keys, the chatbot or campaigns count as
// automatic, never as a person's work.
func (a *App) GetStaffReport(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceAnalytics, models.ActionRead)
	if err != nil {
		return nil
	}

	loc := a.orgLocation(orgID)
	fromStr := string(r.RequestCtx.QueryArgs().Peek("from"))
	toStr := string(r.RequestCtx.QueryArgs().Peek("to"))
	fromDay, toDay, errMsg := parseReportDays(fromStr, toStr, loc, time.Now())
	if errMsg != "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, errMsg, nil, "")
	}
	start := fromDay
	end := toDay.AddDate(0, 0, 1)

	resp, err := a.buildStaffReport(orgID, loc, start, end)
	if err != nil {
		a.Log.Error("Failed to build staff report", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to build staff report", nil, "")
	}
	resp.From = fromDay.Format("2006-01-02")
	resp.To = toDay.Format("2006-01-02")
	return r.SendEnvelope(resp)
}

// orgLocation is the organization's own timezone, else the server default.
func (a *App) orgLocation(orgID uuid.UUID) *time.Location {
	var org models.Organization
	if a.DB.Select("id", "settings").Where("id = ?", orgID).First(&org).Error == nil && org.Settings != nil {
		if name, ok := org.Settings["timezone"].(string); ok && name != "" {
			if loc, err := time.LoadLocation(name); err == nil {
				return loc
			}
		}
	}
	return a.defaultLocation()
}

func (a *App) defaultTimezoneName() string {
	if a.Config != nil && a.Config.App.DefaultTimezone != "" {
		return a.Config.App.DefaultTimezone
	}
	return fallbackTimezone
}

func (a *App) defaultLocation() *time.Location {
	if loc, err := time.LoadLocation(a.defaultTimezoneName()); err == nil {
		return loc
	}
	return time.UTC
}

// parseReportDays reads from/to as calendar days in loc. Without them it's the
// month so far.
func parseReportDays(fromStr, toStr string, loc *time.Location, now time.Time) (from, to time.Time, errMsg string) {
	today := now.In(loc)
	if fromStr == "" && toStr == "" {
		from = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc)
		to = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
		return from, to, ""
	}
	var err error
	if from, err = time.ParseInLocation("2006-01-02", fromStr, loc); err != nil {
		return from, to, "Invalid start date format. Use YYYY-MM-DD"
	}
	if to, err = time.ParseInLocation("2006-01-02", toStr, loc); err != nil {
		return from, to, "Invalid end date format. Use YYYY-MM-DD"
	}
	if to.Before(from) {
		return from, to, "End date is before start date"
	}
	if to.Sub(from) > staffReportMaxDays*24*time.Hour {
		return from, to, "Date range is too long (at most a year)"
	}
	return from, to, ""
}

// staffVolumeSQL counts outgoing messages and the chats they went to, for each
// person and day, each person, each day and overall, and for automated senders
// (person_id NULL) by source. GROUPING() tells a rolled-up row from a NULL key.
const staffVolumeSQL = `
SELECT is_person, person_id, day, source, label,
       COUNT(*) AS messages, COUNT(DISTINCT contact_id) AS chats,
       GROUPING(person_id) AS all_people, GROUPING(day) AS all_days, GROUPING(source, label) AS all_sources
FROM (
  SELECT is_person,
         CASE WHEN is_person THEN sent_by_user_id END AS person_id,
         CASE WHEN is_person THEN '' WHEN COALESCE(sent_by_label, '') <> '' THEN 'labelled'
              WHEN metadata->>'campaign_id' IS NOT NULL THEN 'campaign' ELSE 'other' END AS source,
         CASE WHEN is_person THEN '' ELSE COALESCE(sent_by_label, '') END AS label,
         (created_at AT TIME ZONE ?)::date AS day,
         contact_id
  FROM (
    -- A person's work is sent from their login, not through an API key.
    -- Messages from before labels existed have none (NULL).
    SELECT *, (sent_by_user_id IS NOT NULL AND COALESCE(sent_by_label, '') = '') AS is_person
    FROM messages
    WHERE organization_id = ? AND direction = 'outgoing' AND deleted_at IS NULL
      AND created_at >= ? AND created_at < ?
  ) o
) m
GROUP BY GROUPING SETS (
  (is_person, person_id, day), (is_person, person_id), (is_person, day), (is_person),
  (is_person, source, label)
)
HAVING (is_person AND GROUPING(source, label) <> 0)
    OR (NOT is_person AND (GROUPING(person_id) = 0 OR GROUPING(source, label) = 0))`

// answerSQL is an outgoing message that answers the patient: anything except an
// API-key send or a campaign. A reminder going out doesn't answer the patient,
// while a chatbot or phone-app reply does, so a person replying after that wasn't
// the first to answer. idx_messages_contact_answers is built on this predicate.
const answerSQL = `direction = 'outgoing' AND deleted_at IS NULL AND COALESCE(sent_by_label, '') = '' AND metadata->>'campaign_id' IS NULL`

// staffFirstReplySQL measures each wait that a person's reply in the range ended.
// A wait starts with the patient's first message after the last answer. Each
// chat is read once, from its last answer before the range, so a wait that began
// before the range still counts in full; window functions then number the
// answers and find where each wait began.
const staffFirstReplySQL = `
WITH replied AS (
  SELECT contact_id, MIN(created_at) AS first_at
  FROM messages
  WHERE organization_id = ? AND deleted_at IS NULL AND direction = 'outgoing'
    AND sent_by_user_id IS NOT NULL AND COALESCE(sent_by_label, '') = ''
    AND created_at >= ? AND created_at < ?
  GROUP BY contact_id
),
anchored AS (
  SELECT rp.contact_id, COALESCE(a.created_at, '-infinity'::timestamptz) AS from_at
  FROM replied rp
  LEFT JOIN LATERAL (
    SELECT created_at FROM messages
    WHERE contact_id = rp.contact_id AND created_at < rp.first_at AND ` + answerSQL + `
    ORDER BY created_at DESC LIMIT 1
  ) a ON true
),
numbered AS (
  SELECT m.contact_id, m.created_at, m.direction,
         m.sent_by_user_id IS NOT NULL AND COALESCE(m.sent_by_label, '') = '' AS is_person,
         COUNT(*) FILTER (WHERE m.direction = 'outgoing' AND COALESCE(m.sent_by_label, '') = '' AND m.metadata->>'campaign_id' IS NULL)
           OVER (PARTITION BY m.contact_id ORDER BY m.created_at, m.id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS answers_before,
         m.sent_by_user_id
  FROM anchored an
  JOIN messages m ON m.contact_id = an.contact_id AND m.created_at >= an.from_at AND m.created_at < ?
  WHERE m.deleted_at IS NULL
),
waits AS (
  SELECT *, MIN(created_at) FILTER (WHERE direction = 'incoming')
              OVER (PARTITION BY contact_id, answers_before) AS wait_start
  FROM numbered
),
firsts AS (
  SELECT sent_by_user_id AS person_id,
         (created_at AT TIME ZONE ?)::date AS day,
         EXTRACT(EPOCH FROM (created_at - wait_start))::float8 AS secs
  FROM waits
  WHERE is_person AND direction = 'outgoing' AND wait_start IS NOT NULL AND wait_start < created_at
    AND created_at >= ?
)
SELECT person_id, day,
       COUNT(*) AS replies,
       COUNT(*) FILTER (WHERE secs <= ?) AS on_target,
       percentile_cont(0.5) WITHIN GROUP (ORDER BY secs) AS median,
       percentile_cont(0.9) WITHIN GROUP (ORDER BY secs) AS p90,
       GROUPING(person_id) AS all_people, GROUPING(day) AS all_days
FROM firsts
GROUP BY GROUPING SETS ((person_id, day), (person_id), (day), ())`

// staffOwnershipSQL is the current state, not the range: who owns which chats
// and how many of them wait on a reply.
const staffOwnershipSQL = `
SELECT assigned_user_id AS person_id, COUNT(*) AS owned, COUNT(waiting_since) AS waiting,
       MIN(waiting_since) AS oldest_waiting
FROM contacts
WHERE organization_id = ? AND deleted_at IS NULL
GROUP BY assigned_user_id`

type staffVolumeRow struct {
	IsPerson   bool
	PersonID   *uuid.UUID
	Day        *time.Time
	Source     *string
	Label      *string
	Messages   int64
	Chats      int64
	AllPeople  int
	AllDays    int
	AllSources int
}

type staffFirstReplyRow struct {
	PersonID  *uuid.UUID
	Day       *time.Time
	Replies   int64
	OnTarget  int64
	Median    *float64
	P90       *float64
	AllPeople int
	AllDays   int
}

type staffOwnershipRow struct {
	PersonID      *uuid.UUID
	Owned         int64
	Waiting       int64
	OldestWaiting *time.Time
}

// staffAccumulator fills rows as the query results arrive, keyed by person.
type staffAccumulator struct {
	days    []string
	dayIdx  map[string]int
	team    *StaffReportRow
	people  map[uuid.UUID]*StaffReportRow
	auto    *AutomaticReportRow
	orphans []uuid.UUID
}

func (acc *staffAccumulator) person(id uuid.UUID) *StaffReportRow {
	if row, ok := acc.people[id]; ok {
		return row
	}
	row := &StaffReportRow{UserID: id.String(), Daily: emptyDays(acc.days)}
	acc.people[id] = row
	acc.orphans = append(acc.orphans, id)
	return row
}

func (acc *staffAccumulator) day(daily []StaffDayStats, d *time.Time) *StaffDayStats {
	if d == nil {
		return nil
	}
	if i, ok := acc.dayIdx[d.Format("2006-01-02")]; ok {
		return &daily[i]
	}
	return nil
}

func emptyDays(days []string) []StaffDayStats {
	out := make([]StaffDayStats, len(days))
	for i, d := range days {
		out[i] = StaffDayStats{Date: d}
	}
	return out
}

func (a *App) buildStaffReport(orgID uuid.UUID, loc *time.Location, start, end time.Time) (*StaffReportResponse, error) {
	tz := loc.String()
	var days []string
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("2006-01-02"))
	}
	acc := &staffAccumulator{
		days:   days,
		dayIdx: make(map[string]int, len(days)),
		team:   &StaffReportRow{Daily: emptyDays(days)},
		people: map[uuid.UUID]*StaffReportRow{},
		auto:   &AutomaticReportRow{Sources: []AutomaticSource{}, Daily: emptyDays(days)},
	}
	for i, d := range days {
		acc.dayIdx[d] = i
	}

	// Everyone in the organization is listed, even with nothing to show.
	var members []models.User
	if err := a.DB.Model(&models.User{}).
		Select("users.id", "users.full_name", "users.is_active", "users.is_available").
		Joins("JOIN user_organizations uo ON uo.user_id = users.id AND uo.organization_id = ? AND uo.deleted_at IS NULL", orgID).
		Where("users.is_active = ?", true).
		Find(&members).Error; err != nil {
		return nil, err
	}
	for _, m := range members {
		acc.people[m.ID] = &StaffReportRow{
			UserID: m.ID.String(), Name: m.FullName, IsActive: m.IsActive, IsAvailable: m.IsAvailable,
			Daily: emptyDays(days),
		}
	}

	var volume []staffVolumeRow
	if err := a.DB.Raw(staffVolumeSQL, tz, orgID, start, end).Scan(&volume).Error; err != nil {
		return nil, err
	}
	for _, v := range volume {
		switch {
		case !v.IsPerson && v.AllSources == 0:
			// Automatic, by source.
			src := AutomaticSource{Kind: deref(v.Source), MessagesSent: v.Messages, ChatsReached: v.Chats}
			if src.Kind == AutomaticKindLabelled {
				src.Label = deref(v.Label)
			}
			acc.auto.Sources = append(acc.auto.Sources, src)
		case v.IsPerson && v.AllPeople == 0 && v.PersonID != nil:
			row := acc.person(*v.PersonID)
			if v.AllDays == 1 {
				row.MessagesSent, row.ChatsAnswered = v.Messages, v.Chats
			} else if d := acc.day(row.Daily, v.Day); d != nil {
				d.MessagesSent, d.ChatsAnswered = v.Messages, v.Chats
			}
		case !v.IsPerson:
			// Automatic over all sources, per day or overall.
			if v.AllDays == 1 {
				acc.auto.MessagesSent, acc.auto.ChatsReached = v.Messages, v.Chats
			} else if d := acc.day(acc.auto.Daily, v.Day); d != nil {
				d.MessagesSent, d.ChatsAnswered = v.Messages, v.Chats
			}
		default:
			// The team: rolled up over people.
			if v.AllDays == 1 {
				acc.team.MessagesSent, acc.team.ChatsAnswered = v.Messages, v.Chats
			} else if d := acc.day(acc.team.Daily, v.Day); d != nil {
				d.MessagesSent, d.ChatsAnswered = v.Messages, v.Chats
			}
		}
	}
	sort.Slice(acc.auto.Sources, func(i, j int) bool {
		return acc.auto.Sources[i].MessagesSent > acc.auto.Sources[j].MessagesSent
	})

	var firsts []staffFirstReplyRow
	if err := a.DB.Raw(staffFirstReplySQL, orgID, start, end, end, tz, start, staffReportTargetSecs).Scan(&firsts).Error; err != nil {
		return nil, err
	}
	for _, f := range firsts {
		row := acc.team
		if f.AllPeople == 0 && f.PersonID != nil {
			row = acc.person(*f.PersonID)
		}
		if f.AllDays == 1 {
			row.FirstReplies, row.OnTarget, row.MedianSecs, row.P90Secs = f.Replies, f.OnTarget, f.Median, f.P90
		} else if d := acc.day(row.Daily, f.Day); d != nil {
			d.FirstReplies, d.OnTarget, d.MedianSecs, d.P90Secs = f.Replies, f.OnTarget, f.Median, f.P90
		}
	}

	var owned []staffOwnershipRow
	if err := a.DB.Raw(staffOwnershipSQL, orgID).Scan(&owned).Error; err != nil {
		return nil, err
	}
	unassigned := StaffReportRow{Daily: []StaffDayStats{}}
	for _, o := range owned {
		row := &unassigned
		if o.PersonID != nil {
			row = acc.person(*o.PersonID)
		}
		row.OwnedChats, row.WaitingNow, row.OldestWaiting = o.Owned, o.Waiting, o.OldestWaiting
		acc.team.OwnedChats += o.Owned
		acc.team.WaitingNow += o.Waiting
		if o.OldestWaiting != nil && (acc.team.OldestWaiting == nil || o.OldestWaiting.Before(*acc.team.OldestWaiting)) {
			acc.team.OldestWaiting = o.OldestWaiting
		}
	}

	// Name people who sent or own something but have left the organization.
	if len(acc.orphans) > 0 {
		var gone []models.User
		a.DB.Unscoped().Select("id", "full_name").Where("id IN ?", acc.orphans).Find(&gone)
		for _, u := range gone {
			acc.people[u.ID].Name = u.FullName
		}
	}

	people := make([]StaffReportRow, 0, len(acc.people))
	for _, row := range acc.people {
		people = append(people, *row)
	}
	sort.Slice(people, func(i, j int) bool {
		if people[i].ChatsAnswered != people[j].ChatsAnswered {
			return people[i].ChatsAnswered > people[j].ChatsAnswered
		}
		return people[i].Name < people[j].Name
	})

	return &StaffReportResponse{
		Timezone:   loc.String(),
		Days:       days,
		TargetSecs: staffReportTargetSecs,
		SlowSecs:   staffReportSlowSecs,
		Team:       *acc.team,
		People:     people,
		Unassigned: unassigned,
		Automatic:  *acc.auto,
	}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
