package admin

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/pkg/oauth"
)

// stubEmojiStore is a customEmojiStore backed by a map. Errors, when set,
// are returned instead of a result.
type stubEmojiStore struct {
	emojis     map[int64]db.CustomEmoji
	categories map[int64]string
	nextID     int64

	listErr     error
	categoryErr error
	takenErr    error
	createErr   error
	deleteErr   error
	// raceShortcode is reported as free by the check but refused by the
	// insert, as the unique index refuses a shortcode created in between.
	raceShortcode string

	listParams []db.AdminListCustomEmojisParams
	created    []db.AdminCreateCustomEmojiParams
	deleted    []int64
}

func newStubEmojiStore(emojis ...db.CustomEmoji) *stubEmojiStore {
	s := &stubEmojiStore{emojis: map[int64]db.CustomEmoji{}, categories: map[int64]string{}, nextID: 100}
	for _, emoji := range emojis {
		s.emojis[emoji.ID] = emoji
	}
	return s
}

func (s *stubEmojiStore) AdminListCustomEmojis(_ context.Context, arg db.AdminListCustomEmojisParams) ([]db.CustomEmoji, error) {
	s.listParams = append(s.listParams, arg)
	if s.listErr != nil {
		return nil, s.listErr
	}
	all := make([]db.CustomEmoji, 0, len(s.emojis))
	for _, emoji := range s.emojis {
		all = append(all, emoji)
	}
	slices.SortFunc(all, func(a, b db.CustomEmoji) int { return int(b.ID - a.ID) })
	start := min(int(arg.Offset), len(all))
	end := min(start+int(arg.Limit), len(all))
	return all[start:end], nil
}

func (s *stubEmojiStore) GetCustomEmojiCategoriesByIDs(_ context.Context, ids []int64) ([]db.GetCustomEmojiCategoriesByIDsRow, error) {
	if s.categoryErr != nil {
		return nil, s.categoryErr
	}
	var rows []db.GetCustomEmojiCategoriesByIDsRow
	for _, id := range ids {
		if name, ok := s.categories[id]; ok {
			rows = append(rows, db.GetCustomEmojiCategoriesByIDsRow{ID: id, Name: pgtype.Text{String: name, Valid: true}})
		}
	}
	return rows, nil
}

func (s *stubEmojiStore) LocalCustomEmojiShortcodeTaken(_ context.Context, shortcode string) (bool, error) {
	if s.takenErr != nil {
		return false, s.takenErr
	}
	for _, emoji := range s.emojis {
		if !emoji.Domain.Valid && emoji.Shortcode == shortcode {
			return true, nil
		}
	}
	return false, nil
}

func (s *stubEmojiStore) AdminCreateCustomEmoji(_ context.Context, arg db.AdminCreateCustomEmojiParams) (db.CustomEmoji, error) {
	if s.createErr != nil {
		return db.CustomEmoji{}, s.createErr
	}
	if arg.Shortcode == s.raceShortcode {
		return db.CustomEmoji{}, &pgconn.PgError{Code: "23505", ConstraintName: "index_custom_emojis_on_shortcode_local"}
	}
	s.created = append(s.created, arg)
	s.nextID++
	emoji := db.CustomEmoji{
		ID:               s.nextID,
		Shortcode:        arg.Shortcode,
		Domain:           arg.Domain,
		ImageFileName:    arg.ImageFileName,
		ImageContentType: arg.ImageContentType,
		ImageFileSize:    arg.ImageFileSize,
		CategoryID:       arg.CategoryID,
		VisibleInPicker:  arg.VisibleInPicker,
	}
	s.emojis[emoji.ID] = emoji
	return emoji, nil
}

func (s *stubEmojiStore) DeleteCustomEmoji(_ context.Context, id int64) (db.CustomEmoji, error) {
	if s.deleteErr != nil {
		return db.CustomEmoji{}, s.deleteErr
	}
	emoji, ok := s.emojis[id]
	if !ok {
		return db.CustomEmoji{}, pgx.ErrNoRows
	}
	delete(s.emojis, id)
	s.deleted = append(s.deleted, id)
	return emoji, nil
}

// stubStorage is a media.Storage backed by a map. Like LocalStorage, it
// deletes missing files without an error.
type stubStorage struct {
	files     map[string][]byte
	saveErr   error
	deleteErr error

	deletes []string
	// deleteCtxErrs records each Delete's context error.
	deleteCtxErrs []error
}

func newStubStorage() *stubStorage {
	return &stubStorage{files: map[string][]byte{}}
}

func (s *stubStorage) Save(_ context.Context, path string, data []byte) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.files[path] = bytes.Clone(data)
	return nil
}

func (s *stubStorage) Load(_ context.Context, path string) ([]byte, error) {
	data, ok := s.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (s *stubStorage) Delete(ctx context.Context, path string) error {
	s.deletes = append(s.deletes, path)
	s.deleteCtxErrs = append(s.deleteCtxErrs, ctx.Err())
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.files, path)
	return nil
}

func (s *stubStorage) URL(path string) string { return "https://example.com/files/" + path }

func (s *stubStorage) Exists(_ context.Context, path string) (bool, error) {
	_, ok := s.files[path]
	return ok, nil
}

var _ media.Storage = (*stubStorage)(nil)

// emojiFixture serves the custom emoji routes for a caller whose role and
// token allow every one of them.
type emojiFixture struct {
	store   *stubEmojiStore
	storage *stubStorage
	mux     *http.ServeMux
}

