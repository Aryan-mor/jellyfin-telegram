// Package chanindex talks to the channel index bot (telegram-channel-index).
//
// The index lists channels under a fixed set of tags. This program asks it for a page and, later,
// tells it when a channel it added has brought files in. The signed-in account sends those
// messages. A library bot cannot talk to this bot.
package chanindex

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ErrLimited means the index refused another introduction today.
var ErrLimited = errors.New("the index is rate limiting this sender")

// ErrRejected means the index will not list that channel.
var ErrRejected = errors.New("the index refused that channel")

// Talker sends a message to a bot and returns its answer.
type Talker interface {
	TalkToBot(ctx context.Context, username, text string, wait time.Duration) (string, error)
}

// Client asks the channel index bot.
type Client struct {
	Bot  string
	TG   Talker
	Wait time.Duration
}

func (c *Client) wait() time.Duration {
	if c.Wait > 0 {
		return c.Wait
	}
	return 30 * time.Second
}

// Item is one channel on a result page.
type Item struct {
	N       int    `json:"n"`
	Ref     string `json:"ref"`
	Score   int    `json:"score"`
	Members int    `json:"members"`
	Title   string `json:"title"`
}

// Page is one search answer.
type Page struct {
	ListID int64  `json:"list_id"`
	Tag    string `json:"tag"`
	Page   int    `json:"page"`
	Pages  int    `json:"pages"`
	Items  []Item `json:"items"`
}

// Search asks for one page of a tag. Page numbers start at 1.
func (c *Client) Search(ctx context.Context, tag string, page int) (Page, error) {
	if page < 1 {
		page = 1
	}
	text := "/search " + tag
	if page > 1 {
		text += " " + strconv.Itoa(page)
	}
	reply, err := c.TG.TalkToBot(ctx, c.Bot, text, c.wait())
	if err != nil {
		return Page{}, err
	}
	return parseSearch(reply)
}

// Vote sends one vote for a channel ref such as @name or c/123. value is +1 or -1.
func (c *Client) Vote(ctx context.Context, ref, tag string, value int) error {
	cmd := "/accept"
	if value < 0 {
		cmd = "/reject"
	}
	reply, err := c.TG.TalkToBot(ctx, c.Bot, cmd+" "+ref+" "+tag, c.wait())
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(reply), "ok ") {
		return fmt.Errorf("the index answered: %s", firstLine(reply))
	}
	return nil
}

// Introduce registers a public channel under tags. The username has no @.
// A reply of "ok" or "pending" both count. The index checks the channel itself.
func (c *Client) Introduce(ctx context.Context, username string, tags []string) error {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" || len(tags) == 0 {
		return errors.New("a public channel and a tag are required")
	}
	list := append([]string(nil), tags...)
	slices.Sort(list)
	reply, err := c.TG.TalkToBot(ctx, c.Bot, "/add @"+username+" "+strings.Join(list, " "), c.wait())
	if err != nil {
		return err
	}
	reply = strings.TrimSpace(reply)
	if strings.HasPrefix(reply, "ok ") || strings.HasPrefix(reply, "pending ") {
		return nil
	}
	if reply == "limited" {
		return ErrLimited
	}
	if reply == "no" {
		return ErrRejected
	}
	return fmt.Errorf("the index answered: %s", firstLine(reply))
}

func parseSearch(reply string) (Page, error) {
	reply = strings.TrimSpace(reply)
	if reply == "" || reply == "none" {
		return Page{}, nil
	}
	lines := strings.Split(reply, "\n")
	var page Page
	var id int64
	var tag, frac string
	if _, err := fmt.Sscanf(strings.TrimSpace(lines[0]), "list %d tag %s page %s", &id, &tag, &frac); err != nil {
		return Page{}, fmt.Errorf("the index answered: %s", firstLine(reply))
	}
	cur, total, ok := splitPage(frac)
	if !ok || id <= 0 || tag == "" {
		return Page{}, fmt.Errorf("the index answered: %s", firstLine(reply))
	}
	page.ListID, page.Tag, page.Page, page.Pages = id, tag, cur, total
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "next:") {
			continue
		}
		item, ok := parseItem(line)
		if !ok {
			return Page{}, fmt.Errorf("the index answered: %s", firstLine(line))
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

func splitPage(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return 0, 0, false
	}
	cur, err := strconv.Atoi(a)
	total, err2 := strconv.Atoi(b)
	return cur, total, err == nil && err2 == nil && cur > 0 && total > 0
}

func parseItem(line string) (Item, bool) {
	f := strings.Fields(line)
	if len(f) < 4 {
		return Item{}, false
	}
	n, err1 := strconv.Atoi(f[0])
	score, err2 := strconv.Atoi(f[2])
	members, err3 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil || err3 != nil || n < 1 {
		return Item{}, false
	}
	if !strings.HasPrefix(f[1], "@") && !strings.HasPrefix(f[1], "c/") && !strings.HasPrefix(f[1], "+") {
		return Item{}, false
	}
	title := ""
	if len(f) > 4 {
		title = strings.Join(f[4:], " ")
	}
	return Item{N: n, Ref: f[1], Score: score, Members: members, Title: title}, true
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 120 {
		line = line[:120]
	}
	return line
}
