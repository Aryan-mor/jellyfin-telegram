package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PrepareDir creates the shared download tree and checks that this user can write it.
func PrepareDir(dir string) error {
	// The category folders are made here, not left to qBittorrent. It only creates one with its first
	// torrent, and until then Radarr and Sonarr report that the download folder does not exist.
	for _, sub := range []string{
		filepath.Join("torrents", "movies"), filepath.Join("torrents", "tv"),
		filepath.Join("media", "movies"), filepath.Join("media", "tv"),
	} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("cannot write in %s: %w", dir, err)
	}
	return os.Remove(probe)
}

func bind(host, container string) string {
	host = filepath.Clean(host)
	if runtime.GOOS == "windows" {
		host = strings.ReplaceAll(host, `\`, `/`)
	}
	return host + ":" + container
}

// qbitConfig is the file qBittorrent reads on first start. The password is already hashed.
func qbitConfig(username, hash string) string {
	return fmt.Sprintf(`[BitTorrent]
Session\DefaultSavePath=/data/torrents
Session\Port=%d
Session\MaxRatioEnabled=true
Session\MaxRatio=0
Session\MaxRatioAction=0

[Preferences]
WebUI\Username=%s
WebUI\Password_PBKDF2="%s"
WebUI\Port=8080
WebUI\Address=*
WebUI\LocalHostAuth=false
WebUI\AuthSubnetWhitelistEnabled=false
WebUI\CSRFProtection=false
WebUI\ClickjackingProtection=false
WebUI\HostHeaderValidation=false
WebUI\SecureCookie=false

[LegalNotice]
Accepted=true
`, PeerPort, username, hash)
}

// excludedFiles keeps executables out of an unattended DHT index.
const excludedFiles = `*.exe
*.bat
*.cmd
*.com
*.scr
*.pif
*.lnk`

// WriteQbitConfig writes the config once. A later start keeps the file, so the password does not rotate.
func WriteQbitConfig(dir, username, password string) error {
	confDir := filepath.Join(dir, "qBittorrent")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(confDir, "qBittorrent.conf")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	hash, err := PasswordHash(password)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(qbitConfig(username, hash)), 0o644)
}