func newEmojiFixture(store *stubEmojiStore, storage *stubStorage) *emojiFixture {
	h := &CustomEmojisHandler{store: store, storage: storage, roles: rolesFor(adminPermissions)}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return &emojiFixture{store: store, storage: storage, mux: mux}
}

func (f *emojiFixture) serve(req *http.Request) *httptest.ResponseRecorder {
	req = withScopes(withIdentity(req, int64Ptr(10), int64Ptr(20)), adminToken)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// emojiPart is a part of a multipart body. A part with a filename is a file.
type emojiPart struct {
	name        string
	filename    string
	contentType string
	data        []byte
}

func shortcodePart(shortcode string) emojiPart {
	return emojiPart{name: "shortcode", data: []byte(shortcode)}
}

func imagePart(data []byte) emojiPart {
	return emojiPart{name: "image", filename: "emoji.png", contentType: "image/png", data: data}
}

func multipartBody(t *testing.T, parts ...emojiPart) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, part := range parts {
		header := textproto.MIMEHeader{}
		disposition := `form-data; name="` + part.name + `"`
		if part.filename != "" {
			disposition += `; filename="` + part.filename + `"`
		}
		header.Set("Content-Disposition", disposition)
		if part.contentType != "" {
			header.Set("Content-Type", part.contentType)
		}
		w, err := mw.CreatePart(header)
		if err != nil {
			t.Fatalf("create part %s: %v", part.name, err)
		}
		if _, err := w.Write(part.data); err != nil {
			t.Fatalf("write part %s: %v", part.name, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	return &body, mw.FormDataContentType()
}

func createRequest(t *testing.T, parts ...emojiPart) *http.Request {
	t.Helper()
	body, contentType := multipartBody(t, parts...)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", body)
	req.Header.Set("Content-Type", contentType)
	return req
}

// Image fixtures.

func pngImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// paddedPNG returns a valid PNG of exactly size bytes, padded after IEND.
func paddedPNG(t *testing.T, size int) []byte {
	t.Helper()
	data := pngImage(t)
	if len(data) > size {
		t.Fatalf("png is %d bytes, larger than %d", len(data), size)
	}
	return append(data, make([]byte, size-len(data))...)
}

// pngHeader returns the signature and IHDR chunk of an RGBA PNG, which is
// all image.DecodeConfig reads.
func pngHeader(width, height uint32) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], width)
	binary.BigEndian.PutUint32(ihdr[4:], height)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	buf.Write(chunk)
	_ = binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return buf.Bytes()
}

func gifImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

// gifHeader returns a GIF header and logical screen descriptor, which is
// all image.DecodeConfig reads.
func gifHeader(width, height uint16) []byte {
	header := []byte("GIF89a")
	header = binary.LittleEndian.AppendUint16(header, width)
	header = binary.LittleEndian.AppendUint16(header, height)
	return append(header, 0, 0, 0)
}

// webpVP8X returns a WebP file holding only a VP8X chunk, which is all
// image.DecodeConfig reads. animated sets the animation flag.
func webpVP8X(width, height uint32, animated bool) []byte {
	chunk := make([]byte, 10)
	if animated {
		chunk[0] = 1 << 1
	}
	putUint24 := func(b []byte, v uint32) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }
	putUint24(chunk[4:], width-1)
	putUint24(chunk[7:], height-1)

	data := []byte("RIFF")
	data = binary.LittleEndian.AppendUint32(data, uint32(4+8+len(chunk)))
	data = append(data, "WEBPVP8X"...)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(chunk)))
	return append(data, chunk...)
}

