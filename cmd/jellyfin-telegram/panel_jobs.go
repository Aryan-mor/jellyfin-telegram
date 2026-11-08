package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/panel"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgpool"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsetup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// panelJobs runs one create-mirror or create-bot job at a time and keeps its progress.
type panelJobs struct {
	mu      sync.Mutex
	running bool
	steps   []panel.JobLine
	errText string
	// restart stays true once any job saved a new bot. start does not clear it: the restart is
	// still owed until the program starts again, which also resets this value.
	restart bool
	result  any
	owned   any
	draft   *tgsetup.Found
}

func (j *panelJobs) view() panel.JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	progress := append([]panel.JobLine{}, j.steps...)
	if progress == nil {
		progress = []panel.JobLine{}
	}
	out := panel.JobView{
		Running: j.running, Progress: progress, Error: j.errText, Restart: j.restart, Result: j.result, Owned: j.owned,
	}
	return out
}

func (j *panelJobs) push(text, state string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.steps) > 0 && j.steps[len(j.steps)-1].State == "wait" {
		j.steps[len(j.steps)-1] = panel.JobLine{Text: text, State: state}
		return
	}
	j.steps = append(j.steps, panel.JobLine{Text: text, State: state})
}

func (j *panelJobs) start(work func(context.Context, panel.Step) (any, error)) error {
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return panel.ErrBusy
	}
	j.running = true
	j.steps = []panel.JobLine{{Text: "Starting", State: "wait"}}
	j.errText = ""
	j.result = nil
	j.owned = nil
	j.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		res, err := work(ctx, j.push)
		j.finish(res, err)
	}()
	return nil
}

func (j *panelJobs) finish(res any, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.running = false
	j.owned = nil
	if err != nil {
		j.errText = err.Error()
		for i := range j.steps {
			if j.steps[i].State == "wait" {
				j.steps[i].State = "bad"
			}
		}
	}
	if m, ok := res.(map[string]any); ok {
		if v, _ := m["restart"].(bool); v {
			j.restart = true
		}
		if owned, ok := m["owned"]; ok {
			j.owned = owned
			delete(m, "owned")
		}
		j.result = m
		return
	}
	j.result = res
}

func (j *panelJobs) mirrorCreate(cat *catalog.Catalog, pool *tgpool.Pool, users *tguser.Manager, envPath string, main int64) func(*http.Request, map[string]any) (any, error) {
	return func(_ *http.Request, body map[string]any) (any, error) {
		if users == nil {
			return nil, errors.New("sign in a Telegram account first")
		}
		title, _ := body["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, errors.New("give the mirror a name")
		}
		public := true
		if v, ok := body["public"].(bool); ok {
			public = v
		}
		preferred, _ := body["username"].(string)
		kinds, err := kindList(body["kinds"])
		if err != nil {
			return nil, err
		}
		fresh, botID, err := parseBotChoice(body["bot"])
		if err != nil {
			return nil, err
		}
		if !fresh {
			if _, err := poolBotName(pool, botID); err != nil {
				return nil, err
			}
		}
		return nil, j.start(func(ctx context.Context, progress panel.Step) (any, error) {
			return createMirror(ctx, cat, pool, users, envPath, main, title, public, preferred, kinds, fresh, botID, progress)
		})
	}
}

