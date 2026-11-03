// Package seerr runs a private Seerr next to the managed Jellyfin and finishes its
// first-run setup with that Jellyfin as the media server.
package seerr

// Image is Seerr v3.5.1. The digest is pinned so a moved tag cannot change what runs.
const Image = "ghcr.io/seerr-team/seerr:v3.5.1@sha256:d8fab675b55ad293ded98fd71efcfd46054cbad9746534ae1cc4794a88c0b3be"

// ContainerName is stable so a copy left over from an earlier run can be removed.
const ContainerName = "jellyfin-telegram-seerr"

// Port is where Seerr listens on this machine. Only this program connects to it.
const Port = 15055

// mediaServerJellyfin is Seerr's MediaServerType.JELLYFIN. Plex is 1, so 2 makes Jellyfin
// the server the setup creates instead of leaving the choice on the first-run page.
const mediaServerJellyfin = 2