func jpegImage(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func localEmoji(id int64, shortcode, file string) db.CustomEmoji {
	return db.CustomEmoji{
		ID:              id,
		Shortcode:       shortcode,
		ImageFileName:   pgtype.Text{String: file, Valid: file != ""},
		VisibleInPicker: true,
	}
}

func remoteEmoji(id int64, shortcode, domain string) db.CustomEmoji {
	return db.CustomEmoji{
		ID:              id,
		Shortcode:       shortcode,
		Domain:          pgtype.Text{String: domain, Valid: true},
		ImageRemoteUrl:  pgtype.Text{String: "https://" + domain + "/emoji.png", Valid: true},
		VisibleInPicker: true,
	}
}

const storedBlobcat = "custom_emojis/images/original/0123456789abcdef.png"

var storedImagePath = regexp.MustCompile(`\Acustom_emojis/images/original/[0-9a-f]{16}\.(png|gif|webp)\z`)

func TestCustomEmojisCreate(t *testing.T) {
	tests := []struct {
		name            string
		parts           func(t *testing.T) []emojiPart
		wantShortcode   string
		wantContentType string
	}{
		{
			name:            "png",
			parts:           func(t *testing.T) []emojiPart { return []emojiPart{shortcodePart("blobfox"), imagePart(pngImage(t))} },
			wantShortcode:   "blobfox",
			wantContentType: "image/png",
		},
		{
			name:            "gif",
			parts:           func(t *testing.T) []emojiPart { return []emojiPart{shortcodePart("blob_fox"), imagePart(gifImage(t))} },
			wantShortcode:   "blob_fox",
			wantContentType: "image/gif",
		},
		{
			name: "animated webp",
			parts: func(*testing.T) []emojiPart {
				return []emojiPart{shortcodePart("BlobFox2"), imagePart(webpVP8X(128, 128, true))}
			},
			wantShortcode:   "BlobFox2",
			wantContentType: "image/webp",
		},
		{
			name: "type detected from the bytes, not the part's Content-Type",
			parts: func(t *testing.T) []emojiPart {
				return []emojiPart{shortcodePart("blobfox"), {name: "image", filename: "emoji.jpg", contentType: "image/jpeg", data: gifImage(t)}}
			},
			wantShortcode:   "blobfox",
			wantContentType: "image/gif",
		},
		{
			name: "image one byte below the limit",
			parts: func(t *testing.T) []emojiPart {
				return []emojiPart{shortcodePart("blobfox"), imagePart(paddedPNG(t, customEmojiImageLimit-1))}
			},
			wantShortcode:   "blobfox",
			wantContentType: "image/png",
		},
		{
			name: "GIF at the GIF pixel limit",
			parts: func(*testing.T) []emojiPart {
				return []emojiPart{shortcodePart("blobfox"), imagePart(gifHeader(1280, 720))}
			},
			wantShortcode:   "blobfox",
			wantContentType: "image/gif",
		},
		{
			name: "PNG at the pixel limit",
			parts: func(*testing.T) []emojiPart {
				return []emojiPart{shortcodePart("blobfox"), imagePart(pngHeader(7680, 4320))}
			},
			wantShortcode:   "blobfox",
			wantContentType: "image/png",
		},
		{
			name: "shortcode of 128 characters",
			parts: func(t *testing.T) []emojiPart {
				return []emojiPart{shortcodePart(strings.Repeat("a", 128)), imagePart(pngImage(t))}
			},
			wantShortcode:   strings.Repeat("a", 128),
			wantContentType: "image/png",
		},
		{
			name:            "shortcode of 2 characters",
			parts:           func(t *testing.T) []emojiPart { return []emojiPart{shortcodePart("ab"), imagePart(pngImage(t))} },
			wantShortcode:   "ab",
			wantContentType: "image/png",
		},
		{
			name:            "shortcode a remote emoji uses",
			parts:           func(t *testing.T) []emojiPart { return []emojiPart{shortcodePart("blobcat"), imagePart(pngImage(t))} },
			wantShortcode:   "blobcat",
			wantContentType: "image/png",
		},
		{
			name:            "local shortcode differing in case",
			parts:           func(t *testing.T) []emojiPart { return []emojiPart{shortcodePart("BlobCat"), imagePart(pngImage(t))} },
			wantShortcode:   "BlobCat",
			wantContentType: "image/png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStubEmojiStore(localEmoji(1, "blobcat_local", storedBlobcat), remoteEmoji(2, "blobcat", "example.com"))
			f := newEmojiFixture(store, newStubStorage())
			parts := tt.parts(t)
			var upload []byte
			for _, part := range parts {
				if part.name == "image" {
					upload = part.data
				}
			}

			rec := f.serve(createRequest(t, parts...))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
			}
			if len(store.created) != 1 {
				t.Fatalf("created %d emojis, want 1", len(store.created))
			}
			created := store.created[0]
			path := created.ImageFileName.String
			if !storedImagePath.MatchString(path) {
				t.Fatalf("image stored at %q, want custom_emojis/images/original/<16 hex>.<ext>", path)
			}
			if want := "." + customEmojiImageTypes[tt.wantContentType]; filepath.Ext(path) != want {
				t.Errorf("image extension = %q, want %q", filepath.Ext(path), want)
			}
			if stored, ok := f.storage.files[path]; !ok || !bytes.Equal(stored, upload) {
				t.Errorf("stored image at %q present = %t, matches the upload = %t", path, ok, bytes.Equal(stored, upload))
			}
			if len(f.storage.files) != 1 {
				t.Errorf("storage holds %d files, want 1", len(f.storage.files))
			}

			if created.Shortcode != tt.wantShortcode || created.Domain.Valid {
				t.Errorf("created shortcode %q domain %v, want local %q", created.Shortcode, created.Domain, tt.wantShortcode)
			}
			if created.ImageContentType.String != tt.wantContentType || !created.ImageContentType.Valid {
				t.Errorf("content type = %v, want %q", created.ImageContentType, tt.wantContentType)
			}
			if !created.ImageFileSize.Valid || int(created.ImageFileSize.Int32) != len(upload) {
				t.Errorf("file size = %v, want %d", created.ImageFileSize, len(upload))
			}
			if !created.VisibleInPicker || created.CategoryID.Valid {
				t.Errorf("visible_in_picker = %t, category = %v; want visible, no category", created.VisibleInPicker, created.CategoryID)
			}

			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			want := map[string]any{
				"id":                "101",
				"shortcode":         tt.wantShortcode,
				"url":               "/files/" + path,
				"static_url":        "/files/" + path,
				"visible_in_picker": true,
				"disabled":          false,
				"domain":            "",
			}
			if len(resp) != len(want) {
				t.Errorf("response = %v, want %v", resp, want)
			}
			for key, value := range want {
				if resp[key] != value {
					t.Errorf("response[%q] = %#v, want %#v", key, resp[key], value)
				}
			}
		})
	}
}

