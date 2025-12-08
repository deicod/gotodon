// Package admin provides Mastodon-compatible Admin API handlers.
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// AccountsHandler handles admin account endpoints
type AccountsHandler struct {
	idGen *snowflake.Generator
	// In-memory storage for demonstration
	accounts map[int64]*AdminAccount
	actions  []AccountAction
}

// NewAccountsHandler creates a new admin accounts handler
func NewAccountsHandler() *AccountsHandler {
	return &AccountsHandler{
		idGen:    snowflake.NewGenerator(),
		accounts: make(map[int64]*AdminAccount),
		actions:  make([]AccountAction, 0),
	}
}

// AdminAccount represents an account in the admin view
type AdminAccount struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Domain         *string   `json:"domain"`
	CreatedAt      time.Time `json:"created_at"`
	Email          string    `json:"email,omitempty"`
	IP             *string   `json:"ip,omitempty"`
	Role           *Role     `json:"role,omitempty"`
	Confirmed      bool      `json:"confirmed"`
	Approved       bool      `json:"approved"`
	Disabled       bool      `json:"disabled"`
	Silenced       bool      `json:"silenced"`
	Suspended      bool      `json:"suspended"`
	Sensitized     bool      `json:"sensitized"`
	InviteRequest  *string   `json:"invite_request,omitempty"`
	CreatedByAppID *string   `json:"created_by_application_id,omitempty"`
	InvitedByID    *string   `json:"invited_by_account_id,omitempty"`
}

// Role represents an admin role
type Role struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Position    int    `json:"position"`
	Permissions int64  `json:"permissions"`
	Highlighted bool   `json:"highlighted"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// AccountAction represents an action taken on an account
type AccountAction struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	TargetID  string    `json:"target_account_id"`
	Action    string    `json:"action"`
	Text      string    `json:"text,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	SendEmail bool      `json:"send_email_notification"`
}

// GetAll handles GET /api/v1/admin/accounts
func (h *AccountsHandler) GetAll(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	// Parse query parameters
	limit := 100
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	_ = limit

	// Return mock accounts
	accounts := make([]AdminAccount, 0)
	for _, acc := range h.accounts {
		accounts = append(accounts, *acc)
	}

	return c.JSON(http.StatusOK, accounts)
}

// Get handles GET /api/v1/admin/accounts/:id
func (h *AccountsHandler) Get(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account, exists := h.accounts[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	return c.JSON(http.StatusOK, account)
}

// TakeAction handles POST /api/v1/admin/accounts/:id/action
func (h *AccountsHandler) TakeAction(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	adminID, _ := middleware.GetAccountID(c)

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account, exists := h.accounts[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	var req struct {
		Type                  string `json:"type" form:"type"`
		Text                  string `json:"text" form:"text"`
		ReportID              string `json:"report_id" form:"report_id"`
		WarningPresetID       string `json:"warning_preset_id" form:"warning_preset_id"`
		SendEmailNotification bool   `json:"send_email_notification" form:"send_email_notification"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	// Validate action type
	validActions := map[string]bool{
		"none": true, "disable": true, "sensitive": true,
		"silence": true, "suspend": true,
	}
	if !validActions[req.Type] {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Invalid action type",
		})
	}

	// Apply action
	switch req.Type {
	case "disable":
		account.Disabled = true
	case "sensitive":
		account.Sensitized = true
	case "silence":
		account.Silenced = true
	case "suspend":
		account.Suspended = true
	}

	// Record action
	action := AccountAction{
		ID:        strconv.FormatInt(h.idGen.Generate(), 10),
		AccountID: strconv.FormatInt(adminID, 10),
		TargetID:  c.Param("id"),
		Action:    req.Type,
		Text:      req.Text,
		CreatedAt: time.Now(),
		SendEmail: req.SendEmailNotification,
	}
	h.actions = append(h.actions, action)

	return c.JSON(http.StatusOK, map[string]any{})
}

// Approve handles POST /api/v1/admin/accounts/:id/approve
func (h *AccountsHandler) Approve(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account, exists := h.accounts[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account.Approved = true

	return c.JSON(http.StatusOK, account)
}

// Reject handles POST /api/v1/admin/accounts/:id/reject
func (h *AccountsHandler) Reject(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	if _, exists := h.accounts[accountID]; !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	// In production: delete the account
	delete(h.accounts, accountID)

	return c.JSON(http.StatusOK, map[string]any{})
}

// Enable handles POST /api/v1/admin/accounts/:id/enable
func (h *AccountsHandler) Enable(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account, exists := h.accounts[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account.Disabled = false

	return c.JSON(http.StatusOK, account)
}

// Unsilence handles POST /api/v1/admin/accounts/:id/unsilence
func (h *AccountsHandler) Unsilence(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account, exists := h.accounts[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account.Silenced = false

	return c.JSON(http.StatusOK, account)
}

// Unsuspend handles POST /api/v1/admin/accounts/:id/unsuspend
func (h *AccountsHandler) Unsuspend(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account, exists := h.accounts[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account.Suspended = false

	return c.JSON(http.StatusOK, account)
}

// Unsensitize handles POST /api/v1/admin/accounts/:id/unsensitize
func (h *AccountsHandler) Unsensitize(c echo.Context) error {
	if err := h.requireAdmin(c); err != nil {
		return err
	}

	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account, exists := h.accounts[accountID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	account.Sensitized = false

	return c.JSON(http.StatusOK, account)
}

func (h *AccountsHandler) requireAdmin(c echo.Context) error {
	role, ok := c.Get("role").(string)
	if !ok || (role != "admin" && role != "moderator") {
		return echo.NewHTTPError(http.StatusForbidden, map[string]string{
			"error": "This action is not allowed",
		})
	}
	return nil
}

// RegisterRoutes registers admin account routes
func (h *AccountsHandler) RegisterRoutes(g *echo.Group) {
	accounts := g.Group("/accounts")
	accounts.GET("", h.GetAll)
	accounts.GET("/:id", h.Get)
	accounts.POST("/:id/action", h.TakeAction)
	accounts.POST("/:id/approve", h.Approve)
	accounts.POST("/:id/reject", h.Reject)
	accounts.POST("/:id/enable", h.Enable)
	accounts.POST("/:id/unsilence", h.Unsilence)
	accounts.POST("/:id/unsuspend", h.Unsuspend)
	accounts.POST("/:id/unsensitize", h.Unsensitize)
}
