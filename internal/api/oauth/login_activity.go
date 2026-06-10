package oauth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
)

// IP masks used to decide whether a sign-in comes from a familiar network,
// mirroring Mastodon's SuspiciousSignInDetector.
const (
	ipv4ToleranceMask = 16
	ipv6ToleranceMask = 64
)

const authenticationMethodPassword = "password"

// SetMailDeliverer enables suspicious sign-in notification emails.
func (h *Handler) SetMailDeliverer(deliverer mailer.Deliverer) {
	if h == nil {
		return
	}
	h.mail = deliverer
}

// recordLoginSuccess persists a successful login activity, updates the user's
// sign-in bookkeeping, and notifies the user when the IP is unfamiliar.
func (h *Handler) recordLoginSuccess(ctx context.Context, r *http.Request, user db.User) {
	if h.queries == nil {
		return
	}

	addr, ok := parseClientAddr(r)
	suspicious := false
	if ok && h.suspiciousSignIn(ctx, user, addr) {
		suspicious = true
	}

	var ipPtr *netip.Addr
	if ok {
		ipPtr = &addr
	}
	if _, err := h.queries.CreateLoginActivity(ctx, db.CreateLoginActivityParams{
		UserID:               user.ID,
		AuthenticationMethod: pgtype.Text{String: authenticationMethodPassword, Valid: true},
		Success:              pgtype.Bool{Bool: true, Valid: true},
		Ip:                   ipPtr,
		UserAgent:            requestUserAgent(r),
	}); err != nil {
		slog.Error("Failed to record login activity", "error", err)
	}

	if err := h.queries.UpdateUserSignIn(ctx, user.ID); err != nil {
		slog.Error("Failed to update user sign-in counters", "error", err)
	}

	if suspicious && h.mail != nil {
		locale := ""
		if user.Locale.Valid {
			locale = user.Locale.String
		}
		mail := mailer.Mail{
			Kind:   mailer.KindSuspiciousSignIn,
			To:     user.Email,
			Locale: locale,
			Params: map[string]string{
				"IP":        addr.String(),
				"UserAgent": r.UserAgent(),
				"Timestamp": time.Now().UTC().Format("Jan 2, 2006 15:04 MST"),
			},
		}
		if err := h.mail.Deliver(ctx, mail); err != nil {
			slog.Error("Failed to enqueue suspicious sign-in mail", "error", err)
		}
	}
}

// recordLoginFailure persists a failed password authentication attempt.
func (h *Handler) recordLoginFailure(ctx context.Context, r *http.Request, user db.User, reason string) {
	if h.queries == nil || user.ID == 0 {
		return
	}
	var ipPtr *netip.Addr
	if addr, ok := parseClientAddr(r); ok {
		ipPtr = &addr
	}
	if _, err := h.queries.CreateLoginActivity(ctx, db.CreateLoginActivityParams{
		UserID:               user.ID,
		AuthenticationMethod: pgtype.Text{String: authenticationMethodPassword, Valid: true},
		Success:              pgtype.Bool{Bool: false, Valid: true},
		FailureReason:        pgtype.Text{String: reason, Valid: reason != ""},
		Ip:                   ipPtr,
		UserAgent:            requestUserAgent(r),
	}); err != nil {
		slog.Error("Failed to record login activity", "error", err)
	}
}

// suspiciousSignIn reports whether the sign-in should trigger a notification:
// the user has signed in before, has no OTP protection, and the IP does not
// fall within a previously seen masked network.
func (h *Handler) suspiciousSignIn(ctx context.Context, user db.User, addr netip.Addr) bool {
	if user.OtpRequiredForLogin || !user.CurrentSignInAt.Valid {
		return false
	}
	count, err := h.queries.CountSuccessfulLoginActivitiesByMaskedIP(ctx, db.CountSuccessfulLoginActivitiesByMaskedIPParams{
		UserID:   user.ID,
		MaskedIp: maskedNetwork(addr),
	})
	if err != nil {
		slog.Error("Failed to check login activity for IP", "error", err)
		return false
	}
	return count == 0
}

// maskedNetwork widens an address to its tolerance network in CIDR notation.
func maskedNetwork(addr netip.Addr) string {
	bits := ipv4ToleranceMask
	if addr.Is6() && !addr.Is4In6() {
		bits = ipv6ToleranceMask
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return fmt.Sprintf("%s/32", addr)
	}
	return prefix.String()
}

func parseClientAddr(r *http.Request) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(middleware.ClientIP(r))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func requestUserAgent(r *http.Request) pgtype.Text {
	agent := r.UserAgent()
	return pgtype.Text{String: agent, Valid: agent != ""}
}
