package handlers_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// staffMsg saves a message with its sender, as the send paths record them.
func staffMsg(t *testing.T, app *handlers.App, orgID, contactID uuid.UUID, dir models.Direction, at time.Time, userID *uuid.UUID, label string, meta models.JSONB) {
	t.Helper()
	if meta == nil {
		meta = models.JSONB{}
	}
	msg := &models.Message{
		BaseModel:       models.BaseModel{ID: uuid.New(), CreatedAt: at},
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "test-account",
		Direction:       dir,
		MessageType:     models.MessageTypeText,
		Content:         "Hello",
		Status:          models.MessageStatusSent,
		SentByUserID:    userID,
		SentByLabel:     label,
		Metadata:        meta,
	}
	require.NoError(t, app.DB.Create(msg).Error)
}

func staffReportOrg(t *testing.T, app *handlers.App, timezone string) (*models.Organization, *models.User) {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	if timezone != "" {
		require.NoError(t, app.DB.Model(org).Update("settings", models.JSONB{"timezone": timezone}).Error)
	}
	role := testutil.CreateTestRoleExact(t, app.DB, org.ID, "Report Manager", false, false, getAnalyticsPermissions(t, app))
	manager := testutil.CreateTestUser(t, app.DB, org.ID,
		testutil.WithEmail(testutil.UniqueEmail("staff-report-mgr")),
		testutil.WithFullName("Manager"),
		testutil.WithRoleID(&role.ID),
	)
	return org, manager
}

func staffMember(t *testing.T, app *handlers.App, orgID uuid.UUID, name string) *models.User {
	t.Helper()
	return testutil.CreateTestUser(t, app.DB, orgID,
		testutil.WithEmail(testutil.UniqueEmail("staff-report")),
		testutil.WithFullName(name),
	)
}

func getStaffReport(t *testing.T, app *handlers.App, orgID, userID uuid.UUID, from, to string) (int, handlers.StaffReportResponse) {
	t.Helper()
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, orgID, userID)
	if from != "" {
		testutil.SetQueryParam(req, "from", from)
		testutil.SetQueryParam(req, "to", to)
	}
	require.NoError(t, app.GetStaffReport(req))
	var resp struct {
		Data handlers.StaffReportResponse `json:"data"`
	}
	status := testutil.GetResponseStatusCode(req)
	if status == fasthttp.StatusOK {
		require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
	}
	return status, resp.Data
}

func personRow(t *testing.T, resp handlers.StaffReportResponse, id uuid.UUID) handlers.StaffReportRow {
	t.Helper()
	for _, p := range resp.People {
		if p.UserID == id.String() {
			return p
		}
	}
	t.Fatalf("no row for %s", id)
	return handlers.StaffReportRow{}
}

func dayRow(t *testing.T, row handlers.StaffReportRow, date string) handlers.StaffDayStats {
	t.Helper()
	for _, d := range row.Daily {
		if d.Date == date {
			return d
		}
	}
	t.Fatalf("no day %s", date)
	return handlers.StaffDayStats{}
}

func ptrVal(t *testing.T, v *float64) float64 {
	t.Helper()
	require.NotNil(t, v)
	return *v
}