func (j *panelJobs) botCreate(users *tguser.Manager, envPath string, main int64) func(*http.Request, string, map[string]any) (any, error) {
	return func(_ *http.Request, action string, body map[string]any) (any, error) {
		if users == nil {
			return nil, errors.New("sign in a Telegram account first")
		}
		switch action {
		case "create":
			count := int(number(body["count"]))
			if count < 1 || count > 3 {
				return nil, errors.New("choose between 1 and 3 bots")
			}
			return nil, j.start(func(ctx context.Context, progress panel.Step) (any, error) {
				runner := &tgsetup.Runner{S: users, Pause: 4 * time.Second}
				found, err := runner.Create(ctx, tgsetup.CreateOptions{
					Title: "Jellyfin library", Channel: strconv.FormatInt(main, 10), Bots: count,
				}, func(text, state string) { progress(text, state) })
				// A bot made before a later step failed is still a working bot of this account.
				// Its token is saved either way, so a failure never leaves a bot nobody can use.
				if len(found.Tokens) > 0 {
					if serr := appendBotTokens(envPath, found.Tokens); serr != nil {
						return nil, errors.Join(err, serr)
					}
				}
				if len(found.Owned) > 0 {
					j.mu.Lock()
					j.draft = &found
					j.mu.Unlock()
					return map[string]any{"owned": ownedView(found.Owned), "kept": found.Bots, "restart": len(found.Tokens) > 0}, err
				}
				if err != nil {
					if len(found.Tokens) > 0 {
						return map[string]any{"restart": true, "bots": found.Bots}, err
					}
					return nil, err
				}
				return map[string]any{"ok": true, "restart": true, "bots": found.Bots}, nil
			})
		case "reuse":
			names, err := stringList(body["usernames"])
			if err != nil || len(names) == 0 {
				return nil, errors.New("pick a bot from the list")
			}
			j.mu.Lock()
			if j.draft == nil || j.draft.ChannelID == 0 {
				j.mu.Unlock()
				return nil, errors.New("there are no bots to choose from")
			}
			draft := *j.draft
			allowed := map[string]bool{}
			for _, b := range draft.Owned {
				allowed[strings.ToLower(b.Username)] = true
			}
			j.mu.Unlock()
			var chosen []string
			seen := map[string]bool{}
			for _, raw := range names {
				name := strings.TrimPrefix(strings.TrimSpace(raw), "@")
				key := strings.ToLower(name)
				if name == "" || seen[key] {
					continue
				}
				if !allowed[key] {
					return nil, errors.New("pick a bot from the list")
				}
				seen[key] = true
				chosen = append(chosen, name)
			}
			if len(chosen) < 1 || len(chosen) > 3 {
				return nil, errors.New("pick between 1 and 3 bots")
			}
			return nil, j.start(func(ctx context.Context, progress panel.Step) (any, error) {
				runner := &tgsetup.Runner{S: users, Pause: 4 * time.Second}
				found, err := runner.Reuse(ctx, tgsetup.ReuseOptions{
					ChannelID: draft.ChannelID, Title: draft.Title, Tokens: draft.Tokens, Bots: draft.Bots, Usernames: chosen,
				}, func(text, state string) { progress(text, state) })
				if len(found.Tokens) > 0 {
					if serr := appendBotTokens(envPath, found.Tokens); serr != nil {
						return nil, errors.Join(err, serr)
					}
				}
				if err != nil {
					if len(found.Tokens) > 0 {
						return map[string]any{"restart": true, "bots": found.Bots}, err
					}
					return nil, err
				}
				j.mu.Lock()
				j.draft = nil
				j.mu.Unlock()
				return map[string]any{"ok": true, "restart": true, "bots": found.Bots}, nil
			})
		default:
			return nil, errors.New("unknown action")
		}
	}
}

func ownedView(owned []tgsetup.OwnedBot) []map[string]any {
	out := make([]map[string]any, 0, len(owned))
	for _, b := range owned {
		out = append(out, map[string]any{"username": b.Username, "name": b.Name})
	}
	return out
}

