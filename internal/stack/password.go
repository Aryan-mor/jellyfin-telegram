package stack

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
)

// PasswordHash is the PBKDF2 string qBittorrent stores in qBittorrent.conf.
// SHA-512, 100000 iterations, 16 byte salt, 64 byte key, both base64, joined by a colon.
func PasswordHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha512.New, password, salt, 100000, 64)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("@ByteArray(%s:%s)", base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(key)), nil
}
