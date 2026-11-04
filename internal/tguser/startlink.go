package tguser

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/gotd/td/tg"
)

// A link may be written with or without a scheme, with www, and with a slash before the query.
// tg://resolve?domain=<bot>&start=<arg> is the form Telegram itself uses.
var reStart = regexp.MustCompile(`(?i)(?:^|[^\w./])((?:(?:https?:)?//)?(?:www\.)?(?:t\.me|telegram\.me|telegram\.dog)/[A-Za-z0-9_]{5,}/?\?[^\s<>)\]\"']+)`)
var reResolve = regexp.MustCompile(`(?i)(?:^|[^\w./])(tg://resolve\?[^\s<>)\]\"']+)`)
var reBot = regexp.MustCompile(`^[A-Za-z0-9_]{5,}$`)

// StartURLs returns the distinct bot deep links in text and in text-url entities.
// Each result is https://t.me/<bot>?start=<argument> with the bot name in lower case.
func StartURLs(text string, entityURLs []string) []string {
	found := append(located(reStart, text), located(reResolve, text)...)
	sort.SliceStable(found, func(i, j int) bool { return found[i].at < found[j].at })
	var raw []string
	for _, hit := range found {
		raw = append(raw, hit.raw)
	}
	raw = append(raw, entityURLs...)
	seen := map[string]bool{}
	var out []string
	for _, item := range raw {
		link, ok := CanonicalStart(item)
		if !ok || seen[link] {
			continue
		}
		seen[link] = true
		out = append(out, link)
	}
	return out
}

type locatedLink struct {
	at  int
	raw string
}

func located(re *regexp.Regexp, text string) []locatedLink {
	var out []locatedLink
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		if len(m) >= 4 && m[2] >= 0 {
			out = append(out, locatedLink{at: m[2], raw: text[m[2]:m[3]]})
		}
	}
	return out
}

// CanonicalStart reports the bot deep link form, or false when raw is not one.
func CanonicalStart(raw string) (string, bool) {
	raw = strings.TrimSpace(strings.TrimRight(raw, ".,;:!?"))
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + strings.TrimPrefix(raw, "//")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	bot, arg := startParts(u)
	if !reBot.MatchString(bot) || arg == "" {
		return "", false
	}
	return "https://t.me/" + strings.ToLower(bot) + "?start=" + url.QueryEscape(arg), true
}

func startParts(u *url.URL) (bot, arg string) {
	if strings.EqualFold(u.Scheme, "tg") && strings.EqualFold(u.Host, "resolve") {
		return u.Query().Get("domain"), queryValue(u, "start")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "t.me" && host != "telegram.me" && host != "telegram.dog" {
		return "", ""
	}
	bot = strings.Trim(u.Path, "/")
	if i := strings.IndexByte(bot, '/'); i >= 0 {
		bot = bot[:i]
	}
	return bot, queryValue(u, "start")
}

func queryValue(u *url.URL, key string) string {
	for k, vs := range u.Query() {
		if strings.EqualFold(k, key) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

func textURL(msg *tg.Message) []string {
	var out []string
	for _, ent := range msg.Entities {
		if u, ok := ent.(*tg.MessageEntityTextURL); ok && u.URL != "" {
			out = append(out, u.URL)
		}
	}
	return out
}

// buttonURLs reads bot links that are buttons under the post. Copying such a post shows
// "label (url)", but the message text itself does not contain the URL.
func buttonURLs(msg *tg.Message) []string {
	markup, ok := msg.ReplyMarkup.(*tg.ReplyInlineMarkup)
	if !ok || markup == nil {
		return nil
	}
	var out []string
	for _, row := range markup.Rows {
		for _, btn := range row.Buttons {
			if u := inlineURL(btn.Type); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

func inlineURL(t tg.InlineButtonTypeClass) string {
	u, ok := t.(*tg.InlineButtonTypeURL)
	if !ok || u == nil {
		return ""
	}
	return u.URL
}
