package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// checkBots looks at every bot in the main channel (id 0) or in one mirror. With fix set, a bot that
// is missing or may not post is made an admin with the signed-in account, and the channel is
// checked once more.
func checkBots(r *http.Request, cat *catalog.Catalog, pool *tgpool.Pool, users *tguser.Manager, main int64, body map[string]any) (any, error) {
	if pool == nil {
		return nil, errors.New("the bots are not running")
	}
	ctx := tguser.Prioritise(r.Context())
	fix, _ := body["fix"].(bool)
	id := int64(number(body["id"]))
	channelID, label := int64(0), "main channel"
	lookup := main
	if id > 0 {
		list, err := cat.Mirrors(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, m := range list {
			if m.ID == id {
				channelID, lookup, label, found = m.ChannelID, m.ChannelID, m.Title, true
			}
		}
		if !found {
			return nil, errors.New("that mirror is not listed")
		}
	}
	results := pool.CheckBots(ctx, channelID)
	fixed := 0
	var fixErr string
	if fix {
		var names []string
		for _, c := range results {
			if c.State != tgpool.CheckOK && strings.TrimPrefix(c.Username, "@") != "" {
				names = append(names, strings.TrimPrefix(c.Username, "@"))
			}
		}
		if len(names) > 0 {
			if users == nil {
				fixErr = "sign in a Telegram account to add the bots"
			} else if ch, err := resolveCreated(ctx, users, lookup); err != nil {
				fixErr = err.Error()
			} else if err := promoteBots(ctx, users, pool, names, ch, nil); err != nil {
				fixErr = err.Error()
			}
			// Telegram needs a moment before a new admin can act.
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			before := results
			results = pool.CheckBots(ctx, channelID)
			for i, c := range results {
				if c.State == tgpool.CheckOK && i < len(before) && before[i].State != tgpool.CheckOK {
					fixed++
				}
			}
			if channelID != 0 {
				pool.ForgetMirrorDenials(channelID)
			}
		}
	}
	all := true
	bots := make([]map[string]any, 0, len(results))
	for _, c := range results {
		if c.State != tgpool.CheckOK {
			all = false
		}
		name := c.Username
		if name != "" {
			name = "@" + strings.TrimPrefix(name, "@")
		}
		bots = append(bots, map[string]any{"id": c.ID, "name": name, "state": c.State, "detail": c.Detail})
	}
	out := map[string]any{"ok": all, "channel": label, "bots": bots, "fixed": fixed}
	if fixErr != "" {
		out["error"] = fixErr
	}
	return out, nil
}
