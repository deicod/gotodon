// Package media provides media processing utilities.
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"

	_ "golang.org/x/image/webp" // WebP support
)

// Errors
var (
	ErrUnsupportedType  = errors.New("unsupported media type")
	ErrFileTooLarge     = errors.New("file too large")
	ErrInvalidImage     = errors.New("invalid image")
	ErrProcessingFailed = errors.New("media processing failed")
)

// MediaType represents the type of media
type MediaType string

const (
	MediaTypeImage   MediaType = "image"
	MediaTypeGifv    MediaType = "gifv"
	MediaTypeVideo   MediaType = "video"
	MediaTypeAudio   MediaType = "audio"
	MediaTypeUnknown MediaType = "unknown"
)

// Limits (matching Mastodon)
const (
	MaxImageSize       = 16 * 1024 * 1024 // 16 MB
	MaxVideoSize       = 103809024        // ~99 MB
	MaxImagePixels     = 33177600         // 33.2 megapixels
	MaxVideoPixels     = 8294400          // 8.3 megapixels
	MaxVideoFrameRate  = 120
	ThumbnailMaxWidth  = 400
	ThumbnailMaxHeight = 400
)

// SupportedImageTypes are MIME types we accept for images
var SupportedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// SupportedVideoTypes are MIME types we accept for videos
var SupportedVideoTypes = map[string]bool{
	"video/webm":      true,
	"video/mp4":       true,
	"video/quicktime": true,
	"video/ogg":       true,
}

// SupportedAudioTypes are MIME types we accept for audio
var SupportedAudioTypes = map[string]bool{
	"audio/mpeg":  true,
	"audio/ogg":   true,
	"audio/flac":  true,
	"audio/wav":   true,
	"audio/x-wav": true,
	"audio/mp4":   true,
	"audio/aac":   true,
}

// Attachment represents a processed media attachment
type Attachment struct {
	ID          int64
	AccountID   int64
	StatusID    *int64
	Type        MediaType
	FilePath    string
	FileName    string
	ContentType string
	FileSize    int64
	Width       int
	Height      int
	Blurhash    string
	Description string
	RemoteURL   string
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Thumbnail
	ThumbnailPath   string
	ThumbnailWidth  int
	ThumbnailHeight int
}

// FileMeta contains metadata about the media file
type FileMeta struct {
	Original      MetaSize   `json:"original,omitempty"`
	Small         MetaSize   `json:"small,omitempty"`
	Focus         *MetaFocus `json:"focus,omitempty"`
	Length        *string    `json:"length,omitempty"`
	Duration      *float64   `json:"duration,omitempty"`
	Fps           *int       `json:"fps,omitempty"`
	Size          *string    `json:"size,omitempty"`
	Aspect        *float64   `json:"aspect,omitempty"`
	AudioEncode   *string    `json:"audio_encode,omitempty"`
	AudioBitrate  *string    `json:"audio_bitrate,omitempty"`
	AudioChannels *string    `json:"audio_channels,omitempty"`
}

// MetaSize contains dimension information
type MetaSize struct {
	Width  int     `json:"width,omitempty"`
	Height int     `json:"height,omitempty"`
	Size   string  `json:"size,omitempty"`
	Aspect float64 `json:"aspect,omitempty"`
}

// MetaFocus contains focal point for cropping
type MetaFocus struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// DetectType determines the media type from MIME type
func DetectType(contentType string) MediaType {
	contentType = strings.ToLower(strings.Split(contentType, ";")[0])

	if SupportedImageTypes[contentType] {
		if contentType == "image/gif" {
			return MediaTypeGifv
		}
		return MediaTypeImage
	}
	if SupportedVideoTypes[contentType] {
		return MediaTypeVideo
	}
	if SupportedAudioTypes[contentType] {
		return MediaTypeAudio
	}
	return MediaTypeUnknown
}

// ValidateType checks if the content type is supported
func ValidateType(contentType string) error {
	mediaType := DetectType(contentType)
	if mediaType == MediaTypeUnknown {
		return ErrUnsupportedType
	}
	return nil
}

// ValidateSize checks if the file size is within limits
func ValidateSize(size int64, mediaType MediaType) error {
	var maxSize int64
	switch mediaType {
	case MediaTypeImage, MediaTypeGifv:
		maxSize = MaxImageSize
	case MediaTypeVideo, MediaTypeAudio:
		maxSize = MaxVideoSize
	default:
		return ErrUnsupportedType
	}

	if size > maxSize {
		return ErrFileTooLarge
	}
	return nil
}

// DetectContentType detects the content type from file data
func DetectContentType(data []byte) string {
	return http.DetectContentType(data)
}

