package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

const passwordCharset = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// GeneratePassword returns a CSPRNG-generated, human-shareable access code.
func GeneratePassword(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = passwordCharset[int(b)%len(passwordCharset)]
	}
	return string(out), nil
}

// HashPassword salts and hashes a password for storage/comparison.
// The relay never persists a reversible plaintext secret.
func HashPassword(password string) (hash string, salt string, err error) {
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", "", fmt.Errorf("generate salt: %w", err)
	}
	salt = base64.RawStdEncoding.EncodeToString(saltBytes)
	hash = hashWithSalt(password, salt)
	return hash, salt, nil
}

// VerifyPassword does a constant-time comparison of password against the stored hash+salt.
func VerifyPassword(password, hash, salt string) bool {
	candidate := hashWithSalt(password, salt)
	return subtleConstantTimeEqual(candidate, hash)
}

func subtleConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hashWithSalt(password, salt string) string {
	sum := sha256.Sum256([]byte(salt + ":" + password))
	return base64.RawStdEncoding.EncodeToString(sum[:])
}
