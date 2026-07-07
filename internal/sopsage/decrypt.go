package sopsage

import (
	"bytes"
	"fmt"
	"io"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// Decrypt decrypts a SOPS+age YAML file into a flat key/value map. identityData
// is the contents of an age identity file (a bare AGE-SECRET-KEY line or a
// keygen file with comments — both are accepted). It recovers the data key via
// age, decrypts every value, and verifies the MAC before returning.
func Decrypt(identityData, sopsData []byte) (map[string]string, error) {
	pairs, meta, err := parseEncryptedFile(sopsData)
	if err != nil {
		return nil, err
	}
	ids, err := age.ParseIdentities(bytes.NewReader(identityData))
	if err != nil {
		return nil, fmt.Errorf("parse age identity: %w", err)
	}
	dataKey, err := recoverDataKey(ids, meta.Age)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(pairs))
	macValues := make([][]byte, 0, len(pairs))
	for _, p := range pairs {
		// SOPS binds each value to its path; for a flat map the AAD is "<key>:".
		plain, _, err := decryptScalar(p.Enc, dataKey, p.Key+":")
		if err != nil {
			return nil, fmt.Errorf("decrypt %q: %w", p.Key, err)
		}
		out[p.Key] = string(plain)
		macValues = append(macValues, plain)
	}
	if err := verifyMAC(meta.Mac, dataKey, meta.LastModified, computeMAC(macValues)); err != nil {
		return nil, err
	}
	return out, nil
}

// recoverDataKey returns the 32-byte data key from the first age stanza any of
// the identities can unwrap.
func recoverDataKey(ids []age.Identity, stanzas []ageStanza) ([]byte, error) {
	for _, st := range stanzas {
		ar := armor.NewReader(bytes.NewReader([]byte(st.Enc)))
		r, err := age.Decrypt(ar, ids...)
		if err != nil {
			continue
		}
		key, err := io.ReadAll(r)
		if err == nil && len(key) == 32 {
			return key, nil
		}
	}
	return nil, fmt.Errorf("no age identity could unwrap the data key")
}
