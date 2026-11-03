package handlers_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

type chatFixture struct {
	app     *handlers.App
	org     *models.Organization
	user    *models.User
	account *models.WhatsAppAccount
}

func newChatFixture(t *testing.T) chatFixture {
	t.Helper()
	mockServer := newMockWhatsAppServer()
	app := newMsgTestApp(t, mockServer)
	t.Cleanup(func() {
		app.WaitForBackgroundTasks()
		mockServer.close()
	})
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID), testutil.WithFullName("Sara Ahmed"))
	account := createTestAccount(t, app, org.ID)
	return chatFixture{app: app, org: org, user: user, account: account}
}

func (f chatFixture) authed(req *fastglue.Request) *fastglue.Request {
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	return req
}

func (f chatFixture) contact(t *testing.T) *models.Contact {
	t.Helper()
	return testutil.CreateTestContactWith(t, f.app.DB, f.org.ID, testutil.WithContactAccount(f.account.Name))
}

func (f chatFixture) send(t *testing.T, contact *models.Contact, viaKey bool) handlers.MessageResponse {
	t.Helper()
	req := f.authed(testutil.NewJSONRequest(t, map[string]any{"type": "text", "content": map[string]string{"body": "Hello"}}))
	testutil.SetPathParam(req, "id", contact.ID.String())
	if viaKey {
		req.RequestCtx.SetUserValue(middleware.ContextKeyViaAPIKey, true)
	}
	require.NoError(t, f.app.SendMessage(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	var resp struct {
		Data handlers.MessageResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
	return resp.Data
}

func (f chatFixture) messages(t *testing.T, contact *models.Contact, viaKey bool) ([]handlers.MessageResponse, []byte) {
	t.Helper()
	req := f.authed(testutil.NewGETRequest(t))
	testutil.SetPathParam(req, "id", contact.ID.String())
	if viaKey {
		req.RequestCtx.SetUserValue(middleware.ContextKeyViaAPIKey, true)
	}
	require.NoError(t, f.app.GetMessages(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	var resp struct {
		Data struct {
			Messages []handlers.MessageResponse `json:"messages"`
		} `json:"data"`
	}
	body := testutil.GetResponseBody(req)
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Data.Messages, body
}

func (f chatFixture) incoming(t *testing.T, contact *models.Contact) {
	t.Helper()
	require.NoError(t, f.app.DB.Create(&models.Message{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  f.org.ID,
		WhatsAppAccount: f.account.Name,
		ContactID:       contact.ID,
		Direction:       models.DirectionIncoming,
		MessageType:     models.MessageTypeText,
		Content:         "Hi",
		Status:          models.MessageStatusDelivered,
	}).Error)
}

type listedContacts struct {
	Contacts     []handlers.ContactResponse `json:"contacts"`
	FilterCounts map[string]int64           `json:"filter_counts"`
}

func (f chatFixture) list(t *testing.T, filter string) listedContacts {
	t.Helper()
	req := f.authed(testutil.NewGETRequest(t))
	testutil.SetQueryParam(req, "limit", 50)
	if filter != "" {
		testutil.SetQueryParam(req, "filter", filter)
	}
	require.NoError(t, f.app.ListContacts(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	var resp struct {
		Data listedContacts `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
	return resp.Data
}

func (l listedContacts) find(id uuid.UUID) *handlers.ContactResponse {
	for i := range l.Contacts {
		if l.Contacts[i].ID == id {
			return &l.Contacts[i]
		}
	}
	return nil
}

func TestStaffNameOnMessages(t *testing.T) {
	t.Parallel()

	t.Run("a staff reply shows who sent it, live and in history", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		contact := f.contact(t)

		sent := f.send(t, contact, false)
		assert.Equal(t, "Sara Ahmed", sent.SentByName)
		assert.Empty(t, sent.SentByLabel)

		history, body := f.messages(t, contact, false)
		require.Len(t, history, 1)
		assert.Equal(t, "Sara Ahmed", history[0].SentByName)
		assert.NotContains(t, string(body), f.user.Email, "only the name goes to the client")
	})

	t.Run("staff who have left keep their name on old messages", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		contact := f.contact(t)
		f.send(t, contact, false)

		var other models.User
		require.NoError(t, f.app.DB.First(&other, f.user.ID).Error)
		role := testutil.CreateAdminRole(t, f.app.DB, f.org.ID)
		viewer := testutil.CreateTestUser(t, f.app.DB, f.org.ID, testutil.WithRoleID(&role.ID))
		require.NoError(t, f.app.DB.Delete(&other).Error)

		f.user = viewer
		history, _ := f.messages(t, contact, false)
		require.Len(t, history, 1)
		assert.Equal(t, "Sara Ahmed", history[0].SentByName)
	})

	t.Run("an API key send isn't credited to the key's owner", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		contact := f.contact(t)

		sent := f.send(t, contact, true)
		assert.Equal(t, "Automatic", sent.SentByLabel)
		assert.Empty(t, sent.SentByName)
	})
}

func TestWaitingOnStaff(t *testing.T) {
	t.Parallel()
	since := time.Now().Add(-time.Hour)

	t.Run("a staff reply ends the wait and records who replied", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		contact := f.contact(t)
		require.NoError(t, f.app.DB.Model(contact).Update("waiting_since", since).Error)

		f.send(t, contact, false)

		row := f.list(t, "").find(contact.ID)
		require.NotNil(t, row)
		assert.Nil(t, row.WaitingSince)
		assert.Equal(t, "Sara Ahmed", row.LastReplyBy)
		assert.Equal(t, "outgoing", row.LastDirection)
	})

	t.Run("an automatic message doesn't count as answering them", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		contact := f.contact(t)
		require.NoError(t, f.app.DB.Model(contact).Update("waiting_since", since).Error)

		f.send(t, contact, true)

		row := f.list(t, "").find(contact.ID)
		require.NotNil(t, row)
		assert.NotNil(t, row.WaitingSince)
		assert.Equal(t, "Automatic", row.LastReplyBy)
	})
}

func TestMarkUnread(t *testing.T) {
	t.Parallel()

	markUnread := func(t *testing.T, f chatFixture, contact *models.Contact) {
		t.Helper()
		req := f.authed(testutil.NewJSONRequest(t, map[string]any{}))
		testutil.SetPathParam(req, "id", contact.ID.String())
		require.NoError(t, f.app.MarkContactUnread(req))
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	}

	t.Run("flags a read chat until someone opens it", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		contact := f.contact(t)

		markUnread(t, f, contact)
		row := f.list(t, "unread").find(contact.ID)
		require.NotNil(t, row, "appears under Unread")
		assert.Equal(t, 1, row.UnreadCount)
		assert.True(t, row.MarkedUnread)

		f.messages(t, contact, false)
		assert.Nil(t, f.list(t, "unread").find(contact.ID))
	})

	t.Run("can't reach another organization's chat", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		other := newChatFixture(t)
		contact := other.contact(t)

		req := f.authed(testutil.NewJSONRequest(t, map[string]any{}))
		testutil.SetPathParam(req, "id", contact.ID.String())
		require.NoError(t, f.app.MarkContactUnread(req))
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
	})
}

func TestReadingChats(t *testing.T) {
	t.Parallel()

	t.Run("an integration looking something up leaves the chat unread", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		contact := f.contact(t)
		f.incoming(t, contact)

		f.messages(t, contact, true)
		assert.Equal(t, 1, f.list(t, "").find(contact.ID).UnreadCount)

		f.messages(t, contact, false)
		assert.Equal(t, 0, f.list(t, "").find(contact.ID).UnreadCount)
	})
}

func TestWorklistFilters(t *testing.T) {
	t.Parallel()
	f := newChatFixture(t)

	unread := f.contact(t)
	f.incoming(t, unread)
	waiting := f.contact(t)
	require.NoError(t, f.app.DB.Model(waiting).Update("waiting_since", time.Now()).Error)
	mine := f.contact(t)
	require.NoError(t, f.app.DB.Model(mine).Update("assigned_user_id", f.user.ID).Error)

	ids := func(l listedContacts) []uuid.UUID {
		out := []uuid.UUID{}
		for _, c := range l.Contacts {
			out = append(out, c.ID)
		}
		return out
	}

	assert.ElementsMatch(t, []uuid.UUID{unread.ID}, ids(f.list(t, "unread")))
	assert.ElementsMatch(t, []uuid.UUID{waiting.ID}, ids(f.list(t, "waiting")))
	assert.ElementsMatch(t, []uuid.UUID{mine.ID}, ids(f.list(t, "mine")))
	assert.ElementsMatch(t, []uuid.UUID{unread.ID, waiting.ID}, ids(f.list(t, "unassigned")))

	all := f.list(t, "")
	assert.Len(t, all.Contacts, 3)
	assert.Equal(t, map[string]int64{"unread": 1, "waiting": 1, "mine": 1, "unassigned": 2}, all.FilterCounts)
	assert.Equal(t, "Sara Ahmed", all.find(mine.ID).AssignedUserName)
}

func TestMessagingANewNumber(t *testing.T) {
	t.Parallel()

	setCountry := func(t *testing.T, f chatFixture) {
		t.Helper()
		f.org.Settings = models.JSONB{"default_country_code": "973"}
		require.NoError(t, f.app.DB.Model(f.org).Update("settings", f.org.Settings).Error)
	}
	create := func(t *testing.T, f chatFixture, body map[string]any) (*fastglue.Request, handlers.ContactResponse) {
		t.Helper()
		req := f.authed(testutil.NewJSONRequest(t, body))
		require.NoError(t, f.app.CreateContact(req))
		var resp struct {
			Data handlers.ContactResponse `json:"data"`
		}
		_ = json.Unmarshal(testutil.GetResponseBody(req), &resp)
		return req, resp.Data
	}

	t.Run("adds the country code and finds the same chat again", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		setCountry(t, f)

		_, first := create(t, f, map[string]any{"phone_number": "3311 2233", "open_existing": true})
		assert.Equal(t, "97333112233", first.PhoneNumber)

		for _, typed := range []string{"+973 3311 2233", "0097333112233", "٣٣١١٢٢٣٣"} {
			_, again := create(t, f, map[string]any{"phone_number": typed, "open_existing": true})
			assert.Equal(t, first.ID, again.ID, typed)
		}
	})

	t.Run("still refuses a duplicate when not asked to open it", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		setCountry(t, f)
		create(t, f, map[string]any{"phone_number": "33112233"})
		req, _ := create(t, f, map[string]any{"phone_number": "+97333112233"})
		assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(req))
	})

	t.Run("rejects text that isn't a number", func(t *testing.T) {
		t.Parallel()
		f := newChatFixture(t)
		req, _ := create(t, f, map[string]any{"phone_number": "Fatima"})
		assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
	})
}

func TestContactNameStaysPut(t *testing.T) {
	t.Parallel()
	f := newChatFixture(t)
	contact := f.contact(t)

	req := f.authed(testutil.NewJSONRequest(t, map[string]any{"profile_name": "AR · Fatima Hassan +1"}))
	testutil.SetPathParam(req, "id", contact.ID.String())
	require.NoError(t, f.app.UpdateContact(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var saved models.Contact
	require.NoError(t, f.app.DB.First(&saved, contact.ID).Error)
	assert.True(t, saved.NameLocked)
}

func TestClaimingAChat(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (chatFixture, *models.User) {
		t.Helper()
		f := newChatFixture(t)
		// A role with no contacts:write, like the default staff role.
		role := &models.CustomRole{BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: f.org.ID, Name: "Reception " + uuid.NewString()[:8]}
		require.NoError(t, f.app.DB.Create(role).Error)
		return f, testutil.CreateTestUser(t, f.app.DB, f.org.ID, testutil.WithRoleID(&role.ID))
	}
	assign := func(t *testing.T, f chatFixture, actor *models.User, contact *models.Contact, target uuid.UUID) int {
		t.Helper()
		req := testutil.NewJSONRequest(t, map[string]any{"user_id": target.String()})
		testutil.SetAuthContext(req, f.org.ID, actor.ID)
		testutil.SetPathParam(req, "id", contact.ID.String())
		require.NoError(t, f.app.AssignContact(req))
		return testutil.GetResponseStatusCode(req)
	}

	t.Run("staff can take a chat nobody owns", func(t *testing.T) {
		t.Parallel()
		f, staff := setup(t)
		contact := f.contact(t)
		assert.Equal(t, fasthttp.StatusOK, assign(t, f, staff, contact, staff.ID))
	})

	t.Run("but not a colleague's", func(t *testing.T) {
		t.Parallel()
		f, staff := setup(t)
		contact := f.contact(t)
		require.NoError(t, f.app.DB.Model(contact).Update("assigned_user_id", f.user.ID).Error)
		assert.Equal(t, fasthttp.StatusForbidden, assign(t, f, staff, contact, staff.ID))
	})

	t.Run("and can't assign to someone else", func(t *testing.T) {
		t.Parallel()
		f, staff := setup(t)
		contact := f.contact(t)
		assert.Equal(t, fasthttp.StatusForbidden, assign(t, f, staff, contact, f.user.ID))
	})
}
