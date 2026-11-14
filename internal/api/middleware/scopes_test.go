package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHasAnyScope(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		anyOf  []string
		expect bool
	}{
		{"empty token scopes", "", []string{"read"}, false},
		{"whitespace token scopes", " \t ", []string{"read"}, false},
		{"empty token scopes for a child", "", []string{"read", "read:statuses"}, false},
		{"exact match", "read", []string{"read"}, true},
		{"any of the scopes", "follow", []string{"follow", "write", "write:follows"}, true},
		{"the last of the scopes", "write:follows", []string{"follow", "write", "write:follows"}, true},
		{"none of the scopes", "read write:blocks", []string{"follow", "write", "write:follows"}, false},
		{"parent covers child", "read", []string{"read:statuses"}, true},
		{"child doesn't cover parent", "read:statuses", []string{"read"}, false},
		{"child doesn't cover sibling", "read:accounts", []string{"read:statuses"}, false},
		{"read doesn't cover write", "read", []string{"write", "write:statuses"}, false},
		{"write doesn't cover push", "write", []string{"push"}, false},
		{"read doesn't cover profile", "read:accounts", []string{"profile"}, false},
		{"one of several token scopes", "push read:lists", []string{"read", "read:lists"}, true},
		{"admin parent covers admin child", "admin:read", []string{"admin:read:accounts"}, true},
		{"user read doesn't cover admin read", "read", []string{"admin:read"}, false},
		{"no required scopes", "read", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAnyScope(tt.token, tt.anyOf...); got != tt.expect {
				t.Errorf("HasAnyScope(%q, %q) = %t, want %t", tt.token, tt.anyOf, got, tt.expect)
			}
		})
	}
}

func TestHasToken(t *testing.T) {
	keys := []ContextKey{ContextKeyTokenID, ContextKeyAppID, ContextKeyUserID, ContextKeyAccountID}
	for _, key := range keys {
		ctx := context.WithValue(context.Background(), key, int64(1))
		if !HasToken(ctx) {
			t.Errorf("HasToken with %s = false, want true", key)
		}
	}
	if !HasToken(context.WithValue(context.Background(), ContextKeyScopes, "")) {
		t.Error("HasToken with empty scopes = false, want true")
	}
	if HasToken(context.Background()) {
		t.Error("HasToken without values = true, want false")
	}
	if HasToken(context.WithValue(context.Background(), ContextKeyClientIP, "192.0.2.1")) {
		t.Error("HasToken with only a client IP = true, want false")
	}
}

// scopeRequest describes the context a request reaches a scope wrapper with.
type scopeRequest struct {
	tokenID   bool
	accountID bool
	scopes    *string
}

func strPtr(s string) *string { return &s }

func (sr scopeRequest) build() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/statuses", nil)
	ctx := req.Context()
	if sr.tokenID {
		ctx = context.WithValue(ctx, ContextKeyTokenID, int64(7))
		ctx = context.WithValue(ctx, ContextKeyAppID, int64(8))
	}
	if sr.accountID {
		ctx = context.WithValue(ctx, ContextKeyUserID, int64(9))
		ctx = context.WithValue(ctx, ContextKeyAccountID, int64(10))
	}
	if sr.scopes != nil {
		ctx = context.WithValue(ctx, ContextKeyScopes, *sr.scopes)
	}
	return req.WithContext(ctx)
}

