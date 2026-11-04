package stack

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// SearchMissing asks Radarr and Sonarr to look again for everything they monitor and have no file for.
//
// They search once, when a title is added, and after that they only read the newest items of each
// indexer's feed. Bitmagnet adds hundreds of torrents a minute and learns of a given release at an
// unpredictable time, so a title it did not know yet would otherwise never be looked for again.
// Radarr leaves out films that are not released yet, which is what Radarr's own setting says.
func (r *Runner) SearchMissing(ctx context.Context) error {
	snap := r.Snapshot()
	if snap.RadarrAPIKey == "" || snap.SonarrAPIKey == "" {
		return errors.New("Radarr and Sonarr are not set up yet")
	}
	client := &http.Client{Timeout: time.Minute}
	var errs []error
	if err := searchMissing(ctx, App{Kind: "radarr", Base: fmt.Sprintf("http://127.0.0.1:%d/radarr", RadarrPort), Key: snap.RadarrAPIKey, HTTP: client}); err != nil {
		errs = append(errs, err)
	}
	if err := searchMissing(ctx, App{Kind: "sonarr", Base: fmt.Sprintf("http://127.0.0.1:%d/sonarr", SonarrPort), Key: snap.SonarrAPIKey, HTTP: client}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func searchMissing(ctx context.Context, a App) error {
	command := "MissingMoviesSearch"
	if a.Kind == "sonarr" {
		command = "MissingEpisodeSearch"
	}
	if err := a.call(ctx, http.MethodPost, "/api/v3/command", map[string]any{"name": command, "monitored": true}, nil); err != nil {
		return fmt.Errorf("%s: %w", a.Kind, err)
	}
	return nil
}
