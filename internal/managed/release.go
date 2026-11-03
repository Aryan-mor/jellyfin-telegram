// Package managed installs and runs a private copy of the official Jellyfin server, so a user
// needs nothing but this program.
package managed

import "fmt"

const baseURL = "https://repo.jellyfin.org/files/"

// Artifact is one file to download. The site publishes no checksums, so each hash is pinned here
// after downloading the release once. A changed file is refused instead of being run.
type Artifact struct {
	URL    string
	SHA256 string
}

// Release is everything needed to run Jellyfin on one platform.
type Release struct {
	Version string
	Server  Artifact
	// FFmpeg is nil where the server archive already includes it.
	FFmpeg *Artifact
}

const jellyfinVersion = "12.4"

var releases = map[string]Release{
	"windows/amd64": {
		Version: jellyfinVersion,
		Server: Artifact{
			URL:    baseURL + "server/windows/stable/v12.4/amd64/jellyfin_12.4-amd64.zip",
			SHA256: "d62b5b30390213680fd1fae5484082ae27b2a778cfd40a473ad7b1e4147d75a6",
		},
	},
	"linux/amd64": {
		Version: jellyfinVersion,
		Server: Artifact{
			URL:    baseURL + "server/linux/stable/v12.4/amd64/jellyfin_12.4-amd64.tar.gz",
			SHA256: "e835e224571ac9186eed1d2239eeb0e972ab31eb61ade44594289afc94c15875",
		},
		FFmpeg: &Artifact{
			URL:    baseURL + "ffmpeg/linux/latest-7.x/amd64/jellyfin-ffmpeg_7.1.4-3_portable_linux64-gpl.tar.xz",
			SHA256: "cab9ff40a47e4232d231e4eb7e4e85fabfeec56c6905266bc94291fc0881f83f",
		},
	},
	"linux/arm64": {
		Version: jellyfinVersion,
		Server: Artifact{
			URL:    baseURL + "server/linux/stable/v12.4/arm64/jellyfin_12.4-arm64.tar.gz",
			SHA256: "88c81e58fabba2c58762b3cb626e0648a4127695e95d34b66ef57d4e96671de4",
		},
		FFmpeg: &Artifact{
			URL:    baseURL + "ffmpeg/linux/latest-7.x/arm64/jellyfin-ffmpeg_7.1.4-3_portable_linuxarm64-gpl.tar.xz",
			SHA256: "77e4b5d044ab73e1f26c9aadaa5d6014d1782500bf2c29afb3ab81f5bea98b1f",
		},
	},
}

// ReleaseFor returns the release for a platform.
func ReleaseFor(goos, goarch string) (Release, error) {
	r, ok := releases[goos+"/"+goarch]
	if !ok {
		return Release{}, fmt.Errorf("no Jellyfin build for %s/%s, install Jellyfin yourself and set JELLYFIN_URL", goos, goarch)
	}
	return r, nil
}
