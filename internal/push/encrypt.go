// Package push implements Web Push delivery: RFC 8291 (aes128gcm) message
// encryption, RFC 8292 (VAPID) request signing, and Mastodon-compatible
// notification payloads.
package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const (
	// encryptionRecordSize is the rs value written into the aes128gcm
	// content-coding header. A single record carries the whole payload.
	encryptionRecordSize = 4096

	saltLength      = 16
	publicKeyLength = 65 // uncompressed P-256 point
	gcmTagLength    = 16
)

// ErrPayloadTooLarge is returned when the plaintext does not fit in a single
// aes128gcm record.
var ErrPayloadTooLarge = errors.New("push payload exceeds maximum size")

// Encrypt encrypts plaintext per RFC 8291 (aes128gcm) for a subscription's
// P-256 public key (uncompressed point) and 16-byte auth secret. The result
// is the full content-coding body: header, key id, and ciphertext.
func Encrypt(plaintext, uaPublic, authSecret []byte) ([]byte, error) {
	asPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ephemeral key: %w", err)
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}
	return encrypt(plaintext, uaPublic, authSecret, salt, asPrivate)
}

// encrypt is the deterministic core of Encrypt, split out so the RFC 8291
// test vector (fixed salt and server key) can be verified exactly.
func encrypt(plaintext, uaPublic, authSecret, salt []byte, asPrivate *ecdh.PrivateKey) ([]byte, error) {
	// One record: plaintext + delimiter byte + GCM tag must fit in rs.
	if len(plaintext)+1+gcmTagLength > encryptionRecordSize {
		return nil, ErrPayloadTooLarge
	}
	if len(salt) != saltLength {
		return nil, fmt.Errorf("salt must be %d bytes", saltLength)
	}
	if len(authSecret) == 0 {
		return nil, errors.New("auth secret is empty")
	}

	uaKey, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("invalid subscription public key: %w", err)
	}
	sharedSecret, err := asPrivate.ECDH(uaKey)
	if err != nil {
		return nil, fmt.Errorf("computing shared secret: %w", err)
	}
	asPublic := asPrivate.PublicKey().Bytes()

	// IKM = HKDF(salt=auth_secret, ikm=ecdh_secret,
	//            info="WebPush: info" || 0x00 || ua_public || as_public)
	keyInfo := make([]byte, 0, 14+len(uaPublic)+len(asPublic))
	keyInfo = append(keyInfo, []byte("WebPush: info\x00")...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	ikm, err := hkdfDerive(authSecret, sharedSecret, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdfDerive(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdfDerive(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// aes128gcm header: salt(16) | rs(4) | idlen(1) | keyid(as_public, 65)
	header := make([]byte, 0, saltLength+4+1+publicKeyLength)
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, encryptionRecordSize)
	header = append(header, byte(publicKeyLength))
	header = append(header, asPublic...)

	// Last (only) record: plaintext || 0x02 delimiter, no padding.
	record := make([]byte, 0, len(plaintext)+1)
	record = append(record, plaintext...)
	record = append(record, 0x02)

	return gcm.Seal(header, nonce, record, nil), nil
}

func hkdfDerive(salt, ikm, info []byte, length int) ([]byte, error) {
	out := make([]byte, length)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, info), out); err != nil {
		return nil, fmt.Errorf("hkdf: %w", err)
	}
	return out, nil
}

// decodeBase64 accepts URL-safe or standard base64, with or without padding,
// matching the lenient decoding browsers and Mastodon clients rely on.
func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("empty base64 value")
	}
	trimmed := strings.TrimRight(value, "=")
	if strings.ContainsAny(trimmed, "+/") {
		return base64.RawStdEncoding.DecodeString(trimmed)
	}
	return base64.RawURLEncoding.DecodeString(trimmed)
}
