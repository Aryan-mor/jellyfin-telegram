package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/Aryan-mor/jellyfin-telegram/internal/autofilter"
	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/hashindex"
	"github.com/Aryan-mor/jellyfin-telegram/internal/newsfeed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsearch"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsource"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
	fileresolver "github.com/Aryan-mor/telegram-file-resolver"
)

// startSearch looks up wanted titles on Telegram and copies a file that passes verification. It
// stays quiet until Seerr has a request, or trending is on, and the switch is on.
func startSearch(cfg config.Config, envPath string, cat *catalog.Catalog, sources *tgsource.Service, users *tguser.Manager, idx *hashindex.Client, log *slog.Logger) *tgsearch.Service {
	svc := &tgsearch.Service{LibraryChannel: cfg.ChannelID, Log: log, On: tgsearch.Switch(envPath)}
	if users != nil {
		svc.TG = users
		// With the hashtag index on, requests and trending ask it first.
		if idx != nil {
			svc.TG = &hashindex.Searcher{Account: users, Index: idx, Skip: ownMirror(cat), Log: log}
		}
	}
	svc.Verify = tgsearch.Thresholds{
		MinSeconds:       cfg.SearchMinMinutes * 60,
		MinBytes:         int64(cfg.SearchMinMB) << 20,
		RuntimeTolerance: float64(cfg.SearchRuntimeTolerancePct) / 100,
	}
	svc.Limits = tgsearch.Limits{PromoteAt: cfg.SearchPromoteAt, MaxDiscovered: cfg.SearchMaxDiscovered, DenyAfter: cfg.SearchDenyAfter}
	if cfg.SearchMaxDiscovered == 0 {
		svc.Limits.MaxDiscovered = -1
	}
	if cat != nil {
		svc.Store = cat
	}
	if sources != nil {
		svc.Promote = sources
	}
	var want tgsearch.Multi
	if cfg.SeerrManaged {
		want = append(want, &tgsearch.Seerr{Base: (&seerr.Server{}).URL(), Root: filepath.Join(cfg.DataDir, "seerr")})
	}
	if cfg.TrendingSearch && strings.TrimSpace(cfg.TMDBAPIKey) != "" {
		// Only trending is filtered. Seerr requests above are always searched.
		tr := &tgsearch.Trending{
			Key: cfg.TMDBAPIKey, MaxPerDay: cfg.TrendingMaxPerDay,
			Settings: func() autofilter.Filter { return autofilter.Load(envPath) },
		}
		if cat != nil {
			tr.Store = cat
		}
		want = append(want, tr)
	} else if cfg.TrendingSearch {
		log.Warn("TRENDING_SEARCH is on but TMDB_API_KEY is empty; trending titles are not searched")
	}
	if len(want) > 0 {
		svc.Want = want
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

// startNews prepares the list of channels that post bot links. The resolver runs in this process on
// a signed-in account. Nil news means there is no signed-in account or the resolver could not start.
func startNews(ctx context.Context, cfg config.Config, cat *catalog.Catalog, users *tguser.Manager, sources *tgsource.Service, log *slog.Logger) *newsfeed.Service {
	if sources == nil || users == nil {
		return nil
	}
	svc := newsfeed.New()
	svc.Cat, svc.TG, svc.LibraryChannel, svc.Log = cat, users, cfg.ChannelID, log
	rc := fileresolver.DefaultConfig()
	rc.StateDir, rc.Log = filepath.Join(cfg.DataDir, "resolver"), log
	// Several private channels per link add up quickly. A slower pace keeps Telegram's join limit away.
	rc.PacingDelay = 10 * time.Second
	eng, err := fileresolver.New(newsfeed.Provider(users), rc)
	if err != nil {
		log.Warn("news resolver could not start; news channels are off", "err", err)
		return nil
	}
	go func() { _ = eng.Run(ctx) }()
	svc.Open = &newsfeed.Resolver{Eng: eng, Note: svc.SetNote}
	return svc
}

func sourceState(svc *tgsource.Service, news *newsfeed.Service, users *tguser.Manager) func(context.Context) (any, error) {
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
func sourceActions(svc *tgsource.Service, news *newsfeed.Service) func(*http.Request, string, map[string]any) (any, error) {
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
			return nil, setNews(ctx, svc, news, body)
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
		case "news_account":
			return nil, setNewsUser(ctx, news, int64(number(body["user_id"])))
		case "news_settings":
			return nil, saveNewsRules(ctx, news, body)
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

func setNews(ctx context.Context, svc *tgsource.Service, news *newsfeed.Service, body map[string]any) error {
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
		// News uses whichever signed-in account is free. The page no longer picks one.
		cur.UserID = 0
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
func sourcesView(svc *tgsource.Service, news *newsfeed.Service, users *tguser.Manager) func(context.Context) (any, error) {
	if svc == nil {
		return nil
	}
	return sourceState(svc, news, users)
}

func sourcesDo(svc *tgsource.Service, news *newsfeed.Service) func(*http.Request, string, map[string]any) (any, error) {
	if svc == nil {
		return nil
	}
	return sourceActions(svc, news)
}

// autoFilter serves the auto-add filter. Genres come from TMDB when a key is saved, so the setup
// page can offer them as soon as one is entered.
func autoFilter(envPath string) *autofilter.Server {
	return &autofilter.Server{EnvPath: envPath, Genres: func(ctx context.Context) ([]string, error) {
		key := os.Getenv("TMDB_API_KEY")
		if m, err := godotenv.Read(envPath); err == nil {
			if v, ok := m["TMDB_API_KEY"]; ok {
				key = v
			}
		}
		if strings.TrimSpace(key) == "" {
			return nil, nil
		}
		return tgsearch.GenreNames(ctx, key)
	}}
}

// sessionsView lists the signed-in accounts for the panel's account tab.
func sessionsView(users *tguser.Manager) func() any {
	if users == nil {
		return nil
	}
	return func() any { return users.Views() }
}
