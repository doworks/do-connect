package handlers

import (
	"github.com/fasthttp/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	ws "github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// newUpgrader creates a WebSocket upgrader that validates origins against the
// configured allowed origins. If no origins are configured, all are allowed.
func newUpgrader(allowedOrigins map[string]bool) websocket.FastHTTPUpgrader {
	return websocket.FastHTTPUpgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
			origin := string(ctx.Request.Header.Peek("Origin"))
			return middleware.IsOriginAllowed(origin, allowedOrigins)
		},
	}
}

// wsUpgrader returns a WebSocket upgrader configured with the app's allowed origins.
func (a *App) wsUpgrader() websocket.FastHTTPUpgrader {
	allowedOrigins := middleware.ParseAllowedOrigins(a.Config.Server.AllowedOrigins)
	return newUpgrader(allowedOrigins)
}

// WebSocketHandler handles WebSocket connections.
// Authentication is performed via message-based auth after the upgrade:
// the client must send {"type":"auth","payload":{"token":"<jwt>"}} within 5 seconds.
func (a *App) WebSocketHandler(r *fastglue.Request) error {
	// Upgrade to WebSocket immediately (unauthenticated)
	up := a.wsUpgrader()
	err := up.Upgrade(r.RequestCtx, func(conn *websocket.Conn) {
		// Create unauthenticated client — auth happens via first message
		client := ws.NewUnauthenticatedClient(a.WSHub, conn, a.validateWSTokenFn())

		// Start pumps in goroutines
		// Client self-registers with hub after successful auth message
		go client.WritePump()
		client.ReadPump() // Blocking - runs until connection closes
	})

	if err != nil {
		a.Log.Error("WebSocket upgrade failed", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "WebSocket upgrade failed", nil, "")
	}

	return nil
}

// validateWSTokenFn returns a function that validates the short-lived socket
// token from /auth/ws-token and returns user ID and organization ID. Other
// tokens signed with the same secret (access or refresh tokens) are refused,
// and a phone's token only while that phone is still signed in.
func (a *App) validateWSTokenFn() ws.AuthenticateFn {
	return func(tokenString string) (uuid.UUID, uuid.UUID, error) {
		token, err := jwt.ParseWithClaims(tokenString, &middleware.JWTClaims{}, func(token *jwt.Token) (any, error) {
			return []byte(a.Config.JWT.Secret), nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))

		if err != nil || !token.Valid {
			return uuid.Nil, uuid.Nil, err
		}

		claims, ok := token.Claims.(*middleware.JWTClaims)
		if !ok || claims.Subject != "ws" || claims.ID != "" {
			return uuid.Nil, uuid.Nil, jwt.ErrTokenInvalidClaims
		}

		if claims.DeviceID != nil {
			var active int64
			if err := a.DB.Model(&models.MobileDevice{}).
				Joins("JOIN users ON users.id = mobile_devices.user_id AND users.deleted_at IS NULL AND users.is_active = ?", true).
				Where("mobile_devices.id = ? AND mobile_devices.user_id = ? AND mobile_devices.revoked_at IS NULL", *claims.DeviceID, claims.UserID).
				Count(&active).Error; err != nil {
				return uuid.Nil, uuid.Nil, err
			}
			if active == 0 {
				return uuid.Nil, uuid.Nil, jwt.ErrTokenInvalidClaims
			}
		}

		return claims.UserID, claims.OrganizationID, nil
	}
}
