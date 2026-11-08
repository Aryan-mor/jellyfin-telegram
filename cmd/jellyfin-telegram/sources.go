package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/fuckjoin"
	"github.com/Aryan-mor/jellyfin-telegram/internal/newsfeed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsearch"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsource"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
	fileresolver "github.com/Aryan-mor/telegram-file-resolver"
)

// startSearch looks up requested titles on Telegram and copies a file it finds. It stays quiet until
// Seerr has a request and the switch is on.
func startSearch(cfg config.Config, envPath string, users *tguser.Manager, log *slog.Logger) *tgsearch.Service {
	svc := &tgsearch.Service{LibraryChannel: cfg.ChannelID, Log: log, On: tgsearch.Switch(envPath)}
	if users != nil {
		svc.TG = users
	}
	if cfg.SeerrManaged {
		svc.Want = &tgsearch.Seerr{Base: (&seerr.Server{}).URL(), Root: filepath.Join(cfg.DataDir, "seerr")}
	}
	return svc
}

// startSources prepares the copier of films and series from other Telegram channels. It stays idle
// until a person turns it on in the panel, and it needs a signed-in account. It returns nil when
// accounts cannot start.
func startSources(cfg config.Config, cat *catalog.Catalog, users *tguser.Manager) *tgsource.Service {
	if users == nil {
		return nil
	}
	svc := tgsource.New()
	svc.Cat, svc.TG, svc.LibraryChannel = cat, users, cfg.ChannelID
	return svc
}

// startNews prepares the list of channels that post bot links. The fuckjoin checkout is downloaded
// into the data directory the first time it is needed. Nil news means there is no signed-in account.
func startNews(ctx context.Context, cfg config.Config, cat *catalog.Catalog, users *tguser.Manager, sources *tgsource.Service, log *slog.Logger) (*newsfeed.Service, *fuckjoin.Runtime) {
	fj := fuckjoin.New(cfg.DataDir, cfg.APIID, cfg.APIHash, log)
	if sources == nil || users == nil {
		return nil, fj
	}
	svc := newsfeed.New()
	svc.Cat, svc.TG, svc.LibraryChannel, svc.Log = cat, users, cfg.ChannelID, log
	// The resolver runs in this process on the signed-in account, so there is no second login. The
	// fuckjoin runtime above is kept only until the resolver has passed a live test.
	rc := fileresolver.DefaultConfig()
	rc.StateDir, rc.Log = filepath.Join(cfg.DataDir, "resolver"), log
	// Several private channels per link add up quickly. A slower pace keeps Telegram's join limit away.
	rc.PacingDelay = 10 * time.Second
	if eng, err := fileresolver.New(newsfeed.Provider(users), rc); err != nil {
		log.Warn("news resolver could not start; falling back to fuckjoin", "err", err)
		svc.Open = &newsfeed.Bridge{FJ: fj, TG: users}
	} else {
		go func() { _ = eng.Run(ctx) }()
		svc.Open = &newsfeed.Resolver{Eng: eng, Note: svc.SetNote}
	}
	return svc, fj
}

// sourceState is what the Channels tab draws: the copier's own status and which accounts it can use.
func sourceState(svc *tgsource.Service, news *newsfeed.Service, fj *fuckjoin.Runtime, users *tguser.Manager) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		st, err := svc.Status(ctx)
		if err != nil {
			return nil, err
		}
		views := users.Views()
		healthy := len(users.Healthy())
		st["accounts"] = map[string]any{"total": len(views), "healthy": healthy}
		if news != nil {
			n, err := news.View(ctx)
			if err != nil {
				return nil, err
			}
			if fj != nil {
				n["fuckjoin"] = fj.View()
			}
			sessions := make([]map[string]any, 0, len(views))
			for _, v := range views {
				sessions = append(sessions, map[string]any{"user_id": v.UserID, "phone": v.Phone, "username": v.Username})
			}
			n["sessions"] = sessions
			st["news"] = n
		}
		return st, nil
	}
}

