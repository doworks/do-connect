package contactutil

import (
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// applyWhatsAppName records the name the person set in WhatsApp. A contact named
// by staff or an integration keeps that name.
func applyWhatsAppName(db *gorm.DB, contact *models.Contact, name string) {
	if name == "" {
		return
	}
	updates := map[string]any{}
	if contact.WhatsAppName != name {
		updates["whats_app_name"] = name
	}
	if !contact.NameLocked && contact.ProfileName != name {
		updates["profile_name"] = name
	}
	if len(updates) > 0 {
		db.Model(contact).Updates(updates)
	}
}

// GetOrCreateContact finds or creates a contact for the given phone number.
// Merges behaviors from both handler and worker implementations:
//   - Normalizes phone (strips leading "+")
//   - Tries both normalized and +prefix forms
//   - Updates the WhatsApp name, and the profile name unless it's locked
//   - Handles race conditions on create by re-fetching
//   - Restores soft-deleted contacts if found
//
// Returns the contact, whether it was newly created, and any error.
func GetOrCreateContact(db *gorm.DB, orgID uuid.UUID, phoneNumber, profileName string) (*models.Contact, bool, error) {
	// Normalize phone number (remove + prefix if present)
	normalizedPhone := phoneNumber
	if len(normalizedPhone) > 0 && normalizedPhone[0] == '+' {
		normalizedPhone = normalizedPhone[1:]
	}

	// Try to find existing contact with normalized phone (including soft-deleted)
	var contact models.Contact
	if err := db.Unscoped().Where("organization_id = ? AND phone_number = ?", orgID, normalizedPhone).First(&contact).Error; err == nil {
		// Restore if soft-deleted
		if contact.DeletedAt.Valid {
			db.Unscoped().Model(&contact).Update("deleted_at", nil)
			contact.DeletedAt.Valid = false
		}
		applyWhatsAppName(db, &contact, profileName)
		return &contact, false, nil
	}

	// Also try with + prefix (contacts may have been stored with it)
	if err := db.Unscoped().Where("organization_id = ? AND phone_number = ?", orgID, "+"+normalizedPhone).First(&contact).Error; err == nil {
		// Restore if soft-deleted
		if contact.DeletedAt.Valid {
			db.Unscoped().Model(&contact).Update("deleted_at", nil)
			contact.DeletedAt.Valid = false
		}
		applyWhatsAppName(db, &contact, profileName)
		return &contact, false, nil
	}

	// Create new contact
	contact = models.Contact{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: orgID,
		PhoneNumber:    normalizedPhone,
		ProfileName:    profileName,
		WhatsAppName:   profileName,
	}
	if err := db.Create(&contact).Error; err != nil {
		// Race condition: another goroutine may have created the contact
		if err2 := db.Unscoped().Where("organization_id = ? AND phone_number = ?", orgID, normalizedPhone).First(&contact).Error; err2 == nil {
			// Restore if soft-deleted
			if contact.DeletedAt.Valid {
				db.Unscoped().Model(&contact).Update("deleted_at", nil)
				contact.DeletedAt.Valid = false
			}
			return &contact, false, nil
		}
		return nil, false, err
	}
	return &contact, true, nil
}

// FindContact finds a contact for the given phone number with both forms (normalized and +prefix).
func FindContact(db *gorm.DB, orgID uuid.UUID, phoneNumber string) (*models.Contact, error) {
	normalizedPhone := phoneNumber
	if len(normalizedPhone) > 0 && normalizedPhone[0] == '+' {
		normalizedPhone = normalizedPhone[1:]
	}

	var contact models.Contact
	if err := db.Where("organization_id = ? AND phone_number = ?", orgID, normalizedPhone).First(&contact).Error; err == nil {
		return &contact, nil
	}

	if err := db.Where("organization_id = ? AND phone_number = ?", orgID, "+"+normalizedPhone).First(&contact).Error; err == nil {
		return &contact, nil
	}

	return nil, gorm.ErrRecordNotFound
}