func TestApp_GetStaffReport_PeopleAutomaticAndFirstReplies(t *testing.T) {
	app := newTestApp(t)
	org, manager := staffReportOrg(t, app, "Asia/Bahrain")
	alice := staffMember(t, app, org.ID, "Alice")
	bob := staffMember(t, app, org.ID, "Bob")

	// 09:00 in Bahrain on 10 March.
	base := time.Date(2026, 3, 10, 6, 0, 0, 0, time.UTC)
	var aliceFirst *models.Contact

	// Alice answers five waits: 60s, 120s, 180s, 240s and 600s.
	for i, secs := range []int{60, 120, 180, 240, 600} {
		c := testutil.CreateTestContact(t, app.DB, org.ID)
		start := base.Add(time.Duration(i) * time.Hour)
		staffMsg(t, app, org.ID, c.ID, models.DirectionIncoming, start, nil, "", nil)
		if i == 0 {
			aliceFirst = c
			// A reminder sent through Alice's API key is automatic: it neither
			// counts as her work nor ends the patient's wait.
			staffMsg(t, app, org.ID, c.ID, models.DirectionOutgoing, start.Add(30*time.Second), &alice.ID, "Automatic", nil)
		}
		if i == 4 {
			// A second message from the patient while waiting doesn't restart the clock.
			staffMsg(t, app, org.ID, c.ID, models.DirectionIncoming, start.Add(5*time.Minute), nil, "", nil)
		}
		staffMsg(t, app, org.ID, c.ID, models.DirectionOutgoing, start.Add(time.Duration(secs)*time.Second), &alice.ID, "", nil)
		if i == 0 {
			// A follow-up is a message sent, not another first reply.
			staffMsg(t, app, org.ID, c.ID, models.DirectionOutgoing, start.Add(time.Duration(secs+5)*time.Second), &alice.ID, "", nil)
		}
	}

	// Bob: a 1200s first reply; a chat the chatbot answered first (not his first
	// reply); and a wait a campaign message didn't end (300s).
	b1 := testutil.CreateTestContact(t, app.DB, org.ID)
	staffMsg(t, app, org.ID, b1.ID, models.DirectionIncoming, base, nil, "", nil)
	staffMsg(t, app, org.ID, b1.ID, models.DirectionOutgoing, base.Add(1200*time.Second), &bob.ID, "", nil)

	b2 := testutil.CreateTestContact(t, app.DB, org.ID)
	staffMsg(t, app, org.ID, b2.ID, models.DirectionIncoming, base, nil, "", nil)
	staffMsg(t, app, org.ID, b2.ID, models.DirectionOutgoing, base.Add(10*time.Second), nil, "", nil)
	staffMsg(t, app, org.ID, b2.ID, models.DirectionOutgoing, base.Add(30*time.Minute), &bob.ID, "", nil)

	// Bob's message saved before labels existed carries NULL; it's still his.
	require.NoError(t, app.DB.Model(&models.Message{}).
		Where("contact_id = ? AND sent_by_user_id = ?", b1.ID, bob.ID).
		Update("sent_by_label", nil).Error)

	b3 := testutil.CreateTestContact(t, app.DB, org.ID)
	staffMsg(t, app, org.ID, b3.ID, models.DirectionIncoming, base, nil, "", nil)
	staffMsg(t, app, org.ID, b3.ID, models.DirectionOutgoing, base.Add(5*time.Second), nil, "", models.JSONB{"campaign_id": uuid.New().String()})
	staffMsg(t, app, org.ID, b3.ID, models.DirectionOutgoing, base.Add(300*time.Second), &bob.ID, "", nil)

	// Who owns what, and what waits now.
	waitingSince := time.Now().Add(-2 * time.Hour)
	require.NoError(t, app.DB.Model(aliceFirst).Updates(map[string]any{"assigned_user_id": alice.ID, "waiting_since": waitingSince}).Error)
	require.NoError(t, app.DB.Model(b1).Update("assigned_user_id", bob.ID).Error)
	lone := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(lone).Update("waiting_since", time.Now().Add(-10*time.Minute)).Error)

	status, resp := getStaffReport(t, app, org.ID, manager.ID, "2026-03-09", "2026-03-11")
	require.Equal(t, fasthttp.StatusOK, status)

	assert.Equal(t, "Asia/Bahrain", resp.Timezone)
	assert.Equal(t, []string{"2026-03-09", "2026-03-10", "2026-03-11"}, resp.Days)
	assert.Equal(t, 900, resp.TargetSecs)

	a := personRow(t, resp, alice.ID)
	assert.Equal(t, "Alice", a.Name)
	assert.Equal(t, int64(6), a.MessagesSent, "the reminder sent with her key is not hers")
	assert.Equal(t, int64(5), a.ChatsAnswered)
	assert.Equal(t, int64(5), a.FirstReplies)
	assert.Equal(t, int64(5), a.OnTarget)
	assert.InDelta(t, 180, ptrVal(t, a.MedianSecs), 0.001)
	// p90 of 60,120,180,240,600 interpolates 240 + 0.6*(600-240).
	assert.InDelta(t, 456, ptrVal(t, a.P90Secs), 0.001)
	assert.Equal(t, int64(1), a.OwnedChats)
	assert.Equal(t, int64(1), a.WaitingNow)
	require.NotNil(t, a.OldestWaiting)
	assert.WithinDuration(t, waitingSince, *a.OldestWaiting, time.Second)
	assert.Equal(t, int64(6), dayRow(t, a, "2026-03-10").MessagesSent)
	assert.Equal(t, int64(0), dayRow(t, a, "2026-03-09").MessagesSent)
	assert.Nil(t, dayRow(t, a, "2026-03-11").MedianSecs)

	b := personRow(t, resp, bob.ID)
	assert.Equal(t, int64(3), b.MessagesSent)
	assert.Equal(t, int64(3), b.ChatsAnswered)
	assert.Equal(t, int64(2), b.FirstReplies, "the chatbot answered b2 first")
	assert.Equal(t, int64(1), b.OnTarget)
	assert.InDelta(t, 750, ptrVal(t, b.MedianSecs), 0.001)
	assert.InDelta(t, 1110, ptrVal(t, b.P90Secs), 0.001)
	assert.Equal(t, int64(1), b.OwnedChats)
	assert.Equal(t, int64(0), b.WaitingNow)

	m := personRow(t, resp, manager.ID)
	assert.Equal(t, int64(0), m.MessagesSent)
	assert.Nil(t, m.MedianSecs)

	// The team: 60,120,180,240,300,600,1200.
	assert.Equal(t, int64(9), resp.Team.MessagesSent)
	assert.Equal(t, int64(8), resp.Team.ChatsAnswered)
	assert.Equal(t, int64(7), resp.Team.FirstReplies)
	assert.InDelta(t, 240, ptrVal(t, resp.Team.MedianSecs), 0.001)
	assert.InDelta(t, 840, ptrVal(t, resp.Team.P90Secs), 0.001)
	assert.InDelta(t, 240, ptrVal(t, dayRow(t, resp.Team, "2026-03-10").MedianSecs), 0.001)
	assert.Equal(t, int64(9), resp.Team.OwnedChats)
	assert.Equal(t, int64(2), resp.Team.WaitingNow)

	assert.Equal(t, int64(7), resp.Unassigned.OwnedChats)
	assert.Equal(t, int64(1), resp.Unassigned.WaitingNow)

	// Automatic: Alice's key's reminder, the chatbot reply and the campaign.
	assert.Equal(t, int64(3), resp.Automatic.MessagesSent)
	assert.Equal(t, int64(3), resp.Automatic.ChatsReached)
	assert.Equal(t, int64(3), dayRow(t, handlers.StaffReportRow{Daily: resp.Automatic.Daily}, "2026-03-10").MessagesSent)
	kinds := map[string]int64{}
	for _, s := range resp.Automatic.Sources {
		kinds[s.Kind+":"+s.Label] = s.MessagesSent
	}
	assert.Equal(t, map[string]int64{"labelled:Automatic": 1, "campaign:": 1, "other:": 1}, kinds)
}

