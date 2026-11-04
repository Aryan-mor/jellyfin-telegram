package stack

import (
	"context"
	"errors"
	"runtime"
	"strings"

	"github.com/Aryan-mor/jellyfin-telegram/internal/seerr"
)

// seerrTarget is where Seerr, inside its container, finds Radarr or Sonarr.
//
// On Linux Seerr shares the host network, so the loopback port that is published is both the
// shortest and the fastest way. Elsewhere it is reached by container name over the shared Docker
// network. The published port through host.docker.internal also works, but connecting takes longer
// than the 250 ms Node allows for one attempt, so Seerr failed at random with an empty error.
func seerrTarget(goos, kind string) (string, int) {
	if goos == "linux" {
		if kind == "radarr" {
			return "127.0.0.1", RadarrPort
		}
		return "127.0.0.1", SonarrPort
	}
	if kind == "radarr" {
		return RadarrName, RadarrInnerPort
	}
	return SonarrName, SonarrInnerPort
}

// linkSeerr puts the Seerr container on the stack's network. Linux needs nothing, as Seerr uses the
// host network there.
func (r *Runner) linkSeerr(ctx context.Context) error {
	if r.SeerrURL == "" || runtime.GOOS == "linux" {
		return nil
	}
	if !containerRunning(ctx, seerr.ContainerName) {
		return errors.New("the Seerr container is not running")
	}
	out, err := docker(ctx, "inspect", "-f", "{{json .NetworkSettings.Networks}}", seerr.ContainerName)
	if err != nil {
		return err
	}
	if strings.Contains(out, `"`+Network+`"`) {
		return nil
	}
	_, err = docker(ctx, "network", "connect", Network, seerr.ContainerName)
	return err
}

func syncSeerr(ctx context.Context, r *Runner, sec secrets, radarr, sonarr Profile) error {
	public := strings.TrimRight(r.PublicBase, "/")
	radarrHost, radarrPort := seerrTarget(runtime.GOOS, "radarr")
	sonarrHost, sonarrPort := seerrTarget(runtime.GOOS, "sonarr")
	return seerr.ApplyDVR(ctx, r.SeerrURL, r.SeerrKey, []seerr.DVR{
		{
			Kind: "radarr", Hostname: radarrHost, Port: radarrPort, APIKey: sec.RadarrAPIKey,
			BaseURL: "/radarr", ExternalURL: public + "/radarr", ProfileID: radarr.ID, ProfileName: radarr.Name,
			Directory: "/data/media/movies",
		},
		{
			Kind: "sonarr", Hostname: sonarrHost, Port: sonarrPort, APIKey: sec.SonarrAPIKey,
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
