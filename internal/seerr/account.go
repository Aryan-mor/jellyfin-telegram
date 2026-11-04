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

// ReadLoginAccount returns the Jellyfin account made for Seerr, without making one when it is missing.
func ReadLoginAccount(root string) (user, password string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(root, "jellyfin.json"))
	if err != nil {
		return "", "", false
	}
	var saved loginFile
	if json.Unmarshal(raw, &saved) != nil || saved.User == "" || saved.Password == "" {
		return "", "", false
	}
	return saved.User, saved.Password, true
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
