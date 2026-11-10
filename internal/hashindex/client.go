// Package hashindex talks to the hashtag index bot (telegram-hashtag-index).
//
// The index keeps links to public posts under hashtags, so a post can be found without joining
// its channel. This program hands it the links of its public mirror posts and asks it for the
// links of wanted titles. A library bot sends those messages. The signed-in account only opens
// the links the index returns.
package hashindex

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Talker sends a message to a bot and returns its answer.
type Talker interface {
	TalkToBot(ctx context.Context, username, text string, wait time.Duration) (string, error)
}

// Client asks the index bot.
type Client struct {
	// Bot is the bot's username.
	Bot string
	TG  Talker
	// Wait is how long to wait for an answer. Zero means 30 seconds.
	Wait time.Duration
	// MaxPages caps the pages one query reads. Zero means 4.
	MaxPages int
}

func (c *Client) wait() time.Duration {
	if c.Wait > 0 {
		return c.Wait
	}
	return 30 * time.Second
}

// Entry is one link the index returned.
type Entry struct {
	Channel string
	Message int
	Kind    string
	Tag     string
}

// fileKinds are the kinds a file search asks for.
const fileKinds = "video,document"

// Query returns the verified file links for a pattern, newest first. A pattern ends in * to match
// more than one tag.
func (c *Client) Query(ctx context.Context, pattern string) ([]Entry, error) {
	pages := c.MaxPages
	if pages <= 0 {
		pages = 4
	}
	var out []Entry
	text := "/q " + pattern + " kind:" + fileKinds
	for page := 0; page < pages && text != ""; page++ {
		reply, err := c.TG.TalkToBot(ctx, c.Bot, text, c.wait())
		if err != nil {
			return out, err
		}
		entries, next, err := parseReply(reply)
		if err != nil {
			return out, err
		}
		out = append(out, entries...)
		text = next
	}
	return out, nil
}

// parseReply reads a search answer: one "link kind #tag" line per link, then maybe a "next:" line.
func parseReply(reply string) ([]Entry, string, error) {
	reply = strings.TrimSpace(reply)
	if reply == "none" {
		return nil, "", nil
	}
	var out []Entry
	next := ""
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "next: "); ok {
			next = rest
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[2], "#") {
			return nil, "", fmt.Errorf("the index answered: %s", firstLine(reply))
		}
		ch, msg, ok := splitLink(f[0])
		if !ok {
			return nil, "", fmt.Errorf("the index sent a link that cannot be read: %s", f[0])
		}
		out = append(out, Entry{Channel: ch, Message: msg, Kind: f[1], Tag: f[2]})
	}
	return out, next, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 120 {
		line = line[:120]
	}
	return line
}

func splitLink(link string) (string, int, bool) {
	rest, ok := strings.CutPrefix(link, "https://t.me/")
	if !ok {
		return "", 0, false
	}
	name, id, ok := strings.Cut(rest, "/")
	if !ok || name == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(id)
	return strings.ToLower(name), n, err == nil && n > 0
}

// Item is a post to hand over.
type Item struct {
	// Tag is the hashtag with its "#".
	Tag     string
	Channel string
	Message int
}

// batch is how many links go in one message. The index lets a sender submit 30 a minute.
const batch = 25

// ErrRateLimited means the index asked the sender to slow down.
var ErrRateLimited = errors.New("the index is rate limiting this sender")

// Submit hands links to the index, several per message. It returns how many it sent before an
// error. A link the index already holds counts as sent.
func (c *Client) Submit(ctx context.Context, items []Item) (int, error) {
	sent := 0
	for len(items) > 0 {
		n := min(len(items), batch)
		var b strings.Builder
		b.WriteString("/create")
		for _, it := range items[:n] {
			fmt.Fprintf(&b, "\n%s https://t.me/%s/%d", it.Tag, it.Channel, it.Message)
		}
		reply, err := c.TG.TalkToBot(ctx, c.Bot, b.String(), c.wait())
		if err != nil {
			return sent, err
		}
		if !strings.HasPrefix(reply, "added ") {
			return sent, fmt.Errorf("the index answered: %s", firstLine(reply))
		}
		if strings.Contains(reply, "over the rate limit") {
			return sent, ErrRateLimited
		}
		sent += n
		items = items[n:]
	}
	return sent, nil
}
