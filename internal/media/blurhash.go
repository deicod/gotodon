// Package media provides blurhash encoding for images.
// BlurHash is a compact representation of a placeholder for an image.
// See: https://blurha.sh/
package media

import (
	"bytes"
	"image"
	"math"
	"strings"
)

// BlurHash encodes an image to a blurhash string
func BlurHash(img image.Image, xComponents, yComponents int) string {
	if xComponents < 1 || xComponents > 9 || yComponents < 1 || yComponents > 9 {
		return ""
	}

	bounds := img.Bounds()
	width := bounds.Max.X - bounds.Min.X
	height := bounds.Max.Y - bounds.Min.Y

	// Get pixel data
	pixels := getPixels(img, width, height)

	// Calculate DC value (average)
	dc := calculateDC(pixels, width, height)

	// Calculate AC values
	ac := make([]float64, xComponents*yComponents*3-3)
	acIndex := 0
	maxAC := float64(0)

	for j := 0; j < yComponents; j++ {
		for i := 0; i < xComponents; i++ {
			if i == 0 && j == 0 {
				continue
			}

			r, g, b := calculateAC(pixels, width, height, i, j)
			ac[acIndex] = r
			ac[acIndex+1] = g
			ac[acIndex+2] = b

			maxAC = math.Max(maxAC, math.Abs(r))
			maxAC = math.Max(maxAC, math.Abs(g))
			maxAC = math.Max(maxAC, math.Abs(b))

			acIndex += 3
		}
	}

	// Encode
	var result strings.Builder

	// Size flag
	sizeFlag := (xComponents - 1) + (yComponents-1)*9
	result.WriteByte(base83Chars[sizeFlag])

	// Quantized max value
	var quantisedMaxValue int
	if len(ac) > 0 && maxAC > 0 {
		quantisedMaxValue = int(math.Max(0, math.Min(82, math.Floor(maxAC*166-0.5))))
	}
	result.WriteByte(base83Chars[quantisedMaxValue])

	// DC value
	dcValue := encodeDC(dc[0], dc[1], dc[2])
	result.WriteString(encodeBase83(dcValue, 4))

	// AC values
	if quantisedMaxValue > 0 {
		for i := 0; i < len(ac); i += 3 {
			acValue := encodeAC(ac[i], ac[i+1], ac[i+2], float64(quantisedMaxValue+1)/166)
			result.WriteByte(base83Chars[acValue])
		}
	} else {
		for i := 0; i < len(ac)/3; i++ {
			result.WriteByte(base83Chars[0])
		}
	}

	return result.String()
}

// BlurHashFromBytes encodes image bytes to a blurhash string
func BlurHashFromBytes(data []byte, xComponents, yComponents int) (string, error) {
	reader := bytes.NewReader(data)
	img, _, err := image.Decode(reader)
	if err != nil {
		return "", err
	}
	return BlurHash(img, xComponents, yComponents), nil
}

const base83Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~"

func getPixels(img image.Image, width, height int) []float64 {
	pixels := make([]float64, width*height*3)
	bounds := img.Bounds()

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			idx := (y*width + x) * 3
			pixels[idx] = sRGBToLinear(float64(r) / 65535)
			pixels[idx+1] = sRGBToLinear(float64(g) / 65535)
			pixels[idx+2] = sRGBToLinear(float64(b) / 65535)
		}
	}

	return pixels
}

func calculateDC(pixels []float64, width, height int) []float64 {
	var r, g, b float64

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 3
			r += pixels[idx]
			g += pixels[idx+1]
			b += pixels[idx+2]
		}
	}

	scale := 1.0 / float64(width*height)
	return []float64{r * scale, g * scale, b * scale}
}

func calculateAC(pixels []float64, width, height, xComp, yComp int) (float64, float64, float64) {
	var r, g, b float64

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			basis := math.Cos(math.Pi*float64(xComp)*float64(x)/float64(width)) *
				math.Cos(math.Pi*float64(yComp)*float64(y)/float64(height))

			idx := (y*width + x) * 3
			r += basis * pixels[idx]
			g += basis * pixels[idx+1]
			b += basis * pixels[idx+2]
		}
	}

	scale := 2.0 / float64(width*height)
	return r * scale, g * scale, b * scale
}

func sRGBToLinear(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

func linearToSRGB(value float64) float64 {
	if value <= 0.0031308 {
		return value * 12.92
	}
	return 1.055*math.Pow(value, 1/2.4) - 0.055
}

func encodeDC(r, g, b float64) int {
	roundedR := int(math.Max(0, math.Min(255, math.Floor(linearToSRGB(r)*255+0.5))))
	roundedG := int(math.Max(0, math.Min(255, math.Floor(linearToSRGB(g)*255+0.5))))
	roundedB := int(math.Max(0, math.Min(255, math.Floor(linearToSRGB(b)*255+0.5))))
	return (roundedR << 16) + (roundedG << 8) + roundedB
}

func encodeAC(r, g, b, maxAC float64) int {
	quantR := int(math.Max(0, math.Min(18, math.Floor(signPow(r/maxAC, 0.5)*9+9.5))))
	quantG := int(math.Max(0, math.Min(18, math.Floor(signPow(g/maxAC, 0.5)*9+9.5))))
	quantB := int(math.Max(0, math.Min(18, math.Floor(signPow(b/maxAC, 0.5)*9+9.5))))
	value := quantR*19*19 + quantG*19 + quantB
	// Ensure value is within base83 range
	if value > 82 {
		value = value % 83
	}
	return value
}

func signPow(value, exp float64) float64 {
	if value < 0 {
		return -math.Pow(-value, exp)
	}
	return math.Pow(value, exp)
}

func encodeBase83(value, length int) string {
	result := make([]byte, length)
	for i := length - 1; i >= 0; i-- {
		result[i] = base83Chars[value%83]
		value /= 83
	}
	return string(result)
}
