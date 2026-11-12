package integration

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

type Secrets struct {
	key  []byte
	aead cipher.AEAD
}

func NewSecrets(key []byte) (*Secrets, error) {
	if len(key) != 32 {
		return nil, errors.New("integration key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Secrets{append([]byte(nil), key...), aead}, nil
}

// LoadSecrets creates a private deployment key once. Never silently replaces a
// missing key when configured integrations already exist (checked by the caller).
func LoadSecrets(path string, allowCreate bool) (*Secrets, error) {
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) && allowCreate {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(e) {
			key, err = os.ReadFile(path)
		} else if e != nil {
			return nil, e
		} else {
			_, err = f.Write(key)
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return nil, errors.New("read integration key; restore the deployment key with the database")
	}
	return NewSecrets(key)
}

// Purpose names what a ciphertext configures. It is part of the associated
// data, with the record it belongs to, so a ciphertext copied to another
// purpose or record does not decrypt.
type Purpose string

const (
	// PurposeListIntegration is a list integration's configuration, bound
	// to its list.
	PurposeListIntegration Purpose = "list-integration"
	// PurposeConnection is a connection's target and credentials, bound to
	// the connection.
	PurposeConnection Purpose = "connection"
)

func associated(purpose Purpose, owner string) []byte {
	return []byte("papergo:" + string(purpose) + ":" + owner)
}

// Encrypt seals raw for purpose and the record owner names.
func (s *Secrets) Encrypt(purpose Purpose, owner, raw string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(raw), associated(purpose, owner)), nil
}

// Decrypt opens what Encrypt sealed for the same purpose and owner.
func (s *Secrets) Decrypt(purpose Purpose, owner string, b []byte) (string, error) {
	n := s.aead.NonceSize()
	if len(b) < n {
		return "", errors.New("the configuration cannot be decrypted")
	}
	plain, err := s.aead.Open(nil, b[:n], b[n:], associated(purpose, owner))
	if err != nil {
		return "", errors.New("the configuration cannot be decrypted")
	}
	return string(plain), nil
}
func (s *Secrets) Hash(raw string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(raw))
	return hex.EncodeToString(h.Sum(nil))
}