func createMirror(ctx context.Context, cat *catalog.Catalog, pool *tgpool.Pool, users *tguser.Manager, envPath string, main int64, title string, public bool, preferred string, kinds []string, fresh bool, botID int64, progress panel.Step) (any, error) {
	progress("Creating the channel", "wait")
	val, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		return tgsetup.CreateChannel(ctx, api, title, "Mirror of the library. Managed by jellyfin-telegram.")
	})
	if err != nil {
		progress("Creating the channel", "bad")
		return nil, err
	}
	ch := val.(tgsetup.Created)
	progress("Channel "+ch.Title+" created", "ok")
	// Until the mirror is saved, a failure removes the new channel again. Otherwise every retry would
	// leave one more empty channel in the account. A bot made on the way keeps its token either way.
	saved := false
	created := ch
	defer func() {
		if saved {
			return
		}
		progress("Removing the unfinished channel", "wait")
		dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := users.Run(dctx, func(ctx context.Context, api *tg.Client) (any, error) {
			return nil, tgsetup.DeleteChannel(ctx, api, created.ID, created.AccessHash)
		}); err != nil {
			progress("Could not remove the unfinished channel "+created.Title+": "+err.Error(), "bad")
			return
		}
		progress("Unfinished channel removed", "ok")
	}()

	var username string
	if public {
		progress("Choosing a public name", "wait")
		named, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
			return tgsetup.SetPublicUsername(ctx, api, ch.ID, ch.AccessHash, preferred, title)
		})
		if err != nil {
			progress("Choosing a public name", "bad")
			return nil, err
		}
		username, _ = named.(string)
		progress("Public name @"+username, "ok")
	}
	progress("Muting the channel", "wait")
	if _, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		return nil, tgsetup.MuteChannel(ctx, api, ch.ID, ch.AccessHash)
	}); err != nil {
		progress("Could not mute the channel: "+err.Error(), "bad")
	} else {
		progress("Channel muted", "ok")
	}
	link := ""
	if username != "" {
		link = "https://t.me/" + username
	}
	result := map[string]any{"ok": true, "channel_id": ch.ID, "title": ch.Title, "username": username, "link": link}

	var postBot int64
	if fresh {
		mainCh, err := resolveCreated(ctx, users, main)
		if err != nil {
			return result, err
		}
		runner := &tgsetup.Runner{S: users, Pause: 4 * time.Second}
		bot, err := runner.NewBotFor(ctx, []tgsetup.Created{mainCh, ch}, func(text, state string) { progress(text, state) })
		if bot.Token != "" {
			result["restart"] = true
			result["bot"] = "@" + bot.Username
			if aerr := appendBotTokens(envPath, []string{bot.Token}); aerr != nil && err == nil {
				err = aerr
			}
			postBot, _ = config.BotID(bot.Token)
		}
		if err != nil {
			return result, err
		}
	} else {
		name, err := poolBotName(pool, botID)
		if err != nil {
			return result, err
		}
		progress("Making @"+name+" an admin", "wait")
		if _, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
			u, err := tgsetup.ResolveBot(ctx, api, name)
			if err != nil {
				return nil, err
			}
			return nil, tgsetup.PromoteBot(ctx, api, ch.ID, ch.AccessHash, u)
		}); err != nil {
			progress("Making @"+name+" an admin", "bad")
			return result, err
		}
		progress("@"+name+" is an admin", "ok")
		got, titleGot, err := pool.AcceptMirror(ctx, botID, ch.ID, username)
		if err != nil {
			return result, err
		}
		ch.ID, ch.Title = got, titleGot
		postBot = botID
	}
	row, err := cat.AddMirror(ctx, ch.ID, postBot, ch.Title, kinds)
	if err != nil {
		return result, err
	}
	saved = true
	if username != "" {
		if err := cat.SetMirrorUsername(ctx, row.ID, username); err != nil {
			return result, err
		}
	}
	if fresh {
		// The new bot joins the pool at the next start. Until then the mirror says so instead of
		// showing a send error.
		_ = cat.SetMirrorError(ctx, row.ID, "waits for a restart: the new bot connects then")
	}
	result["id"] = row.ID
	return result, nil
}

func resolveCreated(ctx context.Context, users *tguser.Manager, id int64) (tgsetup.Created, error) {
	val, err := users.Run(ctx, func(ctx context.Context, api *tg.Client) (any, error) {
		ch, err := tguser.ResolveChannelInput(ctx, api, strconv.FormatInt(id, 10))
		if err != nil {
			return nil, err
		}
		return tgsetup.Created{ID: ch.ID, AccessHash: ch.AccessHash, Title: ch.Title}, nil
	})
	if err != nil {
		return tgsetup.Created{}, err
	}
	return val.(tgsetup.Created), nil
}

