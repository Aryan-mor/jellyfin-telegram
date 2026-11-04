package main

import (
	"path/filepath"

	"github.com/Aryan-mor/jellyfin-telegram/internal/config"
	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
	"github.com/Aryan-mor/jellyfin-telegram/internal/panel"
	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
)

// signInDetails lists how to sign in to each app. Someone who was handed the address has no other
// way to learn the account that was made for them, so the panel always shows it.
func signInDetails(cfg config.Config, creds managed.Credentials, seerrOn, fladderOn bool) []panel.Account {
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
	if fladderOn {
		out = append(out, panel.Account{App: "Fladder", Path: "/fladder/", Note: "A Jellyfin app in the browser. Use a Jellyfin account, as above."})
	}
	return out
}
