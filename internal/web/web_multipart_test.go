package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)

// formPair is one field of a form body. Pairs keep their order.
type formPair struct {
	key, value string
}

func urlencodedRequest(t *testing.T, pairs []formPair) *http.Request {
	t.Helper()
	values := url.Values{}
	for _, p := range pairs {
		values.Add(p.key, p.value)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// multipartRequest sends pairs as curl -F and browsers' FormData do.
func multipartRequest(t *testing.T, pairs []formPair) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, p := range pairs {
		if err := w.WriteField(p.key, p.value); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

type formFieldsPayload struct {
	ClientName string   `json:"client_name" form:"client_name"`
	Count      int      `form:"count"`
	Enabled    bool     `form:"enabled"`
	Note       *string  `form:"note"`
	IDs        []string `json:"id"`
	Choices    []int    `json:"choices" form:"choices[]"`
}

type formPoll struct {
	Options   []string `form:"options[]"`
	ExpiresIn int      `form:"expires_in"`
}

type formStatusPayload struct {
	Status string    `form:"status"`
	Poll   *formPoll `form:"poll"`
}

// formPushPayload has the shape of the POST /api/v1/push/subscription
// request: value structs and a map of bools.
type formPushPayload struct {
	Subscription struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256DH string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	} `json:"subscription"`
	Data struct {
		Alerts map[string]bool `json:"alerts"`
	} `json:"data"`
}

// formMarkersPayload has the shape of the POST /api/v1/markers request: a
// map of structs.
type formMarkersPayload map[string]struct {
	LastReadID string `json:"last_read_id" form:"last_read_id"`
}

// TestBindFormEncodings binds the same fields sent urlencoded and as
// multipart/form-data, which must map onto the target alike.
func TestBindFormEncodings(t *testing.T) {
	note := "hello"
	push := &formPushPayload{}
	push.Subscription.Endpoint = "https://push.example/send/1"
	push.Subscription.Keys.P256DH = "p256dh-key"
	push.Subscription.Keys.Auth = "auth-secret"
	push.Data.Alerts = map[string]bool{"follow": true, "mention": false}

	tests := []struct {
		name   string
		pairs  []formPair
		target func() any
		want   any
	}{
		{
			name:   "fields",
			pairs:  []formPair{{"client_name", "smoke"}, {"count", "3"}, {"enabled", "on"}, {"note", "hello"}},
			target: func() any { return &formFieldsPayload{} },
			want:   &formFieldsPayload{ClientName: "smoke", Count: 3, Enabled: true, Note: &note},
		},
		{
			name:   "array keys",
			pairs:  []formPair{{"id[]", "1"}, {"id[]", "2"}, {"choices[]", "0"}, {"choices[]", "2"}},
			target: func() any { return &formFieldsPayload{} },
			want:   &formFieldsPayload{IDs: []string{"1", "2"}, Choices: []int{0, 2}},
		},
		{
			name:   "single value for a slice",
			pairs:  []formPair{{"id", "7"}},
			target: func() any { return &formFieldsPayload{} },
			want:   &formFieldsPayload{IDs: []string{"7"}},
		},
		{
			name:   "nested keys in a pointer struct",
			pairs:  []formPair{{"status", "Vote"}, {"poll[options][]", "yes"}, {"poll[options][]", "no"}, {"poll[expires_in]", "300"}},
			target: func() any { return &formStatusPayload{} },
			want:   &formStatusPayload{Status: "Vote", Poll: &formPoll{Options: []string{"yes", "no"}, ExpiresIn: 300}},
		},
		{
			name: "nested keys in value structs and a map",
			pairs: []formPair{
				{"subscription[endpoint]", "https://push.example/send/1"},
				{"subscription[keys][p256dh]", "p256dh-key"},
				{"subscription[keys][auth]", "auth-secret"},
				{"data[alerts][follow]", "true"},
				{"data[alerts][mention]", "0"},
			},
			target: func() any { return &formPushPayload{} },
			want:   push,
		},
		{
			name:   "map of structs",
			pairs:  []formPair{{"bearer_token", "token"}, {"home[last_read_id]", "100"}, {"notifications[last_read_id]", "200"}},
			target: func() any { return &formMarkersPayload{} },
			want:   &formMarkersPayload{"home": {LastReadID: "100"}, "notifications": {LastReadID: "200"}},
		},
	}
	encodings := []struct {
		name    string
		request func(*testing.T, []formPair) *http.Request
	}{
		{name: "urlencoded", request: urlencodedRequest},
		{name: "multipart", request: multipartRequest},
	}

	for _, tt := range tests {
		for _, enc := range encodings {
			t.Run(tt.name+"/"+enc.name, func(t *testing.T) {
				got := tt.target()
				if err := Bind(enc.request(t, tt.pairs), got); err != nil {
					t.Fatalf("Bind error: %v", err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("Bind = %#v, want %#v", got, tt.want)
				}
			})
		}
	}
}

// multipartPart is one part of a hand-built multipart body.
type multipartPart struct {
	disposition string
	content     string
}

func formPart(name, value string) multipartPart {
	return multipartPart{disposition: fmt.Sprintf(`form-data; name=%q`, name), content: value}
}

func filePart(name, filename, content string) multipartPart {
	return multipartPart{disposition: fmt.Sprintf(`form-data; name=%q; filename=%q`, name, filename), content: content}
}

// multipartBody writes parts with boundary and returns the body.
func multipartBody(t *testing.T, boundary string, parts ...multipartPart) string {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.SetBoundary(boundary); err != nil {
		t.Fatalf("set boundary: %v", err)
	}
	for _, p := range parts {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", p.disposition)
		pw, err := w.CreatePart(header)
		if err != nil {
			t.Fatalf("create part: %v", err)
		}
		if _, err := io.WriteString(pw, p.content); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return body.String()
}

func TestBindMultipartParts(t *testing.T) {
	webKitBoundary := "----WebKitFormBoundaryAbCdEfGhIjKlMnOp"
	tests := []struct {
		name        string
		contentType string
		body        string
		want        formFieldsPayload
	}{
		{
			name:        "browser boundary keeps its case",
			contentType: "Multipart/Form-Data; boundary=" + webKitBoundary,
			body:        multipartBody(t, webKitBoundary, formPart("client_name", "web")),
			want:        formFieldsPayload{ClientName: "web"},
		},
		{
			name:        "quoted boundary",
			contentType: `multipart/form-data; boundary="quoted boundary"`,
			body:        multipartBody(t, "quoted boundary", formPart("client_name", "quoted")),
			want:        formFieldsPayload{ClientName: "quoted"},
		},
		{
			name:        "file parts are skipped",
			contentType: "multipart/form-data; boundary=files",
			body: multipartBody(t, "files",
				filePart("client_name", "name.txt", "from a file"),
				formPart("count", "2"),
				// An empty file input: Rack drops it as well.
				filePart("note", "", ""),
				multipartPart{disposition: `form-data; name="id[]"; filename*=UTF-8''%C3%A9.png`, content: "\x89PNG"},
				formPart("id[]", "5"),
			),
			want: formFieldsPayload{Count: 2, IDs: []string{"5"}},
		},
		{
			name:        "parts without a form name are skipped",
			contentType: "multipart/form-data; boundary=unnamed",
			body: multipartBody(t, "unnamed",
				multipartPart{disposition: "form-data", content: "no name"},
				multipartPart{disposition: `attachment; name="client_name"`, content: "not form-data"},
				formPart("count", "4"),
			),
			want: formFieldsPayload{Count: 4},
		},
		{
			name:        "parts up to the limit",
			contentType: "multipart/form-data; boundary=many",
			body:        multipartBody(t, "many", repeatedParts(maxMultipartParts)...),
			want:        formFieldsPayload{IDs: repeatedIDs(maxMultipartParts)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)

			var got formFieldsPayload
			if err := Bind(req, &got); err != nil {
				t.Fatalf("Bind error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Bind = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func repeatedParts(n int) []multipartPart {
	parts := make([]multipartPart, n)
	for i := range parts {
		parts[i] = formPart("id[]", "1")
	}
	return parts
}

func repeatedIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "1"
	}
	return ids
}

func TestBindMultipartInvalidOrEmpty(t *testing.T) {
	valid := multipartBody(t, "b", formPart("client_name", "x"))
	// multipart.Writer refuses boundaries this long, so this body is built
	// by hand.
	long := strings.Repeat("b", maxMultipartBoundaryLength+1)
	tests := []struct {
		name        string
		contentType string
		body        string
		// wantEOF expects io.EOF, the error for a body without fields;
		// otherwise Bind must fail with errInvalidMultipart.
		wantEOF bool
	}{
		{name: "empty body", contentType: "multipart/form-data; boundary=b", body: "", wantEOF: true},
		{name: "closing boundary only", contentType: "multipart/form-data; boundary=b", body: "--b--\r\n", wantEOF: true},
		{name: "only file parts", contentType: "multipart/form-data; boundary=b", body: multipartBody(t, "b", filePart("client_name", "a.txt", "x")), wantEOF: true},
		{name: "missing boundary", contentType: "multipart/form-data", body: valid},
		{name: "empty boundary", contentType: `multipart/form-data; boundary=""`, body: valid},
		{name: "malformed content type", contentType: "multipart/form-data; boundary", body: valid},
		{name: "boundary longer than 70 characters", contentType: "multipart/form-data; boundary=" + long, body: "--" + long + "\r\nContent-Disposition: form-data; name=\"client_name\"\r\n\r\nx\r\n--" + long + "--\r\n"},
		{name: "boundary not in the body", contentType: "multipart/form-data; boundary=other", body: valid},
		{name: "body ends before the closing boundary", contentType: "multipart/form-data; boundary=b", body: strings.TrimSuffix(valid, "--b--\r\n")},
		{name: "part cut short", contentType: "multipart/form-data; boundary=b", body: "--b\r\nContent-Disposition: form-data; name=\"client_name\"\r\n\r\nx"},
		{name: "more parts than the limit", contentType: "multipart/form-data; boundary=b", body: multipartBody(t, "b", repeatedParts(maxMultipartParts+1)...)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)

			var got formFieldsPayload
			err := Bind(req, &got)
			if tt.wantEOF {
				if err != io.EOF {
					t.Fatalf("Bind error = %v, want io.EOF", err)
				}
				return
			}
			if !errors.Is(err, errInvalidMultipart) {
				t.Fatalf("Bind error = %v, want errInvalidMultipart", err)
			}
			// Callers such as parseNotificationRequestIDs read io.EOF as
			// "no fields" and must not mistake a broken body for one.
			if errors.Is(err, io.EOF) {
				t.Fatalf("Bind error = %v matches io.EOF", err)
			}
		})
	}
}

// repeatByte is an endless stream of one byte.
type repeatByte byte

func (b repeatByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func TestBindMultipartBodyLimit(t *testing.T) {
	// Nothing Bind reads may land in a temporary file.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	const boundary = "limit"
	tests := []struct {
		name string
		// size is the length of the whole body. A filler part pads the
		// body to it after a client_name field.
		size       int64
		fileFiller bool
		// declared sends size as Content-Length; otherwise the length is
		// unknown, as for a chunked body.
		declared bool
		wantErr  bool
		// maxRead is the most bytes Bind may read from the body.
		maxRead int64
	}{
		{name: "body at the limit binds", size: maxMultipartBodyBytes, fileFiller: true, declared: true, maxRead: maxMultipartBodyBytes},
		{name: "declared length past the limit is not read", size: maxMultipartBodyBytes + 1, fileFiller: true, declared: true, wantErr: true, maxRead: 0},
		{name: "file part past the limit", size: maxMultipartBodyBytes + 1, fileFiller: true, wantErr: true, maxRead: maxMultipartBodyBytes + 1},
		{name: "field past the limit", size: maxMultipartBodyBytes + 1, wantErr: true, maxRead: maxMultipartBodyBytes + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			head := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"client_name\"\r\n\r\nlimit\r\n--" + boundary + "\r\n"
			if tt.fileFiller {
				head += "Content-Disposition: form-data; name=\"upload\"; filename=\"filler.bin\"\r\nContent-Type: application/octet-stream\r\n\r\n"
			} else {
				head += "Content-Disposition: form-data; name=\"note\"\r\n\r\n"
			}
			tail := "\r\n--" + boundary + "--\r\n"
			filler := tt.size - int64(len(head)) - int64(len(tail))
			body := &countingReader{r: io.MultiReader(
				strings.NewReader(head),
				io.LimitReader(repeatByte('a'), filler),
				strings.NewReader(tail),
			)}

			req := httptest.NewRequest(http.MethodPost, "/", body)
			req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			req.ContentLength = -1
			if tt.declared {
				req.ContentLength = tt.size
			}

			var got formFieldsPayload
			err := Bind(req, &got)
			if tt.wantErr {
				var tooLarge *http.MaxBytesError
				if !errors.As(err, &tooLarge) || tooLarge.Limit != maxMultipartBodyBytes {
					t.Fatalf("Bind error = %v, want *http.MaxBytesError with limit %d", err, maxMultipartBodyBytes)
				}
			} else {
				if err != nil {
					t.Fatalf("Bind error: %v", err)
				}
				if got.ClientName != "limit" {
					t.Fatalf("client_name = %q, want %q", got.ClientName, "limit")
				}
			}
			if body.n > tt.maxRead {
				t.Fatalf("read %d bytes, want at most %d", body.n, tt.maxRead)
			}
			entries, err := os.ReadDir(tmp)
			if err != nil {
				t.Fatalf("read temp dir: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("temp dir holds %d entries, want none", len(entries))
			}
		})
	}
}

// TestBindMultipartFillsForm checks that r.PostForm and r.Form hold a
// multipart body's fields after Bind, as they do for urlencoded bodies.
func TestBindMultipartFillsForm(t *testing.T) {
	req := multipartRequest(t, []formPair{{"client_name", "form"}, {"id[]", "1"}})
	req.URL.RawQuery = "scope=read"

	var got formFieldsPayload
	if err := Bind(req, &got); err != nil {
		t.Fatalf("Bind error: %v", err)
	}
	if v := req.PostForm.Get("client_name"); v != "form" {
		t.Fatalf("PostForm client_name = %q, want %q", v, "form")
	}
	if v := req.PostForm["id[]"]; !reflect.DeepEqual(v, []string{"1"}) {
		t.Fatalf("PostForm id[] = %q, want [1]", v)
	}
	if v := req.FormValue("client_name"); v != "form" {
		t.Fatalf("FormValue client_name = %q, want %q", v, "form")
	}
	if v := req.FormValue("scope"); v != "read" {
		t.Fatalf("FormValue scope = %q, want %q", v, "read")
	}
	if req.PostForm.Has("scope") {
		t.Fatal("PostForm holds the query parameter scope")
	}
}