func mirrorVisibility(r *http.Request, cat *catalog.Catalog, users *tguser.Manager, body map[string]any) (any, error) {
	if users == nil {
		return nil, errors.New("sign in a Telegram account first")
	}
	id := int64(number(body["id"]))
	if id <= 0 {
		return nil, errors.New("that mirror is not listed")
	}
	public, _ := body["public"].(bool)
	list, err := cat.Mirrors(r.Context())
	if err != nil {
		return nil, err
	}
	var current catalog.Mirror
	found := false
	for _, m := range list {
		if m.ID == id {
			current = m
			found = true
		}
	}
	if !found {
		return nil, errors.New("that mirror is not listed")
	}
	if public == (current.Username != "") {
		// Already in the state asked for. Making a public channel public again would replace its
		// name and break every link people already have.
		return map[string]any{"ok": true, "username": current.Username, "link": mirrorLink(current.Username)}, nil
	}
	var name string
	_, err = users.Run(r.Context(), func(ctx context.Context, api *tg.Client) (any, error) {
		ch, err := tguser.ResolveChannelInput(ctx, api, strconv.FormatInt(current.ChannelID, 10))
		if err != nil {
			return nil, err
		}
		if !public {
			return nil, tgsetup.ClearUsername(ctx, api, ch.ID, ch.AccessHash)
		}
		got, err := tgsetup.SetPublicUsername(ctx, api, ch.ID, ch.AccessHash, "", current.Title)
		name = got
		return nil, err
	})
	if err != nil {
		return nil, err
	}
	if err := cat.SetMirrorUsername(r.Context(), id, name); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "username": name, "link": mirrorLink(name)}, nil
}

func mirrorLink(username string) string {
	if username == "" {
		return ""
	}
	return "https://t.me/" + username
}

func mirrorAdmin(r *http.Request, pool *tgpool.Pool, users *tguser.Manager, body map[string]any) (any, error) {
	if users == nil {
		return nil, errors.New("sign in a Telegram account first")
	}
	botID := int64(number(body["bot_id"]))
	channel, _ := body["channel"].(string)
	name, err := poolBotName(pool, botID)
	if err != nil {
		return nil, err
	}
	_, err = users.Run(r.Context(), func(ctx context.Context, api *tg.Client) (any, error) {
		ch, err := tguser.ResolveChannelInput(ctx, api, channel)
		if err != nil {
			return nil, err
		}
		u, err := tgsetup.ResolveBot(ctx, api, name)
		if err != nil {
			return nil, err
		}
		return nil, tgsetup.PromoteBot(ctx, api, ch.ID, ch.AccessHash, u)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func poolBotName(pool *tgpool.Pool, id int64) (string, error) {
	if pool == nil || id <= 0 {
		return "", errors.New("that bot is not listed")
	}
	for _, st := range pool.Statuses() {
		if st.ID != id {
			continue
		}
		name := strings.TrimPrefix(st.Username, "@")
		if name == "" {
			return "", errors.New("that bot is not connected yet")
		}
		return name, nil
	}
	return "", errors.New("that bot is not listed")
}

func parseBotChoice(v any) (fresh bool, id int64, err error) {
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) == "new" {
			return true, 0, nil
		}
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil || n <= 0 {
			return false, 0, errors.New("choose a bot, or create a new one")
		}
		return false, n, nil
	case float64:
		if int64(x) <= 0 {
			return false, 0, errors.New("choose a bot, or create a new one")
		}
		return false, int64(x), nil
	default:
		return false, 0, errors.New("choose a bot, or create a new one")
	}
}

func stringList(v any) ([]string, error) {
	raw, ok := v.([]any)
	if !ok {
		return nil, errors.New("pick a bot from the list")
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, _ := item.(string)
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func appendBotTokens(path string, extra []string) error {
	if len(extra) == 0 {
		return errors.New("no bot was created")
	}
	m, err := godotenv.Read(path)
	if err != nil {
		return err
	}
	cur, err := config.ParseTokens(m["TELEGRAM_BOT_TOKENS"])
	if err != nil {
		return err
	}
	have := map[int64]bool{}
	for _, t := range cur {
		id, _ := config.BotID(t)
		have[id] = true
	}
	for _, t := range extra {
		id, err := config.BotID(t)
		if err != nil {
			return err
		}
		if have[id] {
			continue
		}
		have[id] = true
		cur = append(cur, t)
	}
	return setup.UpdateEnv(path, map[string]string{"TELEGRAM_BOT_TOKENS": strings.Join(cur, ",")})
}
