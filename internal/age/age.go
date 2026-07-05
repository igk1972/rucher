// Package age wraps filippo.io/age for the node/compartment key operations B needs:
// generating identities, sealing (encrypting to a recipient) and unsealing.
package age

import (
	"bytes"
	"fmt"
	"io"

	"filippo.io/age"
	"filippo.io/age/armor"
)

func GenerateIdentity() (string, string, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", fmt.Errorf("generate age identity: %w", err)
	}
	return id.String(), id.Recipient().String(), nil
}

func RecipientFor(identity string) (string, error) {
	id, err := age.ParseX25519Identity(identity)
	if err != nil {
		return "", fmt.Errorf("parse identity: %w", err)
	}
	return id.Recipient().String(), nil
}

func Seal(recipient string, plaintext []byte) ([]byte, error) {
	return SealTo([]string{recipient}, plaintext)
}

// SealTo encrypts plaintext to every recipient so any one of their identities can
// unseal it: age writes a stanza per recipient. Used when a compartment lives on
// multiple nodes and its identity.age must be readable by each node's key.
func SealTo(recipients []string, plaintext []byte) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients to seal to")
	}
	rcpts := make([]age.Recipient, 0, len(recipients))
	for _, r := range recipients {
		rcpt, err := age.ParseX25519Recipient(r)
		if err != nil {
			return nil, fmt.Errorf("parse recipient: %w", err)
		}
		rcpts = append(rcpts, rcpt)
	}
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, rcpts...)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil { // flush the age stream
		return nil, err
	}
	if err := aw.Close(); err != nil { // finish the armor block
		return nil, err
	}
	return buf.Bytes(), nil
}

func Unseal(identity string, ciphertext []byte) ([]byte, error) {
	id, err := age.ParseX25519Identity(identity)
	if err != nil {
		return nil, fmt.Errorf("parse identity: %w", err)
	}
	ar := armor.NewReader(bytes.NewReader(ciphertext))
	r, err := age.Decrypt(ar, id)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	return io.ReadAll(r)
}
