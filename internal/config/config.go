// Package config loads settings from the environment and an optional .env file.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
)

// Config is the full service configuration.
type Config struct {
	APIID          int
	APIHash        string
	ChannelID      int64 // bare id, without the -100 prefix
	BotTokens      []string
	BotConcurrency int

	GatewayListen    string
	GatewayPublicURL string
	GatewayToken     string
	ReadaheadBlocks  int

	LibraryDir string
	DataDir    string
	CacheBytes int64

	SyncInterval time.Duration

	JellyfinURL    string
	JellyfinAPIKey string

	// SubtitleLanguage is a normalized language code. Empty turns subtitle handling off.
	SubtitleLanguage string
	SubtitlesOnline  bool
	FFprobePath      string

	// ExtractBytesPerSecond limits background subtitle extraction. Zero turns it off.
	ExtractBytesPerSecond int64
	// JellyfinDataDir is Jellyfin's data folder as seen from this service. Empty asks Jellyfin.
	JellyfinDataDir string
}

// JellyfinEnabled reports whether automatic refresh and probing are configured.
func (c Config) JellyfinEnabled() bool { return c.JellyfinURL != "" && c.JellyfinAPIKey != "" }

// Load reads envPath if it exists, then the process environment. Real environment
// variables win over the file. Errors name variables and never include their values.
func Load(envPath string) (Config, error) {
	if err := godotenv.Load(envPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read %s: %w", envPath, err)
	}

	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	c := Config{
		APIHash:          get("TELEGRAM_API_HASH", ""),
		GatewayListen:    get("GATEWAY_LISTEN", "0.0.0.0:8097"),
		GatewayPublicURL: strings.TrimRight(get("GATEWAY_PUBLIC_URL", ""), "/"),
		GatewayToken:     get("GATEWAY_TOKEN", ""),
		LibraryDir:       get("LIBRARY_DIR", "./library"),
		DataDir:          get("DATA_DIR", "./data"),
		JellyfinURL:      strings.TrimRight(get("JELLYFIN_URL", ""), "/"),
		JellyfinAPIKey:   get("JELLYFIN_API_KEY", ""),
		SubtitlesOnline:  !strings.EqualFold(get("SUBTITLES_ONLINE", "true"), "false"),
		FFprobePath:      get("FFPROBE_PATH", ""),
	}

	if c.APIHash == "" {
		fail("TELEGRAM_API_HASH is required")
	}
	c.APIID = getInt("TELEGRAM_API_ID", 0, fail)
	if c.APIID <= 0 {
		fail("TELEGRAM_API_ID is required")
	}

	id, err := ParseChannelID(get("TELEGRAM_CHANNEL_ID", ""))
	if err != nil {
		fail("TELEGRAM_CHANNEL_ID: %v", err)
	}
	c.ChannelID = id

	c.BotTokens, err = parseTokens(get("TELEGRAM_BOT_TOKENS", ""))
	if err != nil {
		fail("TELEGRAM_BOT_TOKENS: %v", err)
	}

	c.BotConcurrency = getInt("TELEGRAM_BOT_CONCURRENCY", 4, fail)
	if c.BotConcurrency < 1 {
		fail("TELEGRAM_BOT_CONCURRENCY must be at least 1")
	}
	c.ReadaheadBlocks = getInt("GATEWAY_READAHEAD_BLOCKS", 8, fail)
	if c.ReadaheadBlocks < 0 {
		fail("GATEWAY_READAHEAD_BLOCKS must not be negative")
	}
	gb := getInt("CACHE_MAX_GB", 4, fail)
	if gb < 0 {
		fail("CACHE_MAX_GB must not be negative")
	}
	c.CacheBytes = int64(gb) << 30
	sec := getInt("SYNC_INTERVAL_SECONDS", 60, fail)
	if sec < 5 {
		fail("SYNC_INTERVAL_SECONDS must be at least 5")
	}
	c.SyncInterval = time.Duration(sec) * time.Second

	if c.GatewayPublicURL != "" {
		u, err := url.Parse(c.GatewayPublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fail("GATEWAY_PUBLIC_URL must be an http or https URL")
		}
	}
	c.JellyfinDataDir = get("JELLYFIN_DATA_DIR", "")
	mbps := getInt("SUBTITLE_EXTRACT_MB_PER_SEC", 3, fail)
	if mbps < 0 {
		fail("SUBTITLE_EXTRACT_MB_PER_SEC must not be negative")
	}
	c.ExtractBytesPerSecond = int64(mbps) * 1000 * 1000
	if raw := get("SUBTITLE_LANGUAGE", ""); raw != "" {
		c.SubtitleLanguage = lang.Normalize(raw)
		if c.SubtitleLanguage == "" {
			fail("SUBTITLE_LANGUAGE must be a language code such as en, de, fa or ja")
		}
	}
	if (c.JellyfinURL == "") != (c.JellyfinAPIKey == "") {
		fail("set both JELLYFIN_URL and JELLYFIN_API_KEY, or neither")
	}
	return c, errors.Join(errs...)
}

// ValidateServe adds the checks that only matter when running the gateway.
func (c Config) ValidateServe() error {
	if c.GatewayPublicURL == "" {
		return errors.New("GATEWAY_PUBLIC_URL is required")
	}
	return nil
}

// ParseChannelID accepts -100xxxxxxxxxx or the bare id and returns the bare id.
func ParseChannelID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("required")
	}
	s = strings.TrimPrefix(s, "-100")
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("must look like -1001234567890")
	}
	return id, nil
}

// BotID returns the numeric id at the start of a bot token.
func BotID(token string) (int64, error) {
	head, _, ok := strings.Cut(token, ":")
	if !ok {
		return 0, errors.New("token has no colon")
	}
	id, err := strconv.ParseInt(head, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("token does not start with a bot id")
	}
	return id, nil
}

// EnsureGatewayToken returns GATEWAY_TOKEN, or a persisted random one when unset.
func (c Config) EnsureGatewayToken() (string, error) {
	if c.GatewayToken != "" {
		return c.GatewayToken, nil
	}
	path := filepath.Join(c.DataDir, "gateway.token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return "", err
	}
	return token, os.WriteFile(path, []byte(token+"\n"), 0o600)
}

func parseTokens(s string) ([]string, error) {
	var out []string
	seen := map[int64]bool{}
	for i, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		id, err := BotID(t)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %v", i+1, err)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one bot token is required")
	}
	return out, nil
}

func get(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int, fail func(string, ...any)) int {
	v := get(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fail("%s must be a number", key)
		return def
	}
	return n
}
