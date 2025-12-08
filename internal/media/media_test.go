package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// createTestImage creates a simple test image
func createTestImage(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestDetectType(t *testing.T) {
	tests := []struct {
		contentType string
		want        MediaType
	}{
		{"image/jpeg", MediaTypeImage},
		{"image/png", MediaTypeImage},
		{"image/gif", MediaTypeGifv},
		{"image/webp", MediaTypeImage},
		{"video/mp4", MediaTypeVideo},
		{"video/webm", MediaTypeVideo},
		{"audio/mpeg", MediaTypeAudio},
		{"audio/ogg", MediaTypeAudio},
		{"text/plain", MediaTypeUnknown},
		{"application/json", MediaTypeUnknown},
		{"image/jpeg; charset=utf-8", MediaTypeImage}, // with params
	}

	for _, tt := range tests {
		got := DetectType(tt.contentType)
		if got != tt.want {
			t.Errorf("DetectType(%q) = %v, want %v", tt.contentType, got, tt.want)
		}
	}
}

func TestValidateType(t *testing.T) {
	tests := []struct {
		contentType string
		wantErr     bool
	}{
		{"image/jpeg", false},
		{"video/mp4", false},
		{"audio/mpeg", false},
		{"text/plain", true},
		{"application/octet-stream", true},
	}

	for _, tt := range tests {
		err := ValidateType(tt.contentType)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateType(%q) error = %v, wantErr %v", tt.contentType, err, tt.wantErr)
		}
	}
}

func TestValidateSize(t *testing.T) {
	tests := []struct {
		size      int64
		mediaType MediaType
		wantErr   bool
	}{
		{1024, MediaTypeImage, false},
		{MaxImageSize, MediaTypeImage, false},
		{MaxImageSize + 1, MediaTypeImage, true},
		{MaxVideoSize, MediaTypeVideo, false},
		{MaxVideoSize + 1, MediaTypeVideo, true},
		{1024, MediaTypeAudio, false},
	}

	for _, tt := range tests {
		err := ValidateSize(tt.size, tt.mediaType)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateSize(%d, %v) error = %v, wantErr %v", tt.size, tt.mediaType, err, tt.wantErr)
		}
	}
}

func TestGenerateFilename(t *testing.T) {
	name1 := GenerateFilename("test.jpg")
	name2 := GenerateFilename("test.jpg")

	if name1 == name2 {
		t.Error("Generated filenames should be unique")
	}

	if len(name1) < 10 {
		t.Errorf("Filename too short: %s", name1)
	}
}

func TestGetImageDimensions(t *testing.T) {
	testImage := createTestImage(100, 50)
	reader := bytes.NewReader(testImage)

	width, height, err := GetImageDimensions(reader)
	if err != nil {
		t.Fatalf("GetImageDimensions() error = %v", err)
	}

	if width != 100 {
		t.Errorf("width = %d, want 100", width)
	}

	if height != 50 {
		t.Errorf("height = %d, want 50", height)
	}
}

func TestValidateImageDimensions(t *testing.T) {
	tests := []struct {
		width, height int
		wantErr       bool
	}{
		{800, 600, false},
		{4096, 4096, false},
		{10000, 5000, true}, // 50 megapixels > limit
		{1, 1, false},
	}

	for _, tt := range tests {
		err := ValidateImageDimensions(tt.width, tt.height)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateImageDimensions(%d, %d) error = %v, wantErr %v", tt.width, tt.height, err, tt.wantErr)
		}
	}
}

func TestGetExtensionFromMIME(t *testing.T) {
	// Test that extensions are returned (not specific values since they're system-dependent)
	tests := []struct {
		mime    string
		wantExt bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"image/gif", true},
		{"image/webp", true},
		{"video/mp4", true},
		{"audio/mpeg", true},
	}

	for _, tt := range tests {
		got := GetExtensionFromMIME(tt.mime)
		if tt.wantExt && (got == "" || got[0] != '.') {
			t.Errorf("GetExtensionFromMIME(%q) = %q, want a valid extension", tt.mime, got)
		}
	}
}

func TestProcessImage(t *testing.T) {
	testImage := createTestImage(800, 600)

	meta, err := ProcessImage(testImage)
	if err != nil {
		t.Fatalf("ProcessImage() error = %v", err)
	}

	if meta.Original.Width != 800 {
		t.Errorf("Original.Width = %d, want 800", meta.Original.Width)
	}

	if meta.Original.Height != 600 {
		t.Errorf("Original.Height = %d, want 600", meta.Original.Height)
	}

	if meta.Original.Size != "800x600" {
		t.Errorf("Original.Size = %q, want %q", meta.Original.Size, "800x600")
	}

	// Thumbnail should be calculated
	if meta.Small.Width == 0 || meta.Small.Height == 0 {
		t.Error("Small dimensions should be calculated")
	}
}

func TestCalculateThumbnailSize(t *testing.T) {
	tests := []struct {
		width, height int
		wantW, wantH  int
	}{
		{100, 100, 100, 100},   // small, no resize
		{800, 600, 400, 300},   // landscape
		{600, 800, 300, 400},   // portrait
		{4000, 3000, 400, 300}, // large landscape
		{400, 400, 400, 400},   // exact square
	}

	for _, tt := range tests {
		gotW, gotH := calculateThumbnailSize(tt.width, tt.height)
		if gotW != tt.wantW || gotH != tt.wantH {
			t.Errorf("calculateThumbnailSize(%d, %d) = (%d, %d), want (%d, %d)",
				tt.width, tt.height, gotW, gotH, tt.wantW, tt.wantH)
		}
	}
}

func TestDetectContentType(t *testing.T) {
	pngImage := createTestImage(10, 10)
	contentType := DetectContentType(pngImage)

	if contentType != "image/png" {
		t.Errorf("DetectContentType() = %q, want %q", contentType, "image/png")
	}
}