func TestApp_GetStaffReport_DaysFollowTheTimezone(t *testing.T) {
	app := newTestApp(t)
	org, manager := staffReportOrg(t, app, "Asia/Bahrain")
	alice := staffMember(t, app, org.ID, "Alice")

	// 23:50 → 23:58 on the 10th in Bahrain (UTC+3).
	c1 := testutil.CreateTestContact(t, app.DB, org.ID)
	staffMsg(t, app, org.ID, c1.ID, models.DirectionIncoming, time.Date(2026, 3, 10, 20, 50, 0, 0, time.UTC), nil, "", nil)
	staffMsg(t, app, org.ID, c1.ID, models.DirectionOutgoing, time.Date(2026, 3, 10, 20, 58, 0, 0, time.UTC), &alice.ID, "", nil)
	// 23:55 on the 10th → 00:05 on the 11th: the reply belongs to the 11th, and
	// the wait that began the day before still counts in full.
	c2 := testutil.CreateTestContact(t, app.DB, org.ID)
	staffMsg(t, app, org.ID, c2.ID, models.DirectionIncoming, time.Date(2026, 3, 10, 20, 55, 0, 0, time.UTC), nil, "", nil)
	staffMsg(t, app, org.ID, c2.ID, models.DirectionOutgoing, time.Date(2026, 3, 10, 21, 5, 0, 0, time.UTC), &alice.ID, "", nil)

	_, day10 := getStaffReport(t, app, org.ID, manager.ID, "2026-03-10", "2026-03-10")
	a := personRow(t, day10, alice.ID)
	assert.Equal(t, int64(1), a.MessagesSent)
	assert.Equal(t, int64(1), a.FirstReplies)
	assert.InDelta(t, 480, ptrVal(t, a.MedianSecs), 0.001)

	_, day11 := getStaffReport(t, app, org.ID, manager.ID, "2026-03-11", "2026-03-11")
	a = personRow(t, day11, alice.ID)
	assert.Equal(t, int64(1), a.MessagesSent)
	assert.InDelta(t, 600, ptrVal(t, a.MedianSecs), 0.001)

	_, both := getStaffReport(t, app, org.ID, manager.ID, "2026-03-10", "2026-03-11")
	a = personRow(t, both, alice.ID)
	assert.Equal(t, int64(2), a.MessagesSent)
	assert.Equal(t, int64(1), dayRow(t, a, "2026-03-10").MessagesSent)
	assert.Equal(t, int64(1), dayRow(t, a, "2026-03-11").MessagesSent)
	assert.InDelta(t, 540, ptrVal(t, a.MedianSecs), 0.001)

	// Without its own timezone an organization uses the server default; in UTC
	// both replies fall on the 10th.
	app.Config.App.DefaultTimezone = "UTC"
	require.NoError(t, app.DB.Model(org).Update("settings", models.JSONB{}).Error)
	_, utc := getStaffReport(t, app, org.ID, manager.ID, "2026-03-10", "2026-03-10")
	assert.Equal(t, "UTC", utc.Timezone)
	assert.Equal(t, int64(2), personRow(t, utc, alice.ID).MessagesSent)
}

