package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/deicod/gotodon/internal/web"
)

// Headers every served file carries. They match the location Mastodon's
// dist/nginx.conf serves uploads (public/system) from: files never change
// once stored, browsers must not reinterpret them, and nothing they contain
// may run.
const (
	fileCacheControl          = "public, max-age=2419200, immutable"
	fileContentTypeOptions    = "nosniff"
	fileContentSecurityPolicy = "default-src 'none'; form-action 'none'"
)

// FileServer serves the files in a local storage root at FilesRoute, and the
// default avatar and header images at MissingAvatarPath and
// MissingHeaderPath. It answers GET and HEAD only. Paths outside the root,
// dot-files, directories, other non-regular files and missing files are 404.
// The Content-Type comes from the file's bytes, never from its extension,
// and anything but image, video and audio content is served as
// application/octet-stream.
type FileServer struct {
	root string
}

// NewFileServer returns a FileServer for the storage root directory.
func NewFileServer(root string) *FileServer {
	return &FileServer{root: root}
}

// RouteRegistrar is the subset of http.ServeMux the file routes need.
type RouteRegistrar interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// RegisterRoutes registers the file and default image routes. Files take two
// patterns because GET /files/{path...} would conflict with the compat
// handler's GET /{segment}/{status}; together they match every path below
// /files/. A GET pattern also matches HEAD, and ServeMux answers other
// methods with 405.
func (s *FileServer) RegisterRoutes(mux RouteRegistrar) {
	mux.HandleFunc("GET "+FilesRoute+"/{name}", s.ServeFile)
	mux.HandleFunc("GET "+FilesRoute+"/{dir}/{rest...}", s.ServeFile)
	mux.HandleFunc("GET "+MissingAvatarPath, serveFallback(missingAvatar))
	mux.HandleFunc("GET "+MissingHeaderPath, serveFallback(missingHeader))
}

// ServeFile serves the stored file named by the request path below
// FilesRoute.
func (s *FileServer) ServeFile(w http.ResponseWriter, r *http.Request) {
	setFileSecurityHeaders(w.Header())
	if !allowFileMethod(w, r) {
		return
	}

	name, ok := strings.CutPrefix(r.URL.Path, FilesRoute+"/")
	if !ok || !validStoredName(name) {
		http.NotFound(w, r)
		return
	}

	// os.Root confines the lookup, symlinks included, to the storage root.
	root, err := os.OpenRoot(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Nothing has been stored yet.
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = root.Close() }()

	file, err := root.Open(name)
	if err != nil {
		// Missing files, paths through regular files, and links leading
		// outside the root.
		http.NotFound(w, r)
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	contentType, err := sniffContentType(file)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", fileCacheControl)
	header.Set("ETag", fmt.Sprintf(`"%x-%x"`, info.ModTime().Unix(), info.Size()))
	web.ExtendDownloadDeadline(w, info.Size())
	// Content-Type is set, so ServeContent does not consult the name. It
	// handles HEAD, ranges and conditional requests.
	http.ServeContent(w, r, "", info.ModTime(), file)
}

func allowFileMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	return false
}

func setFileSecurityHeaders(header http.Header) {
	header.Set("X-Content-Type-Options", fileContentTypeOptions)
	header.Set("Content-Security-Policy", fileContentSecurityPolicy)
}

// validStoredName reports whether name is a relative path of the kind
// storage writes: no empty, "." or ".." segments, no leading slash, and no
// segment starting with a dot.
func validStoredName(name string) bool {
	if !fs.ValidPath(name) || name == "." || strings.ContainsRune(name, '\\') {
		return false
	}
	for segment := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(segment, ".") {
			return false
		}
	}
	return true
}

