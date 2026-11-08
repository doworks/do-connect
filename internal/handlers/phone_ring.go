package handlers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/push"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Incoming calls ring staff phones through silent pushes: the app shows its
// own full-screen ringing, answers through the same /call-transfers/{id}/connect
// as a browser, and stops ringing on a "call_end" push.
const (
	// A ring the phone gets after this is dropped: the call has moved on.
	phoneRingTTL = 30 * time.Second
	// A stop is worth delivering a little longer, so a phone that was out of
	// reach doesn't ring for a call that's long gone.
	phoneStopTTL = 2 * time.Minute
	// How long the server remembers which phones it rang for a transfer.
	phoneRingMemory = 10 * time.Minute
)

type ringedPhone struct {
	DeviceID  uuid.UUID
	UserID    uuid.UUID
	PushToken string
}

// phoneRings remembers, per transfer, the phones rung and whether the transfer
// is over. One server process handles a call from start to end, so memory is
// enough.
type phoneRings struct {
	mu        sync.Mutex
	transfers map[string]*transferRing
}

type transferRing struct {
	phones  []ringedPhone
	over    bool
	touched time.Time
}

func (p *phoneRings) get(transferID string) *transferRing {
	if p.transfers == nil {
		p.transfers = map[string]*transferRing{}
	}
	now := time.Now()
	for id, ring := range p.transfers {
		if now.Sub(ring.touched) > phoneRingMemory {
			delete(p.transfers, id)
		}
	}
	ring := p.transfers[transferID]
	if ring == nil {
		ring = &transferRing{}
		p.transfers[transferID] = ring
	}
	ring.touched = now
	return ring
}

// RingPhones rings the phones of the staff offered a call (calling.PhoneRinger).
func (a *App) RingPhones(orgID uuid.UUID, userIDs []uuid.UUID, offer map[string]any) {
	transferID, _ := offer["id"].(string)
	if a.Push == nil || transferID == "" {
		return
	}
	a.phoneRings.mu.Lock()
	over := a.phoneRings.get(transferID).over
	a.phoneRings.mu.Unlock()
	if over {
		return
	}

	phones, err := a.callPhones(orgID, userIDs)
	if err != nil {
		a.Log.Error("Failed to find phones to ring", "error", err, "transfer_id", transferID)
		return
	}
	if len(phones) == 0 {
		return
	}
	data := a.ringData(orgID, offer)

	a.phoneRings.mu.Lock()
	ring := a.phoneRings.get(transferID)
	if ring.over {
		a.phoneRings.mu.Unlock()
		return
	}
	ring.phones = append(ring.phones, phones...)
	a.phoneRings.mu.Unlock()

	a.sendToPhones(phones, data, phoneRingTTL)
	a.Log.Info("Rang phones for a call", "transfer_id", transferID, "phones", len(phones))
}

// StopRinging stops phones rung for a transfer (calling.PhoneRinger).
func (a *App) StopRinging(orgID uuid.UUID, transferID string, userIDs []uuid.UUID) {
	if a.Push == nil || transferID == "" {
		return
	}
	only := map[uuid.UUID]bool{}
	for _, id := range userIDs {
		only[id] = true
	}
	a.phoneRings.mu.Lock()
	ring := a.phoneRings.get(transferID)
	if userIDs == nil {
		ring.over = true
	}
	var stop, keep []ringedPhone
	for _, phone := range ring.phones {
		if userIDs == nil || only[phone.UserID] {
			stop = append(stop, phone)
		} else {
			keep = append(keep, phone)
		}
	}
	ring.phones = keep
	a.phoneRings.mu.Unlock()

	if len(stop) > 0 {
		a.sendToPhones(stop, map[string]string{"type": "call_end", "transfer_id": transferID}, phoneStopTTL)
	}
}

