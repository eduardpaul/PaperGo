package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var dummyPasswordHash []byte

func init() {
	var err error
	dummyPasswordHash, err = bcrypt.GenerateFromPassword([]byte("papergo-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
}

// HashPassword stores a local password. The result is a bcrypt hash.
func HashPassword(password string) (string, error) {
	if password == "" || len(password) > 72 {
		return "", errors.New("password must be 1 to 72 bytes")
	}
	raw, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// PasswordMatches reports whether password matches a stored bcrypt hash.
// A missing hash still spends the comparison so absent users are not faster.
func PasswordMatches(hash, password string) bool {
	if password == "" || len(password) > 72 {
		return false
	}
	stored := []byte(hash)
	if hash == "" {
		stored = dummyPasswordHash
	}
	ok := bcrypt.CompareHashAndPassword(stored, []byte(password)) == nil
	return hash != "" && ok
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
