package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

// multipartFormPart is a part of a multipart/form-data test body. A part with
// a filename is a file part.
type multipartFormPart struct {
	name, value, filename string
}

// multipartFormRequest builds a request whose body is sent as curl -F does.
func multipartFormRequest(t *testing.T, method, target string, parts ...multipartFormPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, p := range parts {
		var (
			pw  io.Writer
			err error
		)
		if p.filename != "" {
			pw, err = w.CreateFormFile(p.name, p.filename)
		} else {
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q", p.name))
			pw, err = w.CreatePart(header)
		}
		if err != nil {
			t.Fatalf("create part %q: %v", p.name, err)
		}
		if _, err := io.WriteString(pw, p.value); err != nil {
			t.Fatalf("write part %q: %v", p.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(method, target, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// malformedMultipartRequest has a multipart Content-Type whose boundary
// never appears in the body.
func malformedMultipartRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader("--other\r\nContent-Disposition: form-data; name=\"id[]\"\r\n\r\n1\r\n--other--\r\n"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=expected")
	return req
}

func TestAppsHandler_Create_Multipart(t *testing.T) {
	tests := []struct {
		name            string
		req             func(t *testing.T) *http.Request
		wantCode        int
		wantError       string
		wantName        string
		wantRedirectURI string
		wantScopes      string
		wantWebsite     string
	}{
		{
			// The request of https://docs.joinmastodon.org/client/token/.
			name: "fields as in Mastodon's client guide",
			req: func(t *testing.T) *http.Request {
				return multipartFormRequest(t, http.MethodPost, "/api/v1/apps",
					multipartFormPart{name: "client_name", value: "Test Application"},
					multipartFormPart{name: "redirect_uris", value: "urn:ietf:wg:oauth:2.0:oob"},
					multipartFormPart{name: "scopes", value: "read write push"},
					multipartFormPart{name: "website", value: "https://myapp.example"},
				)
			},
			wantCode:        http.StatusOK,
			wantName:        "Test Application",
			wantRedirectURI: "urn:ietf:wg:oauth:2.0:oob",
			wantScopes:      "read write push",
			wantWebsite:     "https://myapp.example",
		},
		{
			name: "redirect_uris array",
			req: func(t *testing.T) *http.Request {
				return multipartFormRequest(t, http.MethodPost, "/api/v1/apps",
					multipartFormPart{name: "client_name", value: "Array App"},
					multipartFormPart{name: "redirect_uris[]", value: "https://example.com/callback"},
					multipartFormPart{name: "redirect_uris[]", value: "https://example.com/alt"},
				)
			},
			wantCode:        http.StatusOK,
			wantName:        "Array App",
			wantRedirectURI: "https://example.com/callback\nhttps://example.com/alt",
			wantScopes:      "read",
		},
		{
			name: "file part does not bind a field",
			req: func(t *testing.T) *http.Request {
				return multipartFormRequest(t, http.MethodPost, "/api/v1/apps",
					multipartFormPart{name: "client_name", value: "From a file", filename: "name.txt"},
					multipartFormPart{name: "redirect_uris", value: "urn:ietf:wg:oauth:2.0:oob"},
				)
			},
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "Validation failed: Name can't be blank",
		},
		{
			name: "malformed body",
			req: func(t *testing.T) *http.Request {
				return malformedMultipartRequest(http.MethodPost, "/api/v1/apps")
			},
			wantCode:  http.StatusBadRequest,
			wantError: "Invalid request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubAppsQueries{app: db.OauthApplication{
				ID:     7,
				Name:   tt.wantName,
				Uid:    "generated-client-id",
				Secret: "generated-client-secret",
				Scopes: tt.wantScopes,
			}}
			h := NewAppsHandler(stub, &config.Config{})
			rec := httptest.NewRecorder()

			h.Create(rec, tt.req(t))

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantError != "" {
				var resp map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if resp["error"] != tt.wantError {
					t.Fatalf("error = %q, want %q", resp["error"], tt.wantError)
				}
				if stub.createArg != (db.CreateOAuthApplicationParams{}) {
					t.Fatalf("application created: %+v", stub.createArg)
				}
				return
			}
			arg := stub.createArg
			if arg.Name != tt.wantName || arg.RedirectUri != tt.wantRedirectURI || arg.Scopes != tt.wantScopes {
				t.Fatalf("created name=%q redirect_uri=%q scopes=%q, want %q %q %q",
					arg.Name, arg.RedirectUri, arg.Scopes, tt.wantName, tt.wantRedirectURI, tt.wantScopes)
			}
			if arg.Website.String != tt.wantWebsite || arg.Website.Valid != (tt.wantWebsite != "") {
				t.Fatalf("created website = %+v, want %q", arg.Website, tt.wantWebsite)
			}
		})
	}
}

var errQueryNotStubbed = errors.New("query not stubbed")

// execRecordingDB is a db.DBTX that records Exec calls and lets them succeed.
// Queries that return rows fail.
type execRecordingDB struct {
	execs []recordedExec
}

type recordedExec struct {
	sql  string
	args []any
}

func (d *execRecordingDB) Exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	d.execs = append(d.execs, recordedExec{sql: sql, args: args})
	return pgconn.CommandTag{}, nil
}

func (d *execRecordingDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errQueryNotStubbed
}

func (d *execRecordingDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return failingQueryRow{}
}

type failingQueryRow struct{}

func (failingQueryRow) Scan(...any) error { return errQueryNotStubbed }

func TestNotificationsHandler_BulkRequests_Multipart(t *testing.T) {
	const accountID = int64(12345)
	tests := []struct {
		name     string
		dismiss  bool
		req      func(t *testing.T, target string) *http.Request
		wantCode int
		// wantIDs are the request IDs passed to the bulk query; nil means
		// no query runs.
		wantIDs []int64
	}{
		{
			name: "accept id[]",
			req: func(t *testing.T, target string) *http.Request {
				return multipartFormRequest(t, http.MethodPost, target,
					multipartFormPart{name: "id[]", value: "1"},
					multipartFormPart{name: "id[]", value: "2"},
				)
			},
			wantCode: http.StatusOK,
			wantIDs:  []int64{1, 2},
		},
		{
			name:    "dismiss id[]",
			dismiss: true,
			req: func(t *testing.T, target string) *http.Request {
				return multipartFormRequest(t, http.MethodPost, target,
					multipartFormPart{name: "id[]", value: "3"},
					multipartFormPart{name: "id[]", value: "4"},
				)
			},
			wantCode: http.StatusOK,
			wantIDs:  []int64{3, 4},
		},
		{
			name: "accept a single id",
			req: func(t *testing.T, target string) *http.Request {
				return multipartFormRequest(t, http.MethodPost, target, multipartFormPart{name: "id", value: "5"})
			},
			wantCode: http.StatusOK,
			wantIDs:  []int64{5},
		},
		{
			name:    "dismiss skips duplicate and invalid ids",
			dismiss: true,
			req: func(t *testing.T, target string) *http.Request {
				return multipartFormRequest(t, http.MethodPost, target,
					multipartFormPart{name: "id[]", value: "6"},
					multipartFormPart{name: "id[]", value: "6"},
					multipartFormPart{name: "id[]", value: "x"},
					multipartFormPart{name: "id[]", value: "7", filename: "7.txt"},
				)
			},
			wantCode: http.StatusOK,
			wantIDs:  []int64{6},
		},
		{
			name: "accept without ids",
			req: func(t *testing.T, target string) *http.Request {
				return multipartFormRequest(t, http.MethodPost, target)
			},
			wantCode: http.StatusOK,
		},
		{
			name: "accept malformed body",
			req: func(t *testing.T, target string) *http.Request {
				return malformedMultipartRequest(http.MethodPost, target)
			},
			wantCode: http.StatusBadRequest,
		},
		{
			name:    "dismiss malformed body",
			dismiss: true,
			req: func(t *testing.T, target string) *http.Request {
				return malformedMultipartRequest(http.MethodPost, target)
			},
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &execRecordingDB{}
			h := NewNotificationsHandler(db.New(store), nil, nil, nil)
			handle, target, query := h.AcceptRequests, "/api/v1/notifications/requests/accept", "BulkAcceptNotificationRequests"
			if tt.dismiss {
				handle, target, query = h.DismissRequests, "/api/v1/notifications/requests/dismiss", "BulkDismissNotificationRequests"
			}
			req := tt.req(t, target)
			req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
			rec := httptest.NewRecorder()

			handle(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantIDs == nil {
				if len(store.execs) != 0 {
					t.Fatalf("queries = %d, want none", len(store.execs))
				}
				return
			}
			if len(store.execs) != 1 {
				t.Fatalf("queries = %d, want 1", len(store.execs))
			}
			exec := store.execs[0]
			if !strings.Contains(exec.sql, query) {
				t.Fatalf("query = %q, want %s", exec.sql, query)
			}
			want := []any{accountID, tt.wantIDs}
			if !reflect.DeepEqual(exec.args, want) {
				t.Fatalf("query args = %#v, want %#v", exec.args, want)
			}
		})
	}
}