// sourceActions runs what the Channels tab asks for. Nothing is copied until the switch is turned on.
func sourceActions(svc *tgsource.Service, news *newsfeed.Service, fj *fuckjoin.Runtime) func(*http.Request, string, map[string]any) (any, error) {
	return func(r *http.Request, action string, body map[string]any) (any, error) {
		ctx := r.Context()
		switch action {
		case "add":
			input, _ := body["channel"].(string)
			src, err := svc.Add(ctx, input)
			if err != nil {
				return nil, err
			}
			return map[string]any{"source": src}, nil
		case "remove":
			return nil, svc.Remove(ctx, int64(number(body["id"])))
		case "pause":
			on, _ := body["enabled"].(bool)
			return nil, svc.Pause(ctx, int64(number(body["id"])), on)
		case "strict":
			on, _ := body["strict"].(bool)
			return nil, svc.Strict(ctx, int64(number(body["id"])), on)
		case "check":
			svc.CheckNow()
			if news != nil {
				news.Kick()
			}
			return nil, nil
		case "rescan":
			if news != nil {
				_ = news.Rescan(ctx)
			}
			return nil, svc.Rescan(ctx)
		case "news":
			return nil, setNews(ctx, svc, news, fj, body)
		case "news_add":
			if news == nil {
				return nil, errors.New("news channels are not available")
			}
			input, _ := body["channel"].(string)
			src, err := news.Add(ctx, input)
			if err != nil {
				return nil, err
			}
			return map[string]any{"source": src}, nil
		case "news_remove":
			if news == nil {
				return nil, errors.New("news channels are not available")
			}
			return nil, news.Remove(ctx, int64(number(body["id"])))
		case "news_pause":
			if news == nil {
				return nil, errors.New("news channels are not available")
			}
			on, _ := body["enabled"].(bool)
			return nil, news.Pause(ctx, int64(number(body["id"])), on)
		case "news_phone":
			phone, _ := body["phone"].(string)
			if err := fj.SendCode(ctx, phone); err != nil {
				return nil, err
			}
			return map[string]any{"step": "code"}, nil
		case "news_code":
			code, _ := body["code"].(string)
			if err := fj.SignIn(ctx, code); err != nil {
				return nil, err
			}
			if fj.View().Phase == "password" {
				return map[string]any{"step": "password"}, nil
			}
			return map[string]any{"step": "done"}, nil
		case "news_password":
			password, _ := body["password"].(string)
			if err := fj.Password(ctx, password); err != nil {
				return nil, err
			}
			return map[string]any{"step": "done"}, nil
		case "news_account":
			return nil, setNewsUser(ctx, news, int64(number(body["user_id"])))
		case "news_settings":
			return nil, saveNewsRules(ctx, news, body)
		case "news_update":
			sha, err := fj.Update(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"commit": sha}, nil
		case "settings":
			cur, err := svc.Settings(ctx)
			if err != nil {
				return nil, err
			}
			next := cur
			if v, ok := body["enabled"].(bool); ok {
				next.Enabled = v
			}
			setInt := func(key string, dst *int) {
				if _, ok := body[key]; ok {
					*dst = int(number(body[key]))
				}
			}
			setInt("look_back_days", &next.LookBackDays)
			setInt("every_minutes", &next.EveryMinutes)
			setInt("min_minutes", &next.MinMinutes)
			setInt("min_mb", &next.MinMB)
			setInt("max_gb", &next.MaxGB)
			setInt("max_per_day", &next.MaxPerDay)
			setInt("pause_seconds", &next.PauseSeconds)
			if v, ok := body["skip_words"].(string); ok {
				next.SkipWords = strings.TrimSpace(v)
			}
			if v, ok := body["only_words"].(string); ok {
				next.OnlyWords = strings.TrimSpace(v)
			}
			if v, ok := body["need_title"].(bool); ok {
				next.NeedTitle = v
			}
			if next.Enabled && !cur.Enabled {
				if err := mustHaveAccount(svc); err != nil {
					return nil, err
				}
			}
			return nil, svc.Save(ctx, next)
		default:
			return nil, errors.New("unknown action")
		}
	}
}

func setNews(ctx context.Context, svc *tgsource.Service, news *newsfeed.Service, _ *fuckjoin.Runtime, body map[string]any) error {
	if news == nil {
		return errors.New("news channels are not available")
	}
	cur, err := news.Settings(ctx)
	if err != nil {
		return err
	}
	on, _ := body["enabled"].(bool)
	if on && !cur.Enabled {
		if err := mustHaveAccount(svc); err != nil {
			return err
		}
	}
	cur.Enabled = on
	return news.Save(ctx, cur)
}

func setNewsUser(ctx context.Context, news *newsfeed.Service, id int64) error {
	if news == nil {
		return errors.New("news channels are not available")
	}
	cur, err := news.Settings(ctx)
	if err != nil {
		return err
	}
	cur.UserID = id
	return news.Save(ctx, cur)
}

func saveNewsRules(ctx context.Context, news *newsfeed.Service, body map[string]any) error {
	if news == nil {
		return errors.New("news channels are not available")
	}
	cur, err := news.Settings(ctx)
	if err != nil {
		return err
	}
	setInt := func(key string, dst *int) {
		if _, ok := body[key]; ok {
			*dst = int(number(body[key]))
		}
	}
	setInt("look_back_days", &cur.LookBackDays)
	setInt("every_minutes", &cur.EveryMinutes)
	setInt("min_minutes", &cur.MinMinutes)
	setInt("min_mb", &cur.MinMB)
	setInt("max_gb", &cur.MaxGB)
	setInt("max_per_day", &cur.MaxPerDay)
	setInt("pause_seconds", &cur.PauseSeconds)
	if v, ok := body["skip_words"].(string); ok {
		cur.SkipWords = strings.TrimSpace(v)
	}
	if v, ok := body["only_words"].(string); ok {
		cur.OnlyWords = strings.TrimSpace(v)
	}
	if v, ok := body["need_title"].(bool); ok {
		cur.NeedTitle = v
	}
	return news.Save(ctx, cur)
}

func mustHaveAccount(svc *tgsource.Service) error {
	if !svc.TG.HasAccount() {
		return errors.New("sign in a Telegram account that is an admin of the library channel first")
	}
	return nil
}

// sourcesView and sourcesDo leave the tab out of the panel when the copier could not start.
func sourcesView(svc *tgsource.Service, news *newsfeed.Service, fj *fuckjoin.Runtime, users *tguser.Manager) func(context.Context) (any, error) {
	if svc == nil {
		return nil
	}
	return sourceState(svc, news, fj, users)
}

func sourcesDo(svc *tgsource.Service, news *newsfeed.Service, fj *fuckjoin.Runtime) func(*http.Request, string, map[string]any) (any, error) {
	if svc == nil {
		return nil
	}
	return sourceActions(svc, news, fj)
}

// sessionsView lists the signed-in accounts for the panel's account tab.
func sessionsView(users *tguser.Manager) func() any {
	if users == nil {
		return nil
	}
	return func() any { return users.Views() }
}