func TestCustomEmojisCreateValidation(t *testing.T) {
	const tooLarge = "Validation failed: Image must be less than 256 KB, Image file size must be less than 256 KB"

	tests := []struct {
		name    string
		request func(t *testing.T) *http.Request
		status  int
		message string
	}{
		// Image.
		{
			name:    "no image",
			request: func(t *testing.T) *http.Request { return createRequest(t, shortcodePart("blobfox")) },
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image can't be blank",
		},
		{
			name: "image sent as a value, not a file",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobfox"), emojiPart{name: "image", data: pngImage(t)})
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image can't be blank",
		},
		{
			name: "jpeg",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobfox"), imagePart(jpegImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image content type is invalid, Image is invalid",
		},
		{
			name: "text declared as image/png",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobfox"), imagePart([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>")))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image content type is invalid, Image is invalid",
		},
		{
			name:    "empty file",
			request: func(t *testing.T) *http.Request { return createRequest(t, shortcodePart("blobfox"), imagePart(nil)) },
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image content type is invalid, Image is invalid",
		},
		{
			name: "PNG signature without a readable header",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobfox"), imagePart([]byte("\x89PNG\r\n\x1a\nnot a png")))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image content type is invalid, Image is invalid",
		},
		{
			name: "image at the limit",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobfox"), imagePart(paddedPNG(t, customEmojiImageLimit)))
			},
			status:  http.StatusUnprocessableEntity,
			message: tooLarge,
		},
		{
			name: "jpeg at the limit",
			request: func(t *testing.T) *http.Request {
				data := jpegImage(t)
				data = append(data, make([]byte, customEmojiImageLimit-len(data))...)
				return createRequest(t, shortcodePart("blobfox"), imagePart(data))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image content type is invalid, Image is invalid, Image must be less than 256 KB, Image file size must be less than 256 KB",
		},

		// Dimensions, checked before anything else.
		{
			name: "PNG above the pixel limit",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("x"), imagePart(pngHeader(7681, 4320)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "7681x4320 images are not supported",
		},
		{
			name: "GIF above the GIF pixel limit",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobfox"), imagePart(gifHeader(1281, 720)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "1281x720 GIF files are not supported",
		},
		{
			name: "WebP above the pixel limit",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobfox"), imagePart(webpVP8X(10000, 10000, false)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "10000x10000 images are not supported",
		},

		// Shortcode.
		{
			name:    "no shortcode",
			request: func(t *testing.T) *http.Request { return createRequest(t, imagePart(pngImage(t))) },
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid, Shortcode is too short (minimum is 2 characters)",
		},
		{
			name:    "one character",
			request: func(t *testing.T) *http.Request { return createRequest(t, shortcodePart("a"), imagePart(pngImage(t))) },
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid, Shortcode is too short (minimum is 2 characters)",
		},
		{
			name: "hyphen",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blob-fox"), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid",
		},
		{
			name: "space",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blob fox"), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid",
		},
		{
			name: "trailing newline",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobfox\n"), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid",
		},
		{
			name: "colons",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart(":blobfox:"), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid",
		},
		{
			name: "non-ASCII letter",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blöbfox"), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid",
		},
		{
			name: "one non-ASCII character counts as one",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("ö"), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid, Shortcode is too short (minimum is 2 characters)",
		},
		{
			name: "129 characters",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart(strings.Repeat("a", 129)), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is too long (maximum is 128 characters)",
		},
		{
			name: "129 non-ASCII characters",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart(strings.Repeat("ö", 129)), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid, Shortcode is too long (maximum is 128 characters)",
		},
		{
			name: "2049 characters",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart(strings.Repeat("a", 2049)), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is too long (maximum is 2048 characters), Shortcode is too long (maximum is 128 characters)",
		},
		{
			name: "duplicate local shortcode",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobcat"), imagePart(pngImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode has already been taken",
		},

		// Several errors, in Mastodon's order.
		{
			name: "duplicate shortcode and wrong type",
			request: func(t *testing.T) *http.Request {
				return createRequest(t, shortcodePart("blobcat"), imagePart(jpegImage(t)))
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image content type is invalid, Image is invalid, Shortcode has already been taken",
		},
		{
			name:    "no image and an invalid shortcode",
			request: func(t *testing.T) *http.Request { return createRequest(t, shortcodePart("a-")) },
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image can't be blank, Shortcode is invalid",
		},

		// Bodies other than multipart.
		{
			name: "form-encoded body",
			request: func(*testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", strings.NewReader("shortcode=blobfox"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return req
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image can't be blank",
		},
		{
			name: "JSON body",
			request: func(*testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", strings.NewReader(`{"shortcode":"blobfox"}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image can't be blank, Shortcode is invalid, Shortcode is too short (minimum is 2 characters)",
		},
		{
			name: "no body",
			request: func(*testing.T) *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", nil)
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Image can't be blank, Shortcode is invalid, Shortcode is too short (minimum is 2 characters)",
		},
		{
			name: "query string shortcode is ignored",
			request: func(t *testing.T) *http.Request {
				body, contentType := multipartBody(t, imagePart(pngImage(t)))
				req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis?shortcode=blobfox", body)
				req.Header.Set("Content-Type", contentType)
				return req
			},
			status:  http.StatusUnprocessableEntity,
			message: "Validation failed: Shortcode is invalid, Shortcode is too short (minimum is 2 characters)",
		},
		{
			name: "multipart body without a boundary",
			request: func(*testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", strings.NewReader("--x\r\n"))
				req.Header.Set("Content-Type", "multipart/form-data")
				return req
			},
			status:  http.StatusBadRequest,
			message: "Invalid request body",
		},
		{
			name: "truncated multipart body",
			request: func(*testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis",
					strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"shortcode\"\r\n\r\nblobfox"))
				req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
				return req
			},
			status:  http.StatusBadRequest,
			message: "Invalid request body",
		},
		{
			name: "form-encoded body larger than the limit",
			request: func(*testing.T) *http.Request {
				body := "shortcode=" + strings.Repeat("a", maxCustomEmojiRequestBytes)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", io.NopCloser(strings.NewReader(body)))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return req
			},
			status:  http.StatusUnprocessableEntity,
			message: tooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStubEmojiStore(localEmoji(1, "blobcat", storedBlobcat))
			storage := newStubStorage()
			storage.files[storedBlobcat] = []byte("blobcat")
			f := newEmojiFixture(store, storage)

			rec := f.serve(tt.request(t))

			if rec.Code != tt.status {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if got := errorMessage(t, rec); got != tt.message {
				t.Errorf("error = %q, want %q", got, tt.message)
			}
			if len(store.created) != 0 {
				t.Errorf("created %v", store.created)
			}
			if len(storage.files) != 1 || len(storage.deletes) != 0 {
				t.Errorf("storage changed: files %v, deletes %v", filePaths(storage.files), storage.deletes)
			}
		})
	}
}

// filePaths lists a storage's file paths.
func filePaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

// countingReader counts the bytes read from it.
type countingReader struct {
	r    io.Reader
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}

func TestCustomEmojisCreateOversizedBody(t *testing.T) {
	const message = "Validation failed: Image must be less than 256 KB, Image file size must be less than 256 KB"
	// bodySize is far larger than the limit, so a handler that read it in
	// full would show.
	const bodySize = 8 << 20

	t.Run("declared by Content-Length", func(t *testing.T) {
		f := newEmojiFixture(newStubEmojiStore(), newStubStorage())
		probe := &bodyProbe{Reader: strings.NewReader("x")}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", probe)
		req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		req.ContentLength = maxCustomEmojiRequestBytes + 1

		rec := f.serve(req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d (%s), want 422", rec.Code, rec.Body.String())
		}
		if got := errorMessage(t, rec); got != message {
			t.Errorf("error = %q, want %q", got, message)
		}
		if probe.read {
			t.Error("body declared too large was read")
		}
	})

	for _, tt := range []struct {
		name  string
		parts func(t *testing.T) []emojiPart
	}{
		{
			name: "image larger than the limit",
			parts: func(t *testing.T) []emojiPart {
				return []emojiPart{shortcodePart("blobfox"), imagePart(paddedPNG(t, bodySize))}
			},
		},
		{
			name: "shortcode larger than the limit",
			parts: func(t *testing.T) []emojiPart {
				return []emojiPart{shortcodePart(strings.Repeat("a", bodySize)), imagePart(pngImage(t))}
			},
		},
		{
			name: "valid image with overhead past the limit",
			parts: func(t *testing.T) []emojiPart {
				return []emojiPart{
					shortcodePart("blobfox"),
					imagePart(paddedPNG(t, customEmojiImageLimit-1)),
					{name: "padding", data: make([]byte, customEmojiRequestOverhead)},
				}
			},
		},
	} {
		t.Run("chunked, "+tt.name, func(t *testing.T) {
			store := newStubEmojiStore()
			f := newEmojiFixture(store, newStubStorage())
			body, contentType := multipartBody(t, tt.parts(t)...)
			counter := &countingReader{r: body}
			// A reader of unknown length leaves ContentLength at -1, as for
			// a chunked request.
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", counter)
			req.Header.Set("Content-Type", contentType)
			if req.ContentLength != -1 {
				t.Fatalf("ContentLength = %d, want -1", req.ContentLength)
			}

			rec := f.serve(req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d (%s), want 422", rec.Code, rec.Body.String())
			}
			if got := errorMessage(t, rec); got != message {
				t.Errorf("error = %q, want %q", got, message)
			}
			if counter.read > maxCustomEmojiRequestBytes+1 {
				t.Errorf("read %d bytes, more than the %d byte limit", counter.read, maxCustomEmojiRequestBytes)
			}
			if len(store.created) != 0 || len(f.storage.files) != 0 {
				t.Errorf("created %v, stored %v", store.created, filePaths(f.storage.files))
			}
		})
	}
}

func TestCustomEmojisCreateFailures(t *testing.T) {
	canceled := func(req *http.Request) *http.Request {
		ctx, cancel := context.WithCancel(req.Context())
		cancel()
		return req.WithContext(ctx)
	}
	errStorage := errors.New("storage unavailable")

	tests := []struct {
		name        string
		shortcode   string
		setup       func(store *stubEmojiStore, storage *stubStorage)
		status      int
		message     string
		wantRemoved bool
	}{
		{
			name:      "shortcode check fails",
			shortcode: "blobfox",
			setup:     func(store *stubEmojiStore, _ *stubStorage) { store.takenErr = errDatabase },
			status:    http.StatusInternalServerError,
			message:   "Database error",
		},
		{
			name:      "storing the image fails",
			shortcode: "blobfox",
			setup:     func(_ *stubEmojiStore, storage *stubStorage) { storage.saveErr = errStorage },
			status:    http.StatusInternalServerError,
			message:   "Failed to store image",
		},
		{
			name:        "insert fails",
			shortcode:   "blobfox",
			setup:       func(store *stubEmojiStore, _ *stubStorage) { store.createErr = errDatabase },
			status:      http.StatusInternalServerError,
			message:     "Database error",
			wantRemoved: true,
		},
		{
			name:        "shortcode taken between the check and the insert",
			shortcode:   "blobfox",
			setup:       func(store *stubEmojiStore, _ *stubStorage) { store.raceShortcode = "blobfox" },
			status:      http.StatusUnprocessableEntity,
			message:     "Validation failed: Shortcode has already been taken",
			wantRemoved: true,
		},
		{
			name:      "insert fails and removing the image fails",
			shortcode: "blobfox",
			setup: func(store *stubEmojiStore, storage *stubStorage) {
				store.createErr = errDatabase
				storage.deleteErr = errStorage
			},
			status:      http.StatusInternalServerError,
			message:     "Database error",
			wantRemoved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStubEmojiStore()
			storage := newStubStorage()
			tt.setup(store, storage)
			f := newEmojiFixture(store, storage)

			rec := f.serve(createRequest(t, shortcodePart(tt.shortcode), imagePart(pngImage(t))))

			if rec.Code != tt.status {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if got := errorMessage(t, rec); got != tt.message {
				t.Errorf("error = %q, want %q", got, tt.message)
			}
			if len(store.created) != 0 {
				t.Errorf("created %v", store.created)
			}
			if !tt.wantRemoved {
				if len(storage.deletes) != 0 || len(storage.files) != 0 {
					t.Errorf("stored %v, deleted %v; want nothing", filePaths(storage.files), storage.deletes)
				}
				return
			}
			if len(storage.deletes) != 1 || !storedImagePath.MatchString(storage.deletes[0]) {
				t.Fatalf("deleted %v, want the stored image", storage.deletes)
			}
			if storage.deleteErr == nil && len(storage.files) != 0 {
				t.Errorf("files left after the failed insert: %v", filePaths(storage.files))
			}
		})
	}

	t.Run("image removed after the request is canceled", func(t *testing.T) {
		store := newStubEmojiStore()
		store.createErr = errDatabase
		storage := newStubStorage()
		h := &CustomEmojisHandler{store: store, storage: storage, roles: rolesFor(adminPermissions)}

		req := canceled(createRequest(t, shortcodePart("blobfox"), imagePart(pngImage(t))))
		rec := httptest.NewRecorder()
		h.Create(rec, req)

		if len(storage.deletes) != 1 {
			t.Fatalf("deleted %v, want the stored image", storage.deletes)
		}
		if err := storage.deleteCtxErrs[0]; err != nil {
			t.Errorf("image removed with a context that is done: %v", err)
		}
		if len(storage.files) != 0 {
			t.Errorf("files left: %v", filePaths(storage.files))
		}
	})
}

func TestCustomEmojisDelete(t *testing.T) {
	const outside = "media_attachments/files/1/x.png"
	const traversal = "custom_emojis/images/original/../../../config.yaml"

	tests := []struct {
		name        string
		id          string
		setup       func(store *stubEmojiStore, storage *stubStorage)
		status      int
		message     string
		wantDeleted []int64
		wantRemoved []string
	}{
		{
			name:        "local emoji",
			id:          "1",
			status:      http.StatusOK,
			wantDeleted: []int64{1},
			wantRemoved: []string{storedBlobcat},
		},
		{
			name:        "local emoji whose image is already gone",
			id:          "1",
			setup:       func(_ *stubEmojiStore, storage *stubStorage) { delete(storage.files, storedBlobcat) },
			status:      http.StatusOK,
			wantDeleted: []int64{1},
			wantRemoved: []string{storedBlobcat},
		},
		{
			name:        "removing the image fails",
			id:          "1",
			setup:       func(_ *stubEmojiStore, storage *stubStorage) { storage.deleteErr = errors.New("storage unavailable") },
			status:      http.StatusOK,
			wantDeleted: []int64{1},
			wantRemoved: []string{storedBlobcat},
		},
		{
			name:        "remote emoji without a stored image",
			id:          "2",
			status:      http.StatusOK,
			wantDeleted: []int64{2},
		},
		{
			name:        "image outside the custom emoji directory",
			id:          "3",
			status:      http.StatusOK,
			wantDeleted: []int64{3},
		},
		{
			name:        "image path escaping the custom emoji directory",
			id:          "4",
			status:      http.StatusOK,
			wantDeleted: []int64{4},
		},
		{
			name:    "unknown ID",
			id:      "99",
			status:  http.StatusNotFound,
			message: "Record not found",
		},
		{
			name:    "non-numeric ID",
			id:      "blobcat",
			status:  http.StatusNotFound,
			message: "Record not found",
		},
		{
			name:    "ID beyond int64",
			id:      "99999999999999999999",
			status:  http.StatusNotFound,
			message: "Record not found",
		},
		{
			name:    "database error",
			id:      "1",
			setup:   func(store *stubEmojiStore, _ *stubStorage) { store.deleteErr = errDatabase },
			status:  http.StatusInternalServerError,
			message: "Database error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStubEmojiStore(
				localEmoji(1, "blobcat", storedBlobcat),
				remoteEmoji(2, "blobcat", "example.com"),
				localEmoji(3, "outside", outside),
				localEmoji(4, "traversal", traversal),
			)
			storage := newStubStorage()
			for _, path := range []string{storedBlobcat, outside, traversal} {
				storage.files[path] = []byte(path)
			}
			if tt.setup != nil {
				tt.setup(store, storage)
			}
			before := len(store.emojis)
			f := newEmojiFixture(store, storage)

			rec := f.serve(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/custom_emojis/"+tt.id, nil))

			if rec.Code != tt.status {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if tt.message != "" {
				if got := errorMessage(t, rec); got != tt.message {
					t.Errorf("error = %q, want %q", got, tt.message)
				}
			} else if body := strings.TrimSpace(rec.Body.String()); body != "{}" {
				t.Errorf("body = %s, want {}", body)
			}
			if !slices.Equal(store.deleted, tt.wantDeleted) {
				t.Errorf("deleted rows %v, want %v", store.deleted, tt.wantDeleted)
			}
			if len(store.emojis) != before-len(tt.wantDeleted) {
				t.Errorf("%d emojis left, want %d", len(store.emojis), before-len(tt.wantDeleted))
			}
			if !slices.Equal(storage.deletes, tt.wantRemoved) {
				t.Errorf("removed files %v, want %v", storage.deletes, tt.wantRemoved)
			}
			for _, path := range []string{outside, traversal} {
				if _, ok := storage.files[path]; !ok {
					t.Errorf("%s was removed", path)
				}
			}
		})
	}
}

func TestStoredCustomEmojiFiles(t *testing.T) {
	tests := []struct {
		name string
		file pgtype.Text
		want []string
	}{
		{"stored image", pgtype.Text{String: storedBlobcat, Valid: true}, []string{storedBlobcat}},
		{"no image", pgtype.Text{}, nil},
		{"empty name", pgtype.Text{String: "", Valid: true}, nil},
		{"the directory itself", pgtype.Text{String: customEmojiImageDir, Valid: true}, nil},
		{"outside the directory", pgtype.Text{String: "media_attachments/files/1/x.png", Valid: true}, nil},
		{"escaping the directory", pgtype.Text{String: customEmojiImageDir + "../../x.png", Valid: true}, nil},
		{"doubled slash", pgtype.Text{String: customEmojiImageDir + "/x.png", Valid: true}, nil},
		{"absolute path", pgtype.Text{String: "/" + storedBlobcat, Valid: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storedCustomEmojiFiles(db.CustomEmoji{ImageFileName: tt.file})
			if !slices.Equal(got, tt.want) {
				t.Errorf("storedCustomEmojiFiles(%q) = %v, want %v", tt.file.String, got, tt.want)
			}
		})
	}
}

func TestCustomEmojisList(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantLimit  int32
		wantOffset int32
	}{
		{"defaults", "", 100, 0},
		{"limit and offset", "?limit=5&offset=10", 5, 10},
		{"limit of 1", "?limit=1", 1, 0},
		{"limit of 200", "?limit=200", 200, 0},
		{"limit above 200", "?limit=201", 100, 0},
		{"limit of 0", "?limit=0", 100, 0},
		{"negative limit", "?limit=-5", 100, 0},
		{"non-numeric limit", "?limit=all", 100, 0},
		{"negative offset", "?offset=-1", 100, 0},
		{"non-numeric offset", "?offset=x", 100, 0},
		{"offset beyond int32", "?offset=3000000000", 100, math.MaxInt32},
		{"offset beyond int64", "?offset=99999999999999999999", 100, math.MaxInt32},
		{"offset below int64", "?offset=-99999999999999999999", 100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStubEmojiStore()
			f := newEmojiFixture(store, newStubStorage())

			rec := f.serve(httptest.NewRequest(http.MethodGet, "/api/v1/admin/custom_emojis"+tt.query, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
			}
			want := db.AdminListCustomEmojisParams{Limit: tt.wantLimit, Offset: tt.wantOffset}
			if len(store.listParams) != 1 || store.listParams[0] != want {
				t.Errorf("listed with %v, want %v", store.listParams, want)
			}
			if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
				t.Errorf("body = %s, want []", body)
			}
		})
	}
}

