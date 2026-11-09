// Package config loads settings from the environment and an optional .env file.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/lang"
)

// Config is the full service configuration.
type Config struct {
	APIID     int
	APIHash   string
	ChannelID int64 // bare id, without the -100 prefix
	// HomeChannelID is the personal Home Videos and Photos channel. Zero leaves that library off.
	HomeChannelID  int64
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

	// SubtitleLanguage is the preferred language, the first of SubtitleLanguages. Empty turns
	// subtitle handling off.
	SubtitleLanguage  string
	SubtitleLanguages []string
	// UILanguage is the language of the setup page and the panel: en, fa, de or tr.
	// Empty leaves the choice to the browser.
	UILanguage string
	SubtitlesOnline   bool
	FFprobePath       string

	// ExtractBytesPerSecond limits background subtitle extraction. Zero turns it off.
	ExtractBytesPerSecond int64
	// JellyfinDataDir is Jellyfin's data folder as seen from this service. Empty asks Jellyfin.
	JellyfinDataDir string
	// JellyfinManaged means this program installs and runs Jellyfin itself.
	JellyfinManaged bool
	// SeerrManaged means this program runs Seerr against the configured Jellyfin.
	// It is off until SEERR_MANAGED=true, and works with a Jellyfin you already run too.
	SeerrManaged bool
	// FladderManaged means this program runs the Fladder web client, with Seerr filled in when
	// Seerr is on. It is off until FLADDER_MANAGED=true.
	FladderManaged bool

	// LocalMediaDirs are folders of videos already on this computer. The panel lists them and uploads
	// the ones a person chooses. Nothing is uploaded or deleted without that choice.
	LocalMediaDirs []string
	// HomeMediaDirs are folders synced only into the personal channel, and only when a person asks.
	HomeMediaDirs []string

	// DownloadsEnabled turns on Radarr, Sonarr, qBittorrent, Bitmagnet and the channel uploader.
	// It needs Seerr, stays off until a person turns it on, and only works once a healthy admin
	// session exists.
	DownloadsEnabled bool
	// DownloadsDir is the bind-mounted volume shared by qBittorrent, Radarr and Sonarr.
	// Empty resolves to DATA_DIR/downloads.
	DownloadsDir string
	// DownloadsMinFreeGB pauses qBittorrent when the volume has less than this much free.
	DownloadsMinFreeGB int
	// DownloadsSearchEveryMinutes is how often Radarr and Sonarr look again for what is still missing.
	// Zero turns the repeat off.
	DownloadsSearchEveryMinutes int
	// BitmagnetDBMaxGB stops the DHT crawler once Postgres reaches this size. Search stays on.
	BitmagnetDBMaxGB int

	// Upload limits apply to each user session. Zero MB/s means no speed cap.
	// UploadHours is empty for all day, or a window such as 01-08.
	UploadMBPerSec        int
	UploadRestMinutes     int
	UploadLongRestEveryGB int
	UploadLongRestMinutes int
	UploadDailyGB         int
	UploadHours           string
	// SignedLibrary turns on the shared library: new files are reposted with a signed caption that
	// other installations can read. Off keeps the channel as it always was.
	SignedLibrary bool
	// TelegramSearch looks up requested titles on Telegram with the signed-in account and copies a
	// file it finds into the library. On unless TELEGRAM_SEARCH=false.
	TelegramSearch bool
	// SearchMinMinutes and SearchMinMB are the smallest running time and size of a file the search
	// accepts. SearchRuntimeTolerancePct is how far a film may differ from TMDB's runtime, in
	// percent; zero turns that check off.
	SearchMinMinutes          int
	SearchMinMB               int
	SearchRuntimeTolerancePct int
	// SearchPromoteAt is how many verified videos in a channel, seen when the first file is found,
	// make it a direct file channel. SearchMaxDiscovered caps how many the search may add
	// (0 turns promotion off). SearchDenyAfter is how many failed files put a channel or a signer
	// key on the denylist.
	SearchPromoteAt     int
	SearchMaxDiscovered int
	SearchDenyAfter     int
	// HashtagIndexBot is the username of the hashtag index bot (telegram-hashtag-index). It defaults
	// to tgHashtagIndexBot and turns on when a Telegram account is signed in. "off" turns it off.
	HashtagIndexBot string
	// TMDBAPIKey is a TMDB v3 key or v4 read token. Only the trending source uses it.
	TMDBAPIKey string
	// TrendingSearch looks up TMDB's trending titles once a day. Off until TRENDING_SEARCH=true,
	// and it needs TMDB_API_KEY.
	TrendingSearch bool
	// TrendingAllowAdult lets adult-flagged and erotic titles through the trending filter.
	TrendingAllowAdult bool
	// TrendingGenres and TrendingExcludeGenres are comma separated TMDB genre names. An empty
	// include list means the popular genres.
	TrendingGenres        string
	TrendingExcludeGenres string
	// TrendingMaxPerDay caps the trending titles searched per day.
	TrendingMaxPerDay int
	// InboxDeleteOriginals removes an inbox file after it has been copied to Ready.
	InboxDeleteOriginals bool
}

