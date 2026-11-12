// Package seerr runs a private Seerr next to the managed Jellyfin and finishes its
// first-run setup with that Jellyfin as the media server.
package seerr

import (
	"fmt"

	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
)

// Version is the Seerr release this program runs.
const Version = "3.5.1"

// Port is where Seerr listens on this machine. Only this program connects to it.
const Port = 15055

// JellyfinHost is the name Seerr uses to reach a Jellyfin on this machine. Seerr runs as an
// ordinary process here, so loopback is this machine.
const JellyfinHost = "127.0.0.1"

const bundleBase = "https://github.com/Aryan-mor/seerr-bundles/releases/download/v" + Version + "/"

// bundles are built by https://github.com/Aryan-mor/seerr-bundles, because Seerr itself publishes
// only a Docker image. Each one holds Seerr's built files, its production
// dependencies compiled for that system, and the Node it needs. Each hash is the one in that
// release's SHA256SUMS.txt, so a replaced file is refused instead of being run. An empty hash means
// the bundle has not been published yet.
var bundles = map[string]managed.Artifact{
	"windows/amd64": {
		URL:    bundleBase + "seerr-" + Version + "-windows-amd64.tar.gz",
		SHA256: "a1e3130549d8531ac7098f184e1dcc5a7c5b17d511b83a49e5cf7c24bf2fa0a3",
	},
	"linux/amd64": {
		URL:    bundleBase + "seerr-" + Version + "-linux-amd64.tar.gz",
		SHA256: "315e9e2cd4b7c74eb2e8a5f75e136b40d00454d82784ae1ca1ba2b63e2670724",
	},
	"linux/arm64": {
		URL:    bundleBase + "seerr-" + Version + "-linux-arm64.tar.gz",
		SHA256: "436bfd2edc89c87970349470f1ddb5c362f09be174fa65795024f92639cdac94",
	},
}

func bundleFor(goos, goarch string) (managed.Artifact, error) {
	a, ok := bundles[goos+"/"+goarch]
	if !ok {
		return managed.Artifact{}, fmt.Errorf("no Seerr build for %s/%s", goos, goarch)
	}
	return a, nil
}

// mediaServerJellyfin is Seerr's MediaServerType.JELLYFIN. Plex is 1, so 2 makes Jellyfin
// the server the setup creates instead of leaving the choice on the first-run page.
const mediaServerJellyfin = 2
