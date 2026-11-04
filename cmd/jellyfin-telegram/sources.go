package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/catalog"
	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tgsource"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

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

// sourceState is what the Channels tab draws: the copier's own status and which accounts it can use.
func sourceState(svc *tgsource.Service, users *tguser.Manager) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		st, err := svc.Status(ctx)
		if err != nil {
			return nil, err
		}
		views := users.Views()
		healthy := len(users.Healthy())
		st["accounts"] = map[string]any{"total": len(views), "healthy": healthy}
		return st, nil
	}
}

// sourceActions runs what the Channels tab asks for. Nothing is copied until the switch is turned on.
func sourceActions(svc *tgsource.Service) func(*http.Request, string, map[string]any) (any, error) {
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
			return nil, nil
		case "rescan":
			return nil, svc.Rescan(ctx)
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

func mustHaveAccount(svc *tgsource.Service) error {
	if !svc.TG.HasAccount() {
		return errors.New("sign in a Telegram account that is an admin of the library channel first")
	}
	return nil
}

// sourcesView and sourcesDo leave the tab out of the panel when the copier could not start.
func sourcesView(svc *tgsource.Service, users *tguser.Manager) func(context.Context) (any, error) {
	if svc == nil {
		return nil
	}
	return sourceState(svc, users)
}

func sourcesDo(svc *tgsource.Service) func(*http.Request, string, map[string]any) (any, error) {
	if svc == nil {
		return nil
	}
	return sourceActions(svc)
}