func TestRequireScopeWrappers(t *testing.T) {
	user := func(scopes string) scopeRequest {
		return scopeRequest{tokenID: true, accountID: true, scopes: strPtr(scopes)}
	}
	app := func(scopes string) scopeRequest {
		return scopeRequest{tokenID: true, scopes: strPtr(scopes)}
	}
	tests := []struct {
		name string
		req  scopeRequest
		// Expected status from RequireScope and RequireScopeIfToken; 0
		// means the handler ran.
		required int
		ifToken  int
	}{
		{"no token", scopeRequest{}, http.StatusUnauthorized, 0},
		{"user token with write", user("write"), 0, 0},
		{"user token with write:statuses", user("write:statuses"), 0, 0},
		{"user token with read", user("read"), http.StatusForbidden, http.StatusForbidden},
		{"user token with an empty scope string", user(""), http.StatusForbidden, http.StatusForbidden},
		{"user token with scopes left out", scopeRequest{tokenID: true, accountID: true}, http.StatusForbidden, http.StatusForbidden},
		{"identity without a token ID", scopeRequest{accountID: true, scopes: strPtr("write")}, 0, 0},
		{"identity without a token ID or scopes", scopeRequest{accountID: true}, http.StatusForbidden, http.StatusForbidden},
		{"application token with write", app("write"), 0, 0},
		{"application token with read", app("read"), http.StatusForbidden, http.StatusForbidden},
		{"application token without scopes", app(""), http.StatusForbidden, http.StatusForbidden},
	}

	run := func(wrap func(http.HandlerFunc) http.HandlerFunc, req *http.Request) (int, string, bool) {
		ran := false
		handler := wrap(func(w http.ResponseWriter, r *http.Request) {
			ran = true
			w.WriteHeader(http.StatusNoContent)
		})
		rec := httptest.NewRecorder()
		handler(rec, req)
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body.Error, ran
	}
	check := func(t *testing.T, wrapper string, want, code int, message string, ran bool) {
		t.Helper()
		if want == 0 {
			if !ran || code != http.StatusNoContent {
				t.Errorf("%s: status = %d (%q), want the handler to run", wrapper, code, message)
			}
			return
		}
		if ran {
			t.Errorf("%s: the handler ran, want %d", wrapper, want)
		}
		wantMessage := OutsideScopesMessage
		if want == http.StatusUnauthorized {
			wantMessage = InvalidTokenMessage
		}
		if code != want || message != wantMessage {
			t.Errorf("%s: %d %q, want %d %q", wrapper, code, message, want, wantMessage)
		}
	}

	requireWrite := RequireScope("write", "write:statuses")
	writeIfToken := RequireScopeIfToken("write", "write:statuses")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, message, ran := run(requireWrite, tt.req.build())
			check(t, "RequireScope", tt.required, code, message, ran)
			code, message, ran = run(writeIfToken, tt.req.build())
			check(t, "RequireScopeIfToken", tt.ifToken, code, message, ran)
		})
	}
}

// TestRequireScopeAfterAuthenticate checks the wrappers behind the real
// authentication middleware, which stores the token's values.
func TestRequireScopeAfterAuthenticate(t *testing.T) {
	accountID, userID := int64(10), int64(9)
	tests := []struct {
		name   string
		header string
		info   *TokenInfo
		want   int
	}{
		{"no token", "", nil, http.StatusUnauthorized},
		{"token with follow", "Bearer t", &TokenInfo{TokenID: 1, AppID: 2, UserID: &userID, AccountID: &accountID, Scopes: "follow"}, http.StatusNoContent},
		{"token with read", "Bearer t", &TokenInfo{TokenID: 1, AppID: 2, UserID: &userID, AccountID: &accountID, Scopes: "read"}, http.StatusForbidden},
		{"token without scopes", "Bearer t", &TokenInfo{TokenID: 1, AppID: 2, UserID: &userID, AccountID: &accountID, Scopes: ""}, http.StatusForbidden},
		{"application token with write", "Bearer t", &TokenInfo{TokenID: 1, AppID: 2, Scopes: "write"}, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/v1/accounts/{id}/follow", RequireScope("follow", "write", "write:follows")(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			handler := Authenticate(stubTokenValidator{info: tt.info})(mux)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/1/follow", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

func TestRequireScopePanicsOnBadDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
	}{
		{"no scopes", nil},
		{"unknown scope", []string{"read", "read:everything"}},
		{"misspelled scope", []string{"wrte"}},
	}
	for _, tt := range tests {
		for wrapper, build := range map[string]func(...string) func(http.HandlerFunc) http.HandlerFunc{
			"RequireScope":        RequireScope,
			"RequireScopeIfToken": RequireScopeIfToken,
		} {
			t.Run(wrapper+" "+tt.name, func(t *testing.T) {
				defer func() {
					if recover() == nil {
						t.Errorf("%s(%q) didn't panic", wrapper, tt.scopes)
					}
				}()
				build(tt.scopes...)
			})
		}
	}
}

// TestRequireScopeCopiesScopes checks that changing the slice passed in
// doesn't change what a wrapper accepts.
func TestRequireScopeCopiesScopes(t *testing.T) {
	scopes := []string{"write", "write:statuses"}
	wrap := RequireScope(scopes...)
	scopes[0], scopes[1] = "read", "read:statuses"

	handler := wrap(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	handler(rec, scopeRequest{tokenID: true, accountID: true, scopes: strPtr("read")}.build())
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
