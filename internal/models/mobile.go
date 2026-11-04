package models

import (
	"time"

	"github.com/google/uuid"
)

// MobileDevice is a phone signed in through the mobile app. Each device holds
// one long-lived refresh token, stored only as a SHA-256 hash. The token is
// rotated on every refresh; the token it replaced stays usable once, until
// PreviousValidUntil, so a phone that never received the new token (dropped
// connection, crash before saving) is not signed out.
type MobileDevice struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	OrganizationID uuid.UUID `gorm:"type:uuid;index;not null" json:"organization_id"`
	UserID         uuid.UUID `gorm:"type:uuid;index;not null" json:"user_id"`

	Name       string `gorm:"size:100" json:"name"`
	Model      string `gorm:"size:100" json:"model"`
	Platform   string `gorm:"size:20" json:"platform"`
	AppVersion string `gorm:"size:40" json:"app_version"`
	PushToken  string `gorm:"size:4096" json:"-"`

	TokenHash          string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	PreviousTokenHash  *string    `gorm:"size:64;index" json:"-"`
	PreviousValidUntil *time.Time `json:"-"`

	LastSeenAt time.Time  `json:"last_seen_at"`
	RevokedAt  *time.Time `gorm:"index" json:"revoked_at,omitempty"`
	RevokedBy  *uuid.UUID `gorm:"type:uuid" json:"revoked_by,omitempty"`

	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (MobileDevice) TableName() string {
	return "mobile_devices"
}