func TestApp_GetStaffReport_OrganizationsStaySeparate(t *testing.T) {
	app := newTestApp(t)
	org, manager := staffReportOrg(t, app, "UTC")
	alice := staffMember(t, app, org.ID, "Alice")
	c := testutil.CreateTestContact(t, app.DB, org.ID)
	at := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	staffMsg(t, app, org.ID, c.ID, models.DirectionIncoming, at, nil, "", nil)
	staffMsg(t, app, org.ID, c.ID, models.DirectionOutgoing, at.Add(time.Minute), &alice.ID, "", nil)

	other := testutil.CreateTestOrganization(t, app.DB)
	stranger := staffMember(t, app, other.ID, "Stranger")
	oc := testutil.CreateTestContact(t, app.DB, other.ID)
	require.NoError(t, app.DB.Model(oc).Updates(map[string]any{"assigned_user_id": stranger.ID, "waiting_since": time.Now()}).Error)
	for i := 0; i < 4; i++ {
		staffMsg(t, app, other.ID, oc.ID, models.DirectionIncoming, at.Add(time.Duration(i)*time.Hour), nil, "", nil)
		staffMsg(t, app, other.ID, oc.ID, models.DirectionOutgoing, at.Add(time.Duration(i)*time.Hour+time.Hour/2), &stranger.ID, "", nil)
		staffMsg(t, app, other.ID, oc.ID, models.DirectionOutgoing, at.Add(time.Duration(i)*time.Hour+time.Minute), nil, "Automatic", nil)
	}

	status, resp := getStaffReport(t, app, org.ID, manager.ID, "2026-03-10", "2026-03-10")
	require.Equal(t, fasthttp.StatusOK, status)
	assert.Equal(t, int64(1), resp.Team.MessagesSent)
	assert.Equal(t, int64(1), resp.Team.FirstReplies)
	assert.Equal(t, int64(0), resp.Team.WaitingNow)
	assert.Equal(t, int64(0), resp.Automatic.MessagesSent)
	for _, p := range resp.People {
		assert.NotEqual(t, stranger.ID.String(), p.UserID)
	}
	assert.Len(t, resp.People, 2) // the manager and Alice
}

func TestApp_GetStaffReport_Access(t *testing.T) {
	app := newTestApp(t)
	org, manager := staffReportOrg(t, app, "")

	// Agents without analytics:read can't see colleagues' numbers.
	agent := staffMember(t, app, org.ID, "Agent")
	status, _ := getStaffReport(t, app, org.ID, agent.ID, "", "")
	assert.Equal(t, fasthttp.StatusForbidden, status)

	req := testutil.NewGETRequest(t)
	require.NoError(t, app.GetStaffReport(req))
	assert.Equal(t, fasthttp.StatusUnauthorized, testutil.GetResponseStatusCode(req))

	status, _ = getStaffReport(t, app, org.ID, manager.ID, "10-03-2026", "2026-03-11")
	assert.Equal(t, fasthttp.StatusBadRequest, status)
	status, _ = getStaffReport(t, app, org.ID, manager.ID, "2026-03-11", "2026-03-10")
	assert.Equal(t, fasthttp.StatusBadRequest, status)
	status, _ = getStaffReport(t, app, org.ID, manager.ID, "2024-01-01", "2026-03-10")
	assert.Equal(t, fasthttp.StatusBadRequest, status)

	// Without dates it's this month so far, in the server's default zone.
	status, resp := getStaffReport(t, app, org.ID, manager.ID, "", "")
	assert.Equal(t, fasthttp.StatusOK, status)
	assert.Equal(t, "Asia/Bahrain", resp.Timezone)
	assert.NotEmpty(t, resp.Days)
	assert.Equal(t, resp.From, resp.Days[0])
	assert.Equal(t, resp.To, resp.Days[len(resp.Days)-1])
}
