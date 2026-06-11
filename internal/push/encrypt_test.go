package push

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"testing"
)

func b64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := decodeBase64(value)
	if err != nil {
		t.Fatalf("decoding %q: %v", value, err)
	}
	return decoded
}

// TestEncryptRFC8291Vector verifies the full test vector from RFC 8291
// section 5.
func TestEncryptRFC8291Vector(t *testing.T) {
	plaintext := []byte("When I grow up, I want to be a watermelon")
	uaPublic := b64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	authSecret := b64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := b64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	asPrivate, err := ecdh.P256().NewPrivateKey(b64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatalf("loading test private key: %v", err)
	}

	expected := b64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN")

	got, err := encrypt(plaintext, uaPublic, authSecret, salt, asPrivate)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Errorf("ciphertext mismatch\n got: %s\nwant: %s",
			base64.RawURLEncoding.EncodeToString(got),
			base64.RawURLEncoding.EncodeToString(expected))
	}
}

// decryptAES128GCM reverses Encrypt as a user agent would, for round-trip
// testing.
func decryptAES128GCM(t *testing.T, body []byte, uaPrivate *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	if len(body) < saltLength+4+1+publicKeyLength {
		t.Fatalf("body too short: %d bytes", len(body))
	}
	salt := body[:saltLength]
	rs := binary.BigEndian.Uint32(body[saltLength : saltLength+4])
	if rs != encryptionRecordSize {
		t.Fatalf("unexpected record size %d", rs)
	}
	idLen := int(body[saltLength+4])
	if idLen != publicKeyLength {
		t.Fatalf("unexpected key id length %d", idLen)
	}
	asPublicBytes := body[saltLength+5 : saltLength+5+idLen]
	ciphertext := body[saltLength+5+idLen:]

	asPublic, err := ecdh.P256().NewPublicKey(asPublicBytes)
	if err != nil {
		t.Fatalf("parsing server public key: %v", err)
	}
	sharedSecret, err := uaPrivate.ECDH(asPublic)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}

	uaPublic := uaPrivate.PublicKey().Bytes()
	keyInfo := append([]byte("WebPush: info\x00"), append(uaPublic, asPublicBytes...)...)
	ikm, err := hkdfDerive(authSecret, sharedSecret, keyInfo, 32)
	if err != nil {
		t.Fatalf("hkdf ikm: %v", err)
	}
	cek, err := hkdfDerive(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		t.Fatalf("hkdf cek: %v", err)
	}
	nonce, err := hkdfDerive(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		t.Fatalf("hkdf nonce: %v", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	record, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypting: %v", err)
	}

	// Strip padding: trailing zeros then the 0x02 delimiter.
	end := len(record)
	for end > 0 && record[end-1] == 0 {
		end--
	}
	if end == 0 || record[end-1] != 0x02 {
		t.Fatalf("missing record delimiter")
	}
	return record[:end-1]
}

func TestEncryptRoundTrip(t *testing.T) {
	uaPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("generating auth secret: %v", err)
	}

	plaintext := []byte(`{"title":"You were mentioned by alice","body":"hello"}`)
	body, err := Encrypt(plaintext, uaPrivate.PublicKey().Bytes(), authSecret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got := decryptAES128GCM(t, body, uaPrivate, authSecret)
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestEncryptRejectsOversizedPayload(t *testing.T) {
	uaPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	big := make([]byte, encryptionRecordSize)
	if _, err := Encrypt(big, uaPrivate.PublicKey().Bytes(), authSecret); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestEncryptRejectsInvalidKeys(t *testing.T) {
	if _, err := Encrypt([]byte("hi"), []byte{0x04, 0x01}, make([]byte, 16)); err == nil {
		t.Error("expected error for invalid public key")
	}
	uaPrivate, _ := ecdh.P256().GenerateKey(rand.Reader)
	if _, err := Encrypt([]byte("hi"), uaPrivate.PublicKey().Bytes(), nil); err == nil {
		t.Error("expected error for empty auth secret")
	}
}

func TestDecodeBase64Variants(t *testing.T) {
	// URL-safe and standard alphabets, with and without padding, must all
	// decode to the same bytes.
	want := b64(t, "_-8_")
	for _, input := range []string{"_-8_", "/+8/", "/+8/=", "_-8_=="} {
		got, err := decodeBase64(input)
		if err != nil {
			t.Errorf("decodeBase64(%q) unexpected error: %v", input, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("decodeBase64(%q) = %x, want %x", input, got, want)
		}
	}
	if _, err := decodeBase64(""); err == nil {
		t.Error("expected error for empty input")
	}
}
