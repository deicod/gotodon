package admin

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// requireAdmin checks if the user has admin or moderator role
func requireAdmin(c echo.Context) error {
	role, ok := c.Get("role").(string)
	if !ok || (role != "admin" && role != "moderator") {
		return echo.NewHTTPError(http.StatusForbidden, map[string]string{
			"error": "This action is not allowed",
		})
	}
	return nil
}
