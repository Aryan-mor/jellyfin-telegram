package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validEnv = `TELEGRAM_API_ID=12345
TELEGRAM_API_HASH=abcdef
TELEGRAM_CHANNEL_ID=-1001234567890
TELEGRAM_BOT_TOKENS=111:aaa, 222:bbb ,111:aaa
`

func clearEnv(t *testing.T) {
	t.Helper()
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "TELEGRAM_") || strings.HasPrefix(key, "GATEWAY_") ||
			strings.HasPrefix(key, "JELLYFIN_") || strings.HasPrefix(key, "SEERR_") || strings.HasPrefix(key, "FLADDER_") || strings.HasPrefix(key, "SUBTITLE") ||
			key == "FFPROBE_PATH" || key == "LIBRARY_DIR" || key == "DATA_DIR" ||
			key == "CACHE_MAX_GB" || key == "SYNC_INTERVAL_SECONDS" {
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
}

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// load registers the variables with t, so whatever the .env file sets is undone at the
// end of the test.
func load(t *testing.T, body string) (Config, error) {
	t.Helper()
	clearEnv(t)
	for _, line := range strings.Split(body, "\n") {
		if key, _, ok := strings.Cut(line, "="); ok {
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
	return Load(writeEnv(t, body))
}

func TestLoadValid(t *testing.T) {
	c, err := load(t, validEnv)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIID != 12345 || c.ChannelID != 1234567890 {
		t.Errorf("parsed %+v", c)
	}
	if len(c.BotTokens) != 2 || c.BotTokens[1] != "222:bbb" {
		t.Errorf("tokens = %v", c.BotTokens)
	}
	if c.BotConcurrency != 4 || c.ReadaheadBlocks != 8 || c.CacheBytes != 4<<30 {
		t.Errorf("defaults = %+v", c)
	}
	if c.JellyfinEnabled() {
		t.Error("jellyfin should be off")
	}
	if !c.JellyfinManaged || !c.SeerrManaged || !c.FladderManaged {
		t.Error("a private Jellyfin, Seerr and Fladder are the default")
	}
}

func TestEmptyFileNamesEveryRequiredVariable(t *testing.T) {
	_, err := load(t, "")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, name := range []string{"TELEGRAM_API_ID", "TELEGRAM_API_HASH", "TELEGRAM_CHANNEL_ID", "TELEGRAM_BOT_TOKENS"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not mention %s: %v", name, err)
		}
	}
}

func TestErrorsNeverContainSecrets(t *testing.T) {
	_, err := load(t, `TELEGRAM_API_ID=12345
TELEGRAM_API_HASH=abcdef
TELEGRAM_CHANNEL_ID=1
TELEGRAM_BOT_TOKENS=not-a-token-SECRET
JELLYFIN_API_KEY=KEY-SECRET
`)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("secret leaked: %v", err)
	}
}

func TestParseChannelID(t *testing.T) {
	for in, want := range map[string]int64{
		"-1001234": 1234, "1234": 1234, " 55 ": 55,
		"https://t.me/c/4325588340/5": 4325588340, "t.me/c/99/1?single": 99,
	} {
		if got, err := ParseChannelID(in); err != nil || got != want {
			t.Errorf("ParseChannelID(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "abc", "-5", "@name"} {
		if _, err := ParseChannelID(in); err == nil {
			t.Errorf("ParseChannelID(%q) should fail", in)
		}
	}
}

func TestEnsureGatewayTokenPersists(t *testing.T) {
	c := Config{DataDir: filepath.Join(t.TempDir(), "nested")}
	first, err := c.EnsureGatewayToken()
	if err != nil || len(first) < 32 {
		t.Fatalf("token %q, %v", first, err)
	}
	second, err := c.EnsureGatewayToken()
	if err != nil || second != first {
		t.Fatalf("token changed: %q, %v", second, err)
	}
	c.GatewayToken = "fixed"
	if got, _ := c.EnsureGatewayToken(); got != "fixed" {
		t.Errorf("explicit token ignored: %q", got)
	}
}

func TestSeerrFollowsPrivateJellyfin(t *testing.T) {
	c, err := load(t, validEnv+"SEERR_MANAGED=false\n")
	if err != nil || !c.JellyfinManaged || c.SeerrManaged {
		t.Fatalf("skip seerr: %+v, %v", c, err)
	}
	c, err = load(t, validEnv+"JELLYFIN_MANAGED=false\nGATEWAY_PUBLIC_URL=http://127.0.0.1:8097\n")
	if err != nil || c.JellyfinManaged || c.SeerrManaged {
		t.Fatalf("no server: %+v, %v", c, err)
	}
	c, err = load(t, validEnv+`JELLYFIN_URL=http://127.0.0.1:8096
JELLYFIN_API_KEY=abc
GATEWAY_PUBLIC_URL=http://127.0.0.1:8097
`)
	if err != nil || c.JellyfinManaged || !c.SeerrManaged || !c.FladderManaged {
		t.Fatalf("existing Jellyfin should still start Seerr and Fladder: %+v, %v", c, err)
	}
	if err := c.ValidateServe(); err != nil {
		t.Fatal(err)
	}
}

func TestSubtitleLanguage(t *testing.T) {
	c, err := load(t, validEnv+"SUBTITLE_LANGUAGE=ger\n")
	if err != nil || c.SubtitleLanguage != "de" {
		t.Errorf("got %q, %v", c.SubtitleLanguage, err)
	}
	c, err = load(t, validEnv)
	if err != nil || c.SubtitleLanguage != "" {
		t.Errorf("unset should stay empty: %q, %v", c.SubtitleLanguage, err)
	}
	if _, err = load(t, validEnv+"SUBTITLE_LANGUAGE=klingon language\n"); err == nil || !strings.Contains(err.Error(), "SUBTITLE_LANGUAGE") {
		t.Errorf("bad code gave %v", err)
	}
}