func TestCustomEmojisListResponse(t *testing.T) {
	withCategory := func(emoji db.CustomEmoji, category int64) db.CustomEmoji {
		emoji.CategoryID = pgtype.Int8{Int64: category, Valid: true}
		return emoji
	}
	store := newStubEmojiStore(
		withCategory(localEmoji(1, "blobcat", storedBlobcat), 7),
		remoteEmoji(2, "blobfox", "example.com"),
		withCategory(localEmoji(3, "uncategorized", "custom_emojis/images/original/fedcba9876543210.gif"), 8),
	)
	store.categories[7] = "Blobs"
	f := newEmojiFixture(store, newStubStorage())

	rec := f.serve(httptest.NewRequest(http.MethodGet, "/api/v1/admin/custom_emojis?limit=2", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := []map[string]any{
		{
			"id": "3", "shortcode": "uncategorized",
			"url":               "/files/custom_emojis/images/original/fedcba9876543210.gif",
			"static_url":        "/files/custom_emojis/images/original/fedcba9876543210.gif",
			"visible_in_picker": true, "disabled": false, "domain": "",
		},
		{
			"id": "2", "shortcode": "blobfox",
			"url": "https://example.com/emoji.png", "static_url": "https://example.com/emoji.png",
			"visible_in_picker": true, "disabled": false, "domain": "example.com",
		},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d emojis, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Errorf("emoji %d = %v, want %v", i, got[i], want[i])
			continue
		}
		for key, value := range want[i] {
			if got[i][key] != value {
				t.Errorf("emoji %d [%q] = %#v, want %#v", i, key, got[i][key], value)
			}
		}
	}

	rec = f.serve(httptest.NewRequest(http.MethodGet, "/api/v1/admin/custom_emojis?offset=2", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || got[0]["category"] != "Blobs" {
		t.Errorf("second page = %v, want blobcat in category Blobs", got)
	}
}

func TestCustomEmojisListFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*stubEmojiStore)
	}{
		{"listing fails", func(s *stubEmojiStore) { s.listErr = errDatabase }},
		{"loading categories fails", func(s *stubEmojiStore) { s.categoryErr = errDatabase }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			emoji := localEmoji(1, "blobcat", storedBlobcat)
			emoji.CategoryID = pgtype.Int8{Int64: 7, Valid: true}
			store := newStubEmojiStore(emoji)
			tt.setup(store)
			f := newEmojiFixture(store, newStubStorage())

			rec := f.serve(httptest.NewRequest(http.MethodGet, "/api/v1/admin/custom_emojis", nil))

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d (%s), want 500", rec.Code, rec.Body.String())
			}
			if got := errorMessage(t, rec); got != "Database error" {
				t.Errorf("error = %q, want %q", got, "Database error")
			}
		})
	}
}

