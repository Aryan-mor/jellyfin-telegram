// Package seerr runs a private Seerr next to the managed Jellyfin and finishes its
// first-run setup with that Jellyfin as the media server.
package seerr

import (
	"fmt"

	"github.com/Aryan-mor/jellyfin-telegram/internal/managed"
)

// Version is the Seerr release this program runs.
const Version = "3.5.1"

// ContainerName is only read by the optional torrent stack, which joins a Seerr container to its
// Docker network. Seerr itself no longer runs in a container.
const ContainerName = "jellyfin-telegram-seerr"

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
		SHA256: "0cb7e22cc2f2e0f0855da791f6cfb734b76fd9e884e371513e079191c03bf642",
	},
	"linux/amd64": {
		URL:    bundleBase + "seerr-" + Version + "-linux-amd64.tar.gz",
		SHA256: "f947bde44f9b96c364ec995701edb39c26d5594db9a0caafc8d814d7148c461d",
	},
	"linux/arm64": {
		URL:    bundleBase + "seerr-" + Version + "-linux-arm64.tar.gz",
		SHA256: "57fd658fd1326e978ef99250de1799f420bfd057e31986c1d09a0b1c88be9c83",
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
