// Package push sends notifications to the mobile app through Firebase Cloud
// Messaging (FCM HTTP v1), authenticated with a Google service account.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	fcmScope          = "https://www.googleapis.com/auth/firebase.messaging"
	defaultTokenURL   = "https://oauth2.googleapis.com/token"
	defaultSendURL    = "https://fcm.googleapis.com"
	tokenRefreshEarly = 5 * time.Minute
	assertionLifetime = time.Hour
)

// ErrInvalidToken means FCM no longer knows the device token (the app was
// uninstalled, or the token was replaced). The token should be dropped.
var ErrInvalidToken = errors.New("push token is no longer valid")

// Message is one notification to one device.
type Message struct {
	Token string
	Title string
	Body  string
	// Tag groups notifications on the phone: a new one with the same tag
	// replaces the one before.
	Tag       string
	ChannelID string
	Data      map[string]string
}

// Options override the Google endpoints, for tests.
type Options struct {
	TokenURL   string
	SendURL    string
	HTTPClient *http.Client
	Now        func() time.Time
}

// FCM sends messages for one Firebase project.
type FCM struct {
	projectID   string
	clientEmail string
	keyID       string
	key         any
	tokenURL    string
	sendURL     string
	client      *http.Client
	now         func() time.Time

	mu          sync.Mutex
	accessToken string
	expires     time.Time
}

type serviceAccount struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// LoadFCM reads a service-account key file downloaded from the Firebase console.
func LoadFCM(path string, opts Options) (*FCM, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read push credentials: %w", err)
	}
	return NewFCM(data, opts)
}

// NewFCM builds a sender from the contents of a service-account key file.
func NewFCM(credentials []byte, opts Options) (*FCM, error) {
	var sa serviceAccount
	if err := json.Unmarshal(credentials, &sa); err != nil {
		return nil, errors.New("push credentials are not a service-account JSON file")
	}
	if sa.Type != "" && sa.Type != "service_account" {
		return nil, fmt.Errorf("push credentials are of type %q, not a service account", sa.Type)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("push credentials need project_id, client_email and private_key")
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(sa.PrivateKey))
	if err != nil {
		return nil, errors.New("push credentials have an unreadable private key")
	}

	f := &FCM{
		projectID:   sa.ProjectID,
		clientEmail: sa.ClientEmail,
		keyID:       sa.PrivateKeyID,
		key:         key,
		tokenURL:    defaultTokenURL,
		sendURL:     defaultSendURL,
		client:      opts.HTTPClient,
		now:         opts.Now,
	}
	if sa.TokenURI != "" {
		f.tokenURL = sa.TokenURI
	}
	if opts.TokenURL != "" {
		f.tokenURL = opts.TokenURL
	}
	if opts.SendURL != "" {
		f.sendURL = strings.TrimRight(opts.SendURL, "/")
	}
	if f.client == nil {
		f.client = &http.Client{Timeout: 10 * time.Second}
	}
	if f.now == nil {
		f.now = time.Now
	}
	return f, nil
}

// ProjectID is the Firebase project the credentials belong to.
func (f *FCM) ProjectID() string { return f.projectID }

// Send delivers one message. It returns ErrInvalidToken when the device token
// should be forgotten.
func (f *FCM) Send(ctx context.Context, msg Message) error {
	return f.send(ctx, msg, false)
}

// Validate asks FCM to check a message without delivering it.
func (f *FCM) Validate(ctx context.Context, msg Message) error {
	return f.send(ctx, msg, true)
}

func (f *FCM) send(ctx context.Context, msg Message, validateOnly bool) error {
	token, err := f.token(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"validate_only": validateOnly,
		"message":       buildMessage(msg),
	})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/v1/projects/%s/messages:send", f.sendURL, url.PathEscape(f.projectID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("fcm send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// The cached access token may have been revoked; fetch a new one next time.
		f.mu.Lock()
		f.accessToken = ""
		f.mu.Unlock()
	}
	return sendError(resp.StatusCode, respBody)
}

// buildMessage is the FCM v1 message. The notification is shown by Android
// itself while the app is in the background or closed; the lock screen hides
// its text unless the phone allows private content there.
func buildMessage(msg Message) map[string]any {
	notification := map[string]any{
		"visibility": "PRIVATE",
	}
	if msg.ChannelID != "" {
		notification["channel_id"] = msg.ChannelID
	}
	if msg.Tag != "" {
		notification["tag"] = msg.Tag
	}
	m := map[string]any{
		"token": msg.Token,
		"notification": map[string]string{
			"title": msg.Title,
			"body":  msg.Body,
		},
		"android": map[string]any{
			"priority":     "HIGH",
			"notification": notification,
		},
	}
	if len(msg.Data) > 0 {
		m["data"] = msg.Data
	}
	return m
}

type fcmErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
		Details []struct {
			Type      string `json:"@type"`
			ErrorCode string `json:"errorCode"`
			Field     []struct {
				Field string `json:"field"`
			} `json:"fieldViolations"`
		} `json:"details"`
	} `json:"error"`
}

// sendError maps an FCM error response. UNREGISTERED always means the token is
// dead. INVALID_ARGUMENT only does when it is about the token itself: a bad
// payload is our fault and must not cost the phone its token.
func sendError(status int, body []byte) error {
	var parsed fcmErrorBody
	_ = json.Unmarshal(body, &parsed)
	code := parsed.Error.Status
	for _, d := range parsed.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
		}
	}
	switch code {
	case "UNREGISTERED":
		return ErrInvalidToken
	case "INVALID_ARGUMENT":
		if aboutToken(parsed) {
			return ErrInvalidToken
		}
	}
	if code == "" {
		code = http.StatusText(status)
	}
	return fmt.Errorf("fcm send failed: %d %s", status, code)
}

func aboutToken(parsed fcmErrorBody) bool {
	for _, d := range parsed.Error.Details {
		for _, v := range d.Field {
			if v.Field == "message.token" {
				return true
			}
		}
	}
	msg := strings.ToLower(parsed.Error.Message)
	return strings.Contains(msg, "registration token") || strings.Contains(msg, "message.token")
}

// token returns a cached OAuth2 access token, fetching a new one shortly
// before the old one expires.
func (f *FCM) token(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.accessToken != "" && now.Before(f.expires.Add(-tokenRefreshEarly)) {
		return f.accessToken, nil
	}

	claims := jwt.MapClaims{
		"iss":   f.clientEmail,
		"scope": fcmScope,
		"aud":   f.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(assertionLifetime).Unix(),
	}
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if f.keyID != "" {
		assertion.Header["kid"] = f.keyID
	}
	signed, err := assertion.SignedString(f.key)
	if err != nil {
		return "", fmt.Errorf("sign push assertion: %w", err)
	}

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signed},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("push access token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(respBody, &e)
		return "", fmt.Errorf("push access token refused: %d %s", resp.StatusCode, e.Error)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &tok); err != nil || tok.AccessToken == "" {
		return "", errors.New("push access token: unreadable answer")
	}
	if tok.ExpiresIn <= 0 {
		tok.ExpiresIn = 3600
	}
	f.accessToken = tok.AccessToken
	f.expires = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	return f.accessToken, nil
}
