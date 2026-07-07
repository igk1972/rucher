package sopsage

import (
	"crypto/rand"
	"fmt"

	"rucher/internal/age"
)

// version is written into the sops metadata block. It is informational; the
// wire format is stable across sops 3.x, and the sops CLI accepts it on decrypt.
const version = "3.9.4"

// KV is one plaintext entry. Encrypt preserves the given order, which fixes the
// MAC hash order — the sops CLI recomputes the MAC in file order, so any
// consistent order round-trips.
type KV struct {
	Key   string
	Value string
}

// Encrypt produces a SOPS+age YAML file: a fresh 32-byte data key wrapped to
// each recipient, every value AES-GCM-encrypted, and the MAC over all values.
// lastModified is the RFC3339 timestamp bound into the MAC (caller supplies it
// so the output is reproducible in tests).
func Encrypt(recipients []string, values []KV, lastModified string) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients")
	}
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, err
	}

	// Wrap the data key to each recipient — one armored age stanza each.
	stanzas := make([]ageStanza, 0, len(recipients))
	for _, rcpt := range recipients {
		sealed, err := age.Seal(rcpt, dataKey)
		if err != nil {
			return nil, fmt.Errorf("seal data key to %s: %w", rcpt, err)
		}
		stanzas = append(stanzas, ageStanza{Recipient: rcpt, Enc: string(sealed)})
	}

	pairs := make([]encPair, 0, len(values))
	macValues := make([][]byte, 0, len(values))
	for _, kv := range values {
		plain := []byte(kv.Value)
		enc, err := encryptScalar(plain, "str", dataKey, kv.Key+":")
		if err != nil {
			return nil, fmt.Errorf("encrypt %q: %w", kv.Key, err)
		}
		pairs = append(pairs, encPair{Key: kv.Key, Enc: enc})
		macValues = append(macValues, plain)
	}

	encMac, err := encryptMAC(computeMAC(macValues), dataKey, lastModified)
	if err != nil {
		return nil, err
	}
	meta := sopsMeta{
		Age:               stanzas,
		LastModified:      lastModified,
		Mac:               encMac,
		UnencryptedSuffix: "_unencrypted",
		Version:           version,
	}
	return emitEncryptedFile(pairs, meta)
}
