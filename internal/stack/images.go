// Package stack runs qBittorrent, Radarr, Sonarr, Bitmagnet and Postgres, then
// points them at each other. A failure here must not stop playback.
package stack

const (
	Network = "jellyfin-telegram"

	QbitName      = "jellyfin-telegram-qbittorrent"
	RadarrName    = "jellyfin-telegram-radarr"
	SonarrName    = "jellyfin-telegram-sonarr"
	BitmagnetName = "jellyfin-telegram-bitmagnet"
	PostgresName  = "jellyfin-telegram-postgres"

	VolumeRadarr   = "jellyfin-telegram-radarr"
	VolumeSonarr   = "jellyfin-telegram-sonarr"
	VolumePostgres = "jellyfin-telegram-postgres"

	QbitPort      = 18080
	RadarrPort    = 17878
	SonarrPort    = 18989
	BitmagnetPort = 13333
	DHTPort       = 3334
	PeerPort      = 6881

	// Version tags, not floating latest. Digests are filled in when a pull is pinned.
	ImageQbit      = "lscr.io/linuxserver/qbittorrent:5.1.2"
	ImageRadarr    = "lscr.io/linuxserver/radarr:6.4.4"
	ImageSonarr    = "lscr.io/linuxserver/sonarr:4.0.16"
	ImagePostgres  = "postgres:16.6"
	ImageBitmagnet = "ghcr.io/bitmagnet-io/bitmagnet:v0.10.1"
)

// Images is every image the stack pulls.
func Images() []string {
	return []string{ImageQbit, ImageRadarr, ImageSonarr, ImagePostgres, ImageBitmagnet}
}