// sniffContentType returns the served Content-Type of the content at the
// start of r and rewinds it.
func sniffContentType(r io.ReadSeeker) (string, error) {
	var buf [512]byte
	n, err := io.ReadFull(r, buf[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return servedContentType(http.DetectContentType(buf[:n])), nil
}

// servedContentType narrows a detected content type to the image, video and
// audio types uploads hold. Everything else, SVG included, is served as
// application/octet-stream so browsers do not render it.
func servedContentType(detected string) string {
	mediaType, _, err := mime.ParseMediaType(detected)
	if err != nil {
		return "application/octet-stream"
	}
	switch {
	case mediaType == "image/svg+xml":
		return "application/octet-stream"
	case strings.HasPrefix(mediaType, "image/"),
		strings.HasPrefix(mediaType, "video/"),
		strings.HasPrefix(mediaType, "audio/"),
		mediaType == "application/ogg":
		return mediaType
	default:
		return "application/octet-stream"
	}
}

// fallbackImage is a built-in PNG served at a fixed path.
type fallbackImage struct {
	data []byte
	etag string
}

var (
	missingAvatar = sync.OnceValue(func() fallbackImage { return newFallbackImage(renderMissingAvatar()) })
	missingHeader = sync.OnceValue(func() fallbackImage { return newFallbackImage(renderMissingHeader()) })
)

func newFallbackImage(img image.Image) fallbackImage {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// Encoding an in-memory RGBA image into a buffer cannot fail.
		panic(fmt.Sprintf("media: encoding fallback image: %v", err))
	}
	sum := sha256.Sum256(buf.Bytes())
	return fallbackImage{data: buf.Bytes(), etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
}

func serveFallback(load func() fallbackImage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setFileSecurityHeaders(w.Header())
		if !allowFileMethod(w, r) {
			return
		}
		img := load()
		header := w.Header()
		header.Set("Content-Type", "image/png")
		header.Set("Cache-Control", fileCacheControl)
		header.Set("ETag", img.etag)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(img.data))
	}
}

// Fallback image geometry and colours.
const missingAvatarSize = 400

var (
	missingAvatarBackground = color.RGBA{R: 0xd4, G: 0xd4, B: 0xd8, A: 0xff}
	missingAvatarFigure     = color.RGBA{R: 0xa1, G: 0xa1, B: 0xaa, A: 0xff}
)

// renderMissingAvatar draws the default avatar: a neutral silhouette, a head
// above rounded shoulders, on a light background, anti-aliased by 4x4
// supersampling.
func renderMissingAvatar() *image.RGBA {
	const (
		size    = float64(missingAvatarSize)
		samples = 4
		headX   = size / 2
		headY   = size * 0.38
		headR   = size * 0.18
		bodyX   = size / 2
		bodyY   = size * 1.02
		bodyRX  = size * 0.36
		bodyRY  = size * 0.42
	)
	inFigure := func(x, y float64) bool {
		dx, dy := x-headX, y-headY
		if dx*dx+dy*dy <= headR*headR {
			return true
		}
		ex, ey := (x-bodyX)/bodyRX, (y-bodyY)/bodyRY
		return ex*ex+ey*ey <= 1
	}

	img := image.NewRGBA(image.Rect(0, 0, missingAvatarSize, missingAvatarSize))
	for py := range missingAvatarSize {
		for px := range missingAvatarSize {
			covered := 0
			for sy := range samples {
				for sx := range samples {
					x := float64(px) + (float64(sx)+0.5)/samples
					y := float64(py) + (float64(sy)+0.5)/samples
					if inFigure(x, y) {
						covered++
					}
				}
			}
			img.SetRGBA(px, py, blend(missingAvatarBackground, missingAvatarFigure, covered, samples*samples))
		}
	}
	return img
}

// renderMissingHeader draws the default header: a single transparent pixel,
// so clients show their own background behind profiles without a header.
func renderMissingHeader() *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, 1, 1))
}

func blend(from, to color.RGBA, weight, total int) color.RGBA {
	mix := func(a, b uint8) uint8 {
		return uint8((int(a)*(total-weight) + int(b)*weight + total/2) / total)
	}
	return color.RGBA{R: mix(from.R, to.R), G: mix(from.G, to.G), B: mix(from.B, to.B), A: mix(from.A, to.A)}
}
