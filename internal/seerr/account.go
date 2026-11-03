package seerr

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// LoginUser is the Jellyfin account created for Seerr when this program did not install Jellyfin.
const LoginUser = "seerr"

type loginFile struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// LoginAccount returns the Jellyfin user and password Seerr should keep using.
func LoginAccount(root string) (string, string, error) {
	path := filepath.Join(root, "jellyfin.json")
	raw, err := os.ReadFile(path)
	if err == nil {
		var saved loginFile
		if err := json.Unmarshal(raw, &saved); err != nil {
			return "", "", err
		}
		if saved.User != "" && saved.Password != "" {
			return saved.User, saved.Password, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	saved := loginFile{User: LoginUser, Password: hex.EncodeToString(buf)}
	raw, err = json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return "", "", err
	}
	return saved.User, saved.Password, nil
}