// DefaultHashtagIndexBot is the public hashtag index. A signed-in account uses it unless
// HASHTAG_INDEX_BOT is off, false, none, or 0.
const DefaultHashtagIndexBot = "tgHashtagIndexBot"

func hashtagIndexBot(name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	switch strings.ToLower(name) {
	case "", "off", "false", "none", "0", "-":
		return ""
	}
	return name
}

// DefaultAPIID and DefaultAPIHash are the Telegram app this build ships with, set at build time with
// -ldflags "-X .../internal/config.DefaultAPIID=... -X .../internal/config.DefaultAPIHash=...".
// The automatic setup signs in with them unless the person brings their own. They stay empty in the
// source tree, so a build without them asks for the person's own api_id.
var (
	DefaultAPIID   string
	DefaultAPIHash string
)

// NormalizeUILanguage accepts the panel and setup languages.
func NormalizeUILanguage(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "en", "fa", "de", "tr":
		return strings.ToLower(strings.TrimSpace(raw)), true
	default:
		return "", false
	}
}

// DefaultAPI returns the built-in Telegram app, and false when this build has none.
func DefaultAPI() (id int, hash string, ok bool) {
	n, err := strconv.Atoi(strings.TrimSpace(DefaultAPIID))
	hash = strings.TrimSpace(DefaultAPIHash)
	if err != nil || n <= 0 || hash == "" {
		return 0, "", false
	}
	return n, hash, true
}

// NeedsSetup reports that no Telegram details were given at all, as on a first run.
func (c Config) NeedsSetup() bool {
	return c.GatewayListen != "" && c.APIID == 0 && c.APIHash == "" && c.ChannelID == 0 && len(c.BotTokens) == 0
}

// LoopbackURL is how this machine reaches the service. Jellyfin reads .strm files through it when
// it runs here.
func (c Config) LoopbackURL() string {
	_, port, err := net.SplitHostPort(c.GatewayListen)
	if err != nil {
		port = "8097"
	}
	return "http://127.0.0.1:" + port
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
	if raw := strings.TrimSpace(get("HOME_CHANNEL_ID", "")); raw != "" {
		home, err := ParseChannelID(raw)
		if err != nil {
			fail("HOME_CHANNEL_ID: %v", err)
		} else if home == c.ChannelID {
			fail("HOME_CHANNEL_ID is the main channel")
		} else {
			c.HomeChannelID = home
		}
	}

	c.BotTokens, err = ParseTokens(get("TELEGRAM_BOT_TOKENS", ""))
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
		codes, err := lang.List(raw)
		if err != nil || len(codes) == 0 {
			fail("SUBTITLE_LANGUAGE must be language codes such as en, fa or de, separated by commas")
		} else {
			c.SubtitleLanguages = codes
			c.SubtitleLanguage = codes[0]
		}
	}
	if code, ok := NormalizeUILanguage(get("UI_LANGUAGE", "")); ok {
		c.UILanguage = code
	}
	c.JellyfinManaged = c.JellyfinURL == "" && !strings.EqualFold(get("JELLYFIN_MANAGED", "true"), "false")
	withJellyfin := c.JellyfinManaged || c.JellyfinURL != ""
	c.SeerrManaged = withJellyfin && strings.EqualFold(get("SEERR_MANAGED", "false"), "true")
	c.FladderManaged = withJellyfin && strings.EqualFold(get("FLADDER_MANAGED", "false"), "true")
	if (c.JellyfinURL == "") != (c.JellyfinAPIKey == "") {
		fail("set both JELLYFIN_URL and JELLYFIN_API_KEY, or neither")
	}
	for _, dir := range strings.Split(get("HOME_MEDIA_DIRS", ""), "|") {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			c.HomeMediaDirs = append(c.HomeMediaDirs, dir)
		}
	}
	for _, dir := range strings.Split(get("LOCAL_MEDIA_DIRS", ""), "|") {
		if dir = strings.TrimSpace(dir); dir != "" {
			c.LocalMediaDirs = append(c.LocalMediaDirs, dir)
		}
	}
	// Downloads are part of Seerr: it is what asks Radarr and Sonarr for a title.
	c.DownloadsEnabled = c.SeerrManaged && strings.EqualFold(get("DOWNLOADS_ENABLED", "false"), "true")
	c.DownloadsDir = get("DOWNLOADS_DIR", "")
	if c.DownloadsDir == "" {
		c.DownloadsDir = filepath.Join(c.DataDir, "downloads")
	}
	c.DownloadsMinFreeGB = getInt("DOWNLOADS_MIN_FREE_GB", 20, fail)
	if c.DownloadsMinFreeGB < 0 {
		fail("DOWNLOADS_MIN_FREE_GB must not be negative")
	}
	c.DownloadsSearchEveryMinutes = getInt("DOWNLOADS_SEARCH_EVERY_MINUTES", 60, fail)
	if c.DownloadsSearchEveryMinutes < 0 {
		fail("DOWNLOADS_SEARCH_EVERY_MINUTES must not be negative")
	}
	c.BitmagnetDBMaxGB = getInt("BITMAGNET_DB_MAX_GB", 40, fail)
	if c.BitmagnetDBMaxGB < 0 {
		fail("BITMAGNET_DB_MAX_GB must not be negative")
	}
	c.UploadMBPerSec = getInt("UPLOAD_MB_PER_SEC", 8, fail)
	c.UploadRestMinutes = getInt("UPLOAD_REST_MINUTES", 5, fail)
	c.UploadLongRestEveryGB = getInt("UPLOAD_LONG_REST_EVERY_GB", 20, fail)
	c.UploadLongRestMinutes = getInt("UPLOAD_LONG_REST_MINUTES", 60, fail)
	c.UploadDailyGB = getInt("UPLOAD_DAILY_GB", 50, fail)
	for _, n := range []int{c.UploadMBPerSec, c.UploadRestMinutes, c.UploadLongRestEveryGB, c.UploadLongRestMinutes, c.UploadDailyGB} {
		if n < 0 {
			fail("upload limits must not be negative")
			break
		}
	}
	c.SignedLibrary = strings.EqualFold(get("SIGNED_LIBRARY", ""), "true")
	search := strings.TrimSpace(get("TELEGRAM_SEARCH", "true"))
	c.TelegramSearch = search == "" || strings.EqualFold(search, "true") || search == "1"
	c.SearchMinMinutes = getInt("TELEGRAM_SEARCH_MIN_MINUTES", 10, fail)
	c.SearchMinMB = getInt("TELEGRAM_SEARCH_MIN_MB", 50, fail)
	c.SearchRuntimeTolerancePct = getInt("TELEGRAM_SEARCH_RUNTIME_TOLERANCE_PCT", 25, fail)
	c.SearchPromoteAt = getInt("TELEGRAM_SEARCH_PROMOTE_AT", 10, fail)
	c.SearchMaxDiscovered = getInt("TELEGRAM_SEARCH_MAX_DISCOVERED", 50, fail)
	c.SearchDenyAfter = getInt("TELEGRAM_SEARCH_DENY_AFTER", 3, fail)
	c.HashtagIndexBot = hashtagIndexBot(get("HASHTAG_INDEX_BOT", DefaultHashtagIndexBot))
	c.TMDBAPIKey = get("TMDB_API_KEY", "")
	c.TrendingSearch = strings.EqualFold(get("TRENDING_SEARCH", ""), "true") || get("TRENDING_SEARCH", "") == "1"
	c.TrendingAllowAdult = strings.EqualFold(get("TRENDING_ALLOW_ADULT", ""), "true")
	c.TrendingGenres = get("TRENDING_GENRES", "")
	c.TrendingExcludeGenres = get("TRENDING_EXCLUDE_GENRES", "")
	c.TrendingMaxPerDay = getInt("TRENDING_MAX_PER_DAY", 10, fail)
	c.InboxDeleteOriginals = strings.EqualFold(get("INBOX_DELETE_ORIGINALS", ""), "true")
	c.UploadHours = get("UPLOAD_HOURS", "")
	if _, _, err := ParseHours(c.UploadHours); err != nil {
		fail("UPLOAD_HOURS: %v", err)
	}
	return c, errors.Join(errs...)
}

