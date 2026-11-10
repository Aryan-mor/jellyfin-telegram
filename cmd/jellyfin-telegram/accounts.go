package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/panel"
	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
	"github.com/Aryan-mor/jellyfin-telegram/internal/setup"
	"github.com/Aryan-mor/jellyfin-telegram/internal/tguser"
)

// signInDetails lists how to sign in to each app. Someone who was handed the address has no other
// way to learn the account that was made for them, so the panel always shows it.
func signInDetails(cfg config.Config, creds managed.Credentials, seerrOn bool) []panel.Account {
	var out []panel.Account
	if creds.Password != "" {
		out = append(out, panel.Account{
			App: "Jellyfin", Path: "/", User: creds.User, Password: creds.Password,
			Note: "Administrator. Make an account for each other person in Jellyfin, under Dashboard, Users.",
		})
	} else {
		out = append(out, panel.Account{
			App: "Jellyfin", Path: "/",
			Note: "This is the Jellyfin you already run. Sign in with your own account there.",
		})
	}
	if seerrOn {
		note := "Everyone signs in with a Jellyfin account. A request from anyone but an administrator waits until an administrator approves it, unless Auto-Approve is turned on for that person in Seerr, Settings, Users."
		if creds.Password != "" {
			out = append(out, panel.Account{App: "Seerr", Path: "/seerr/", User: creds.User, Password: creds.Password, Note: "Administrator of Seerr, the same account as Jellyfin. " + note})
		} else if user, pass, ok := seerr.ReadLoginAccount(filepath.Join(cfg.DataDir, "seerr")); ok {
			out = append(out, panel.Account{App: "Seerr", Path: "/seerr/", User: user, Password: pass, Note: "Administrator of Seerr. " + note})
		} else {
			out = append(out, panel.Account{App: "Seerr", Path: "/seerr/", Note: note})
		}
	}
	return out
}

// accountControl runs the Telegram account actions of the panel: sign in, sign out and the upload
// pace. The default pace is kept in the .env file.
type accountControl struct {
	users   *tguser.Manager
	envPath string
	mu      sync.Mutex
	pace    tguser.Pace
}

func newAccountControl(cfg config.Config, envPath string, users *tguser.Manager) *accountControl {
	return &accountControl{users: users, envPath: envPath, pace: uploadPace(cfg)}
}

// state is the default pace the panel shows.
func (a *accountControl) state(context.Context) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pace, nil
}

func (a *accountControl) setDefaults(p tguser.Pace) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if a.users != nil {
		a.users.SetDefaults(p)
	}
	a.mu.Lock()
	a.pace = p
	a.mu.Unlock()
	if a.envPath == "" {
		return nil
	}
	return setup.UpdateEnv(a.envPath, map[string]string{
		"UPLOAD_MB_PER_SEC":         strconv.Itoa(p.MBPerSec),
		"UPLOAD_REST_MINUTES":       strconv.Itoa(p.RestMinutes),
		"UPLOAD_LONG_REST_EVERY_GB": strconv.Itoa(p.LongEveryGB),
		"UPLOAD_LONG_REST_MINUTES":  strconv.Itoa(p.LongRestMinutes),
		"UPLOAD_DAILY_GB":           strconv.Itoa(p.DailyGB),
		"UPLOAD_HOURS":              p.Hours,
	})
}

func (a *accountControl) do(r *http.Request, action string, body map[string]any) (any, error) {
	users := a.users
	if users == nil {
		return nil, errors.New("Telegram accounts are not available")
	}
	switch action {
	case "login":
		if cancel, _ := body["cancel"].(bool); cancel {
			users.CancelLogin()
			return map[string]any{"ok": true}, nil
		}
		if phone, _ := body["phone"].(string); phone != "" {
			return users.BeginLogin(r.Context(), phone)
		}
		if pass, _ := body["password"].(string); pass != "" {
			return users.SubmitPassword(r.Context(), pass)
		}
		code, _ := body["code"].(string)
		return users.SubmitCode(r.Context(), code)
	case "logout":
		return nil, users.Logout(r.Context(), int64(number(body["user_id"])))
	case "pace":
		p := tguser.Pace{
			MBPerSec: int(number(body["mb_per_sec"])), RestMinutes: int(number(body["rest_minutes"])),
			LongEveryGB: int(number(body["long_every_gb"])), LongRestMinutes: int(number(body["long_rest_minutes"])),
			DailyGB: int(number(body["daily_gb"])),
		}
		if hours, ok := body["hours"].(string); ok {
			p.Hours = hours
		}
		if id := int64(number(body["user_id"])); id != 0 {
			return nil, users.SetPace(id, p, true)
		}
		return nil, a.setDefaults(p)
	default:
		return nil, errors.New("unknown action")
	}
}