// GenerateFilename generates a unique filename for storage
func GenerateFilename(originalName string) string {
	ext := filepath.Ext(originalName)
	if ext == "" {
		ext = ".bin"
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d-%s", time.Now().UnixNano(), originalName)))
	return hex.EncodeToString(hash[:16]) + ext
}

// GetImageDimensions returns the dimensions of an image
func GetImageDimensions(r io.Reader) (width, height int, err error) {
	config, _, err := image.DecodeConfig(r)
	if err != nil {
		return 0, 0, fmt.Errorf("decoding image config: %w", err)
	}
	return config.Width, config.Height, nil
}

// ValidateImageDimensions checks if image dimensions are within limits
func ValidateImageDimensions(width, height int) error {
	pixels := width * height
	if pixels > MaxImagePixels {
		return fmt.Errorf("image too large: %d pixels exceeds limit of %d", pixels, MaxImagePixels)
	}
	return nil
}

// GetExtensionFromMIME returns the file extension for a MIME type
func GetExtensionFromMIME(contentType string) string {
	extensions, err := mime.ExtensionsByType(contentType)
	if err != nil || len(extensions) == 0 {
		switch contentType {
		case "image/jpeg":
			return ".jpg"
		case "image/png":
			return ".png"
		case "image/gif":
			return ".gif"
		case "image/webp":
			return ".webp"
		case "video/mp4":
			return ".mp4"
		case "video/webm":
			return ".webm"
		case "audio/mpeg":
			return ".mp3"
		default:
			return ".bin"
		}
	}
	return extensions[0]
}

// ProcessImage processes an uploaded image and extracts metadata
func ProcessImage(data []byte) (*FileMeta, error) {
	reader := bytes.NewReader(data)
	config, format, err := image.DecodeConfig(reader)
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}

	if err := ValidateImageDimensions(config.Width, config.Height); err != nil {
		return nil, err
	}

	aspect := float64(config.Width) / float64(config.Height)
	size := fmt.Sprintf("%dx%d", config.Width, config.Height)

	meta := &FileMeta{
		Original: MetaSize{
			Width:  config.Width,
			Height: config.Height,
			Size:   size,
			Aspect: aspect,
		},
	}

	// Calculate thumbnail dimensions
	thumbWidth, thumbHeight := calculateThumbnailSize(config.Width, config.Height)
	meta.Small = MetaSize{
		Width:  thumbWidth,
		Height: thumbHeight,
		Size:   fmt.Sprintf("%dx%d", thumbWidth, thumbHeight),
		Aspect: aspect,
	}

	_ = format // Could use this for validation

	return meta, nil
}

// Thumbnail contains generated thumbnail data.
type Thumbnail struct {
	Data        []byte
	ContentType string
	Width       int
	Height      int
}

// GenerateThumbnail decodes an image and produces a resized thumbnail.
func GenerateThumbnail(data []byte, contentType string) (*Thumbnail, error) {
	if len(data) == 0 {
		return nil, ErrInvalidImage
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidImage
	}

	bounds := img.Bounds()
	if bounds.Empty() {
		return nil, ErrInvalidImage
	}

	width, height := bounds.Dx(), bounds.Dy()
	thumbWidth, thumbHeight := calculateThumbnailSize(width, height)

	dst := image.NewRGBA(image.Rect(0, 0, thumbWidth, thumbHeight))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	thumbFormat := thumbnailFormat(contentType, format)
	var buf bytes.Buffer
	switch thumbFormat {
	case "jpeg":
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
	case "gif":
		if err := gif.Encode(&buf, dst, nil); err != nil {
			return nil, fmt.Errorf("encode gif: %w", err)
		}
	default:
		if err := png.Encode(&buf, dst); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
	}

	return &Thumbnail{
		Data:        buf.Bytes(),
		ContentType: formatContentType(thumbFormat),
		Width:       thumbWidth,
		Height:      thumbHeight,
	}, nil
}

func thumbnailFormat(contentType, decodedFormat string) string {
	normalized := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch normalized {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	}
	switch strings.ToLower(decodedFormat) {
	case "jpeg", "jpg":
		return "jpeg"
	case "png":
		return "png"
	case "gif":
		return "gif"
	default:
		return "png"
	}
}

func formatContentType(format string) string {
	switch format {
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	default:
		return "image/png"
	}
}

// calculateThumbnailSize calculates appropriate thumbnail dimensions
func calculateThumbnailSize(width, height int) (int, int) {
	if width <= ThumbnailMaxWidth && height <= ThumbnailMaxHeight {
		return width, height
	}

	ratio := float64(width) / float64(height)

	if width > height {
		newWidth := ThumbnailMaxWidth
		newHeight := int(float64(newWidth) / ratio)
		if newHeight > ThumbnailMaxHeight {
			newHeight = ThumbnailMaxHeight
			newWidth = int(float64(newHeight) * ratio)
		}
		return newWidth, newHeight
	}

	newHeight := ThumbnailMaxHeight
	newWidth := int(float64(newHeight) * ratio)
	if newWidth > ThumbnailMaxWidth {
		newWidth = ThumbnailMaxWidth
		newHeight = int(float64(newWidth) / ratio)
	}
	return newWidth, newHeight
}

// BuildMediaMeta builds the file_meta JSON for a media attachment
func BuildMediaMeta(width, height int, mediaType MediaType) *FileMeta {
	if width == 0 || height == 0 {
		return nil
	}

	aspect := float64(width) / float64(height)
	size := fmt.Sprintf("%dx%d", width, height)
	thumbW, thumbH := calculateThumbnailSize(width, height)

	return &FileMeta{
		Original: MetaSize{
			Width:  width,
			Height: height,
			Size:   size,
			Aspect: aspect,
		},
		Small: MetaSize{
			Width:  thumbW,
			Height: thumbH,
			Size:   fmt.Sprintf("%dx%d", thumbW, thumbH),
			Aspect: aspect,
		},
	}
}