// ParseHours reads a local-time window such as 01-08. An empty string is the whole day.
// The end hour is exclusive. A window that passes midnight, such as 22-06, is allowed.
func ParseHours(s string) (start, end int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, errors.New("use a window such as 01-08, or leave it empty")
	}
	start, err = strconv.Atoi(strings.TrimSpace(a))
	if err != nil {
		return 0, 0, errors.New("use a window such as 01-08, or leave it empty")
	}
	end, err = strconv.Atoi(strings.TrimSpace(b))
	if err != nil || start < 0 || start > 23 || end < 1 || end > 24 || start == end {
		return 0, 0, errors.New("hours must look like 01-08, with the end hour excluded")
	}
	return start, end, nil
}

// WithinHours reports whether hour falls inside the window from ParseHours.
func WithinHours(start, end, hour int) bool {
	if start == 0 && end == 0 {
		return true
	}
	if start < end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}

// ValidateServe adds the checks that only matter when running the gateway.
func (c Config) ValidateServe() error {
	if c.GatewayPublicURL == "" && !c.JellyfinManaged {
		return errors.New("GATEWAY_PUBLIC_URL is required")
	}
	return nil
}

// Reload reads the settings file again after it changed, letting it win over what the first read
// left in the environment.
func Reload(envPath string) (Config, error) {
	if err := godotenv.Overload(envPath); err != nil {
		return Config{}, fmt.Errorf("read %s: %w", envPath, err)
	}
	return Load(envPath)
}

var reMessageLink = regexp.MustCompile(`t\.me/c/(\d+)`)

// ParseChannelID accepts a message link from the channel, -100xxxxxxxxxx or the bare id, and returns
// the bare id. A message link is the easiest to get, as any Telegram app can copy one.
func ParseChannelID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("required")
	}
	if m := reMessageLink.FindStringSubmatch(s); m != nil {
		s = m[1]
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

// ParseTokens reads comma-separated bot tokens and drops duplicates.
func ParseTokens(s string) ([]string, error) {
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
