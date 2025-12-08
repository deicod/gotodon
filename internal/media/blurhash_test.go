package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestBlurHash(t *testing.T) {
	// Create a simple test image
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 128, A: 255})
		}
	}

	hash := BlurHash(img, 4, 3)

	if hash == "" {
		t.Error("BlurHash should not return empty string")
	}

	// BlurHash should be short (around 20-30 chars for 4x3)
	if len(hash) > 50 {
		t.Errorf("BlurHash too long: %d chars", len(hash))
	}

	// Should only contain base83 characters
	for _, c := range hash {
		if !strings.ContainsRune(base83Chars, c) {
			t.Errorf("Invalid character in blurhash: %c", c)
		}
	}
}

func TestBlurHashFromBytes(t *testing.T) {
	// Create a test image
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: 100, G: 150, B: 200, A: 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("Failed to encode test image: %v", err)
	}

	hash, err := BlurHashFromBytes(buf.Bytes(), 4, 4)
	if err != nil {
		t.Fatalf("BlurHashFromBytes() error = %v", err)
	}

	if hash == "" {
		t.Error("BlurHash should not be empty")
	}
}

func TestBlurHashInvalidComponents(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))

	tests := []struct {
		x, y int
	}{
		{0, 4},  // x too small
		{4, 0},  // y too small
		{10, 4}, // x too large
		{4, 10}, // y too large
	}

	for _, tt := range tests {
		hash := BlurHash(img, tt.x, tt.y)
		if hash != "" {
			t.Errorf("BlurHash(%d, %d) should return empty for invalid components", tt.x, tt.y)
		}
	}
}

func TestBlurHashDeterministic(t *testing.T) {
	// Same image should produce same hash
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}

	hash1 := BlurHash(img, 4, 3)
	hash2 := BlurHash(img, 4, 3)

	if hash1 != hash2 {
		t.Errorf("BlurHash should be deterministic: %q != %q", hash1, hash2)
	}
}

func TestSRGBToLinear(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{0, 0},
		{1, 1},
	}

	for _, tt := range tests {
		got := sRGBToLinear(tt.input)
		if got != tt.expected {
			t.Errorf("sRGBToLinear(%f) = %f, want %f", tt.input, got, tt.expected)
		}
	}
}

func TestLinearToSRGB(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{0, 0},
		{1, 1},
	}

	for _, tt := range tests {
		got := linearToSRGB(tt.input)
		diff := got - tt.expected
		if diff < -0.001 || diff > 0.001 {
			t.Errorf("linearToSRGB(%f) = %f, want %f", tt.input, got, tt.expected)
		}
	}
}

func BenchmarkBlurHash(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BlurHash(img, 4, 3)
	}
}
