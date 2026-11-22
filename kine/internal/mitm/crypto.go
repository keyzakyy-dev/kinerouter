package mitm

// sudo password at rest: aes-256-gcm, key = sha256(machineId + "kinerouter-mitm-pwd"),
// stored as "ivHex:tagHex:dataHex" inside settings.mitmSudoEncrypted.
// Mirrors deriveKey/encryptPassword/decryptPassword in manager.js.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const encryptSalt = "kinerouter-mitm-pwd"

// machineID is set by the gateway at startup (same value the JS build uses).
var machineID string

// SetMachineID supplies the machine identifier used to derive the password key.
func SetMachineID(id string) { machineID = id }

func deriveKey() []byte {
	sum := sha256.Sum256([]byte(machineID + encryptSalt))
	return sum[:]
}

// EncryptPassword seals plaintext for storage.
func EncryptPassword(plaintext string) string {
	block, err := aes.NewCipher(deriveKey())
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return ""
	}
	sealed := gcm.Seal(nil, iv, []byte(plaintext), nil)
	data, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	return hex.EncodeToString(iv) + ":" + hex.EncodeToString(tag) + ":" + hex.EncodeToString(data)
}

// DecryptPassword opens a stored value; anything malformed yields "".
func DecryptPassword(stored string) string {
	parts := strings.Split(stored, ":")
	if len(parts) != 3 {
		return ""
	}
	iv, err1 := hex.DecodeString(parts[0])
	tag, err2 := hex.DecodeString(parts[1])
	data, err3 := hex.DecodeString(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return ""
	}
	block, err := aes.NewCipher(deriveKey())
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	plain, err := gcm.Open(nil, iv, append(data, tag...), nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// LoadEncryptedPassword decrypts settings.mitmSudoEncrypted, "" when absent.
func LoadEncryptedPassword() string {
	if getSettings == nil {
		return ""
	}
	s, err := getSettings()
	if err != nil {
		return ""
	}
	stored, _ := s["mitmSudoEncrypted"].(string)
	if stored == "" {
		return ""
	}
	return DecryptPassword(stored)
}

// ClearEncryptedPassword forgets a stale password (wrong sudo password).
func ClearEncryptedPassword() {
	if updateSettings == nil {
		return
	}
	updateSettings(map[string]any{"mitmSudoEncrypted": nil})
}
