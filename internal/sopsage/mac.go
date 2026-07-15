// SPDX-License-Identifier: AGPL-3.0-or-later

package sopsage

import (
	"crypto/sha512"
	"crypto/subtle"
	"fmt"
)

// computeMAC hashes every encrypted plaintext value in tree-walk (file) order
// with SHA-512 and returns the upper-hex digest, matching SOPS with
// mac_only_encrypted=false (no init bytes; every value contributes). For a flat
// map that means all values, in the order the keys appear in the file.
func computeMAC(values [][]byte) string {
	h := sha512.New()
	for _, v := range values {
		h.Write(v)
	}
	return fmt.Sprintf("%X", h.Sum(nil))
}

// encryptMAC stores the hex MAC as a SOPS `ENC[...,type:str]` string. SOPS binds
// the MAC to the file's lastmodified timestamp by using it as the AES-GCM
// additional data, so a rolled-back timestamp fails authentication.
func encryptMAC(macHex string, key []byte, lastModified string) (string, error) {
	return encryptScalar([]byte(macHex), "str", key, lastModified)
}

// verifyMAC decrypts the stored MAC (AAD = lastModified) and checks it against
// the recomputed digest.
func verifyMAC(stored string, key []byte, lastModified, computed string) error {
	plain, _, err := decryptScalar(stored, key, lastModified)
	if err != nil {
		return fmt.Errorf("decrypt mac: %w", err)
	}
	if subtle.ConstantTimeCompare(plain, []byte(computed)) != 1 {
		return fmt.Errorf("MAC mismatch: file integrity check failed")
	}
	return nil
}
