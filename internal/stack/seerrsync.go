package stack

import (
	"context"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
)

func syncSeerr(ctx context.Context, r *Runner, sec secrets, radarr, sonarr Profile) error {
	public := strings.TrimRight(r.PublicBase, "/")
	host := r.JellyfinHost
	if host == "" {
		host = "host.docker.internal"
	}
	return seerr.ApplyDVR(ctx, r.SeerrURL, r.SeerrKey, []seerr.DVR{
		{
			Kind: "radarr", Hostname: host, Port: RadarrPort, APIKey: sec.RadarrAPIKey,
			BaseURL: "/radarr", ExternalURL: public + "/radarr", ProfileID: radarr.ID, ProfileName: radarr.Name,
			Directory: "/data/media/movies",
		},
		{
			Kind: "sonarr", Hostname: host, Port: SonarrPort, APIKey: sec.SonarrAPIKey,
			BaseURL: "/sonarr", ExternalURL: public + "/sonarr", ProfileID: sonarr.ID, ProfileName: sonarr.Name,
			Directory: "/data/media/tv",
		},
	})
}

// ForgetSeerr removes the servers from Seerr. The containers are stopped separately.
func (r *Runner) ForgetSeerr(ctx context.Context) error {
	if r.SeerrURL == "" {
		return nil
	}
	return seerr.RemoveDVR(ctx, r.SeerrURL, r.SeerrKey)
}