// TestCustomEmojisRefusedRequests checks that a refused request changes
// nothing and that a refused create's body is never read. The route table
// tests cover the refusals' statuses for every caller.
func TestCustomEmojisRefusedRequests(t *testing.T) {
	tests := []struct {
		name          string
		authenticated bool
		scopes        string
		permissions   int64
		status        int
	}{
		{"no token", false, "", 0, http.StatusUnauthorized},
		{"user scopes", true, "read write follow push", ownerPermissions, http.StatusForbidden},
		{"admin:read", true, oauth.ScopeAdminRead, ownerPermissions, http.StatusForbidden},
		{"Moderator", true, adminToken, moderatorPermissions, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStubEmojiStore(localEmoji(1, "blobcat", storedBlobcat))
			storage := newStubStorage()
			storage.files[storedBlobcat] = []byte("blobcat")
			h := &CustomEmojisHandler{store: store, storage: storage, roles: rolesFor(tt.permissions)}
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)
			authorize := func(req *http.Request) *http.Request {
				if !tt.authenticated {
					return req
				}
				return withScopes(withIdentity(req, int64Ptr(10), int64Ptr(20)), tt.scopes)
			}

			body, contentType := multipartBody(t, shortcodePart("blobfox"), imagePart(pngImage(t)))
			probe := &bodyProbe{Reader: body}
			create := httptest.NewRequest(http.MethodPost, "/api/v1/admin/custom_emojis", probe)
			create.Header.Set("Content-Type", contentType)
			deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/custom_emojis/1", nil)

			for _, req := range []*http.Request{create, deleteReq} {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, authorize(req))
				if rec.Code != tt.status {
					t.Errorf("%s: status = %d (%s), want %d", req.Method, rec.Code, rec.Body.String(), tt.status)
				}
			}

			if probe.read {
				t.Error("refused create's body was read")
			}
			if len(store.created) != 0 || len(store.deleted) != 0 || len(store.emojis) != 1 {
				t.Errorf("store changed: created %v, deleted %v", store.created, store.deleted)
			}
			if len(storage.files) != 1 || len(storage.deletes) != 0 {
				t.Errorf("storage changed: files %v, deletes %v", filePaths(storage.files), storage.deletes)
			}
		})
	}
}