// callPhones lists the phones to ring: signed in, with alerts on, set to ring,
// of active staff who may take calls. A nil list means everyone in the
// organization who may.
func (a *App) callPhones(orgID uuid.UUID, userIDs []uuid.UUID) ([]ringedPhone, error) {
	if userIDs != nil && len(userIDs) == 0 {
		return nil, nil
	}
	query := a.DB.Table("mobile_devices AS d").
		Select("d.id AS device_id, d.user_id, d.push_token").
		Joins("JOIN users u ON u.id = d.user_id AND u.is_active = ? AND u.deleted_at IS NULL", true).
		Where("d.organization_id = ? AND d.revoked_at IS NULL AND d.push_token <> '' AND d.rings = ?", orgID, true).
		Order("d.last_seen_at DESC").
		Limit(maxPushDevices)
	if userIDs != nil {
		query = query.Where("d.user_id IN ?", userIDs)
	}
	var rows []ringedPhone
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	allowed := map[uuid.UUID]bool{}
	out := rows[:0]
	for _, row := range rows {
		ok, seen := allowed[row.UserID]
		if !seen {
			ok = a.HasPermission(row.UserID, models.ResourceCallTransfers, models.ActionWrite, orgID)
			allowed[row.UserID] = ok
		}
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

// ringData is what the phone needs to show the call before it has signed in
// to anything: who is calling, and the transfer to answer.
func (a *App) ringData(orgID uuid.UUID, offer map[string]any) map[string]string {
	text := func(key string) string {
		v, _ := offer[key].(string)
		return v
	}
	data := map[string]string{
		"type":             "call",
		"transfer_id":      text("id"),
		"call_log_id":      text("call_log_id"),
		"contact_id":       text("contact_id"),
		"caller_phone":     text("caller_phone"),
		"whatsapp_account": text("whatsapp_account"),
		"offered_at":       time.Now().UTC().Format(time.RFC3339),
	}
	if contactID, err := uuid.Parse(text("contact_id")); err == nil {
		var contact models.Contact
		if a.DB.Select("profile_name, phone_number").Where("id = ? AND organization_id = ?", contactID, orgID).First(&contact).Error == nil {
			name, _ := a.MaskContactFields(orgID, contact.ProfileName, contact.PhoneNumber)
			data["caller_name"] = name
		}
	}
	return data
}

func (a *App) sendToPhones(phones []ringedPhone, data map[string]string, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, phone := range phones {
		wg.Add(1)
		go func(phone ringedPhone) {
			defer wg.Done()
			sendCtx, cancelSend := context.WithTimeout(ctx, pushSendTimeout)
			defer cancelSend()
			err := a.Push.Send(sendCtx, push.Message{Token: phone.PushToken, Silent: true, TTL: ttl, Data: data})
			switch {
			case err == nil:
			case errors.Is(err, push.ErrInvalidToken):
				a.dropPushToken(pushDevice{ID: phone.DeviceID, UserID: phone.UserID, PushToken: phone.PushToken})
			default:
				a.Log.Warn("Call push not delivered", "error", err, "device_id", phone.DeviceID, "type", data["type"])
			}
		}(phone)
	}
	wg.Wait()
}

// MobileRingRequest turns ringing on this phone on or off.
type MobileRingRequest struct {
	Rings bool `json:"rings"`
}

// UpdateMobileRing sets whether calls ring on the signed-in phone. Staff who
// keep a phone for chats only, or answer at a desk, turn it off.
func (a *App) UpdateMobileRing(r *fastglue.Request) error {
	deviceID, ok := middleware.GetDeviceID(r)
	if !ok {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Only a signed-in phone can change its ringing", nil, "")
	}
	var req MobileRingRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}
	if err := a.DB.Model(&models.MobileDevice{}).Where("id = ?", deviceID).Update("rings", req.Rings).Error; err != nil {
		a.Log.Error("Failed to update phone ringing", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update ringing", nil, "")
	}
	return r.SendEnvelope(map[string]bool{"rings": req.Rings})
}
