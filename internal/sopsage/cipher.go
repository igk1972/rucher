// Package sopsage decrypts and encrypts SOPS files that use the age backend,
// entirely in-process — no external `sops` binary and, deliberately, no getsops
// import (which would drag in every cloud KMS SDK). It implements the SOPS v3
// wire format for flat YAML maps (key: value), which is all cadres use. The
// data key is wrapped/unwrapped with filippo.io/age via internal/age.
package sopsage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
)

// SOPS uses a 32-byte GCM nonce (not the 12-byte default) and a 16-byte tag.
const (
	nonceSize = 32
	tagSize   = 16
)

// encRE parses an `ENC[AES256_GCM,data:...,iv:...,tag:...,type:...]` value.
var encRE = regexp.MustCompile(`^ENC\[AES256_GCM,data:(.*),iv:(.*),tag:(.*),type:(.*)\]$`)

// encryptScalar encrypts plaintext to a SOPS `ENC[...]` string. additionalData
// binds the value to its position (SOPS uses the path with a trailing colon).
// A fresh 32-byte nonce is generated per call.
func encryptScalar(plaintext []byte, typ string, key []byte, additionalData string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	iv := make([]byte, nonceSize)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, iv, plaintext, []byte(additionalData))
	// gcm.Seal returns ciphertext||tag; SOPS stores the tag separately.
	data, tag := sealed[:len(sealed)-tagSize], sealed[len(sealed)-tagSize:]
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("ENC[AES256_GCM,data:%s,iv:%s,tag:%s,type:%s]",
		b64(data), b64(iv), b64(tag), typ), nil
}

// decryptScalar reverses encryptScalar, returning the plaintext bytes and the
// SOPS type tag. additionalData must match what was used to encrypt.
func decryptScalar(enc string, key []byte, additionalData string) (plaintext []byte, typ string, err error) {
	m := encRE.FindStringSubmatch(enc)
	if m == nil {
		return nil, "", fmt.Errorf("not a SOPS-encrypted value: %q", enc)
	}
	data, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		return nil, "", fmt.Errorf("decode data: %w", err)
	}
	iv, err := base64.StdEncoding.DecodeString(m[2])
	if err != nil {
		return nil, "", fmt.Errorf("decode iv: %w", err)
	}
	tag, err := base64.StdEncoding.DecodeString(m[3])
	if err != nil {
		return nil, "", fmt.Errorf("decode tag: %w", err)
	}
	typ = m[4]
	gcm, err := newGCM(key)
	if err != nil {
		return nil, "", err
	}
	// Recombine into the ciphertext||tag layout gcm.Open expects.
	plaintext, err = gcm.Open(nil, iv, append(data, tag...), []byte(additionalData))
	if err != nil {
		return nil, "", fmt.Errorf("gcm open: %w", err)
	}
	return plaintext, typ, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	return cipher.NewGCMWithNonceSize(block, nonceSize)
}