// TestCustomEmojisLocalStorage creates and deletes an emoji through
// media.LocalStorage, so the files land on and leave the disk.
func TestCustomEmojisLocalStorage(t *testing.T) {
	dir := t.TempDir()
	store := newStubEmojiStore()
	h := &CustomEmojisHandler{
		store:   store,
		storage: media.NewLocalStorage(dir, "https://example.com/files"),
		roles:   rolesFor(adminPermissions),
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, withScopes(withIdentity(req, int64Ptr(10), int64Ptr(20)), adminToken))
		return rec
	}

	upload := pngImage(t)
	rec := serve(createRequest(t, shortcodePart("blobfox"), imagePart(upload)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	path := store.created[0].ImageFileName.String
	onDisk, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read stored image: %v", err)
	}
	if !bytes.Equal(onDisk, upload) {
		t.Error("stored image differs from the upload")
	}

	rec = serve(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/custom_emojis/101", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(path))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stored image after delete: %v, want it removed", err)
	}
	if len(store.emojis) != 0 {
		t.Errorf("emojis left: %v", store.emojis)
	}
}

func TestMapAdminCustomEmoji_Category(t *testing.T) {
	emoji := db.CustomEmoji{
		ID:              12,
		Shortcode:       "party",
		Domain:          pgtype.Text{String: "", Valid: false},
		VisibleInPicker: true,
		Disabled:        false,
		ImageFileName:   pgtype.Text{String: "custom/party.png", Valid: true},
	}

	resp := mapAdminCustomEmoji(emoji, "Fun")
	if resp["category"] != "Fun" {
		t.Fatalf("category = %#v, want %q", resp["category"], "Fun")
	}

	resp = mapAdminCustomEmoji(emoji, "")
	if _, ok := resp["category"]; ok {
		t.Fatalf("category present for empty name")
	}
}
