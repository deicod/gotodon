package web

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Bind decodes the request body into v.
func Bind(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Fallback logging or ignore if write failed
		_ = err
	}
}

// JSONError writes a standardized error JSON response.
func JSONError(w http.ResponseWriter, status int, message string) {
	JSON(w, status, map[string]string{"error": message})
}

// Param returns a path value from the request (Go 1.22+).
func Param(r *http.Request, key string) string {
	return r.PathValue(key)
}

// QueryParam returns a query parameter value.
func QueryParam(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

// Int64Param parses an int64 path parameter.
func Int64Param(r *http.Request, key string) (int64, error) {
	val := r.PathValue(key)
	return strconv.ParseInt(val, 10, 64)
}

// ParsePagination returns limit and offset from query parameters.
func ParsePagination(r *http.Request) (int, int) {
	limitStr := QueryParam(r, "limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit < 0 {
		limit = 0
	}

	offsetStr := QueryParam(r, "offset")
	offset, _ := strconv.Atoi(offsetStr)
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
