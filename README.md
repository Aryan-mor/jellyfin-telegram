# jellyfin-telegram

Play video stored in a private Telegram channel with stock Jellyfin. There is no fork and
no plugin. Jellyfin sees a normal library of `.strm` files, and each one points at a
message in your channel.

```text
Telegram channel  <--MTProto, bot pool-->  jellyfin-telegram  <--HTTP ranges-->  Jellyfin and clients
                                                   |
                                                   +--> library/Movies, library/Shows (.strm files)
```

The service runs as one binary on Windows and Linux. It needs no CGO, FUSE or database server.

## Requirements

- Go 1.26 or newer, to build
- A private Telegram channel holding your videos
- One or more bots, each an admin of that channel, from [@BotFather](https://t.me/BotFather)
- `api_id` and `api_hash` from [my.telegram.org](https://my.telegram.org)
- Jellyfin 10.11.8 or 12.4 (the versions tested)
- Docker, only if you tick an extra in the setup page. Jellyfin alone does not need it. The extras are
  Fladder (a nicer web client), Seerr (requests) and, under Seerr, the downloader.

## Setup

Run the binary with an empty folder. It opens a setup page, then downloads official Jellyfin
12.4, starts it on loopback, and serves Jellyfin, the setup page and the streams on
`GATEWAY_LISTEN` (port 8097). From another device, open the address printed when it is ready.
That address is also the one a phone away from home should use. The private Jellyfin port is
not reachable from the network.

Windows may ask to allow the new program through the firewall the first time another device
connects. Away from home, a mesh VPN such as Tailscale reaches that same port with no extra
setting. Opening the port on the public internet is a separate choice and is left off.

To use a Jellyfin you already run, set `JELLYFIN_URL` and `JELLYFIN_API_KEY` instead. Set
`JELLYFIN_MANAGED=false` to start with no Jellyfin at all.

1. Build the binary.

   ```shell
   go build -o jellyfin-telegram ./cmd/jellyfin-telegram
   ```

   On Windows, use `-o jellyfin-telegram.exe`.

2. Copy `.env.example` to `.env` and fill it in. Every setting is explained there.

3. Verify the setup. This reports each bot, the channel contents, the gateway URL and
   Jellyfin, and it changes nothing.

   ```shell
   jellyfin-telegram check
   ```

4. Measure download speed. Run it with one bot and with all of them to see what extra bots add.

   ```shell
   jellyfin-telegram bench -bots 1
   jellyfin-telegram bench
   ```

5. Start the service. It indexes the channel and writes the library.

   ```shell
   jellyfin-telegram serve
   ```

   With no `JELLYFIN_URL`, this is the whole setup: the page asks for the channel and the
   bots, then Jellyfin is installed, the Movies and Shows libraries are created, and the
   sign-in password is written to `DATA_DIR/jellyfin/credentials.json`. Seerr is started on the
   same port, at `/seerr/`, and signed in to that Jellyfin, so requests use it by default.
   Sign in to Seerr with the same user. An existing Jellyfin gets Seerr too, at that same `/seerr/`.

   On this computer open `http://127.0.0.1:8097/panel`. It uses the same port. The page shows the address a phone should use.
   There you add bots, update the channel link and the language, and follow the steps for
   opening Jellyfin from another network with Tailscale. Saved bots are connected on the
   next start.

   The Downloads tab can turn on a private Radarr, Sonarr, qBittorrent and Bitmagnet. Seerr then
   requests a film or episode, qBittorrent downloads it, and a Telegram account you sign in on that
   tab posts it to the channel. Playback still goes through the same `.strm` file. The copy on disk
   is deleted after Jellyfin has probed the message. A file larger than that account can send is
   refused and is not split. Sign-in is accepted from this computer or over Tailscale, because the
   panel is plain HTTP and the session is full access to the account. The session file is not
   encrypted. More accounts drain the queue faster. One file still uses one account. Turning the
   feature off removes the containers and leaves playback alone. Your home IP is visible to peers
   and to the DHT. There is no VPN in this setup.

When you point at your own Jellyfin instead:

1. Add `LIBRARY_DIR/Movies` as another folder of the existing Movies library, and
   `LIBRARY_DIR/Shows` as another folder of the existing Shows library. A separate library
   hides these videos from the rest of the collection.
2. Create an API key under Dashboard > API Keys and set `JELLYFIN_URL` and `JELLYFIN_API_KEY`,
   so new files get a runtime and codecs.
3. Turn off saving subtitles into media folders (`SaveSubtitlesWithMedia`) on both libraries.
   Otherwise a subtitle added to a season folder Jellyfin has already read stays invisible
   until Jellyfin restarts. The managed server does this itself. Subtitles already sitting
   next to a file stay where they are.

### Running Jellyfin in Docker

Mount the library into the container and use a `GATEWAY_PUBLIC_URL` the container and your
clients can both reach, such as the host's LAN address. `JELLYFIN_URL` is the address as
seen from this service.

## How files are named

Names are parsed from the file name, then the caption. The result decides what Jellyfin does:

| In the channel | Written to the library | Metadata comes from |
|---|---|---|
| `Dune.2021.mkv`, caption `tmdb 438631` | `Movies/Dune (2021) [tmdbid-438631]/...` | TMDB, exact match |
| `Heat.1995.1080p.mkv` | `Movies/Heat (1995)/Heat (1995) - 1080p.strm` | TMDB, by title and year |
| `Lost.S01E02.mkv` | `Shows/Lost/Season 01/Lost S01E02.strm` | TMDB, by title |
| `Family Trip.mp4` | `Movies/Family Trip/` with `movie.nfo` and `poster.jpg` | The caption and the Telegram thumbnail |

For exact matches, put a TMDB id or a themoviedb.org link in the caption. Without one,
Jellyfin guesses from the title and can pick the wrong entry.

If the first caption line is exactly `Title (Year)`, with or without a leading emoji, it
replaces the title and year from the file name. File names often carry site tags and
channel handles, so this line is the most reliable way to name a video.

Movies of one title with different resolutions become versions of a single item.

## Subtitles

Set `SUBTITLE_LANGUAGE` to your language, as a code such as `en`, `de`, `fr`, `fa` or `ja`. If it
is empty, subtitles are not handled. Changing it checks every file again.

Tracks are identified from the first bytes of each file, without reading the whole file. Then
the first matching rule decides:

| The file | What happens |
|---|---|
| Has a track in your language, by tag, title or a sample of its text | Nothing more is needed |
| Is named or captioned as hardsubbed | No subtitle is added |
| Has audio in your language | No subtitle is added |
| Has a title in the script of your language | No subtitle is added |
| Anything else | A subtitle from sub-plus.ir is uploaded to Jellyfin |

Hardsub words only stop the search when they apply: a Persian word for a burn-in counts for a
Persian user and is ignored for everyone else.

This needs `ffprobe`, which the service finds in `PATH` or the Jellyfin install folder. Set
`SUBTITLES_ONLINE=false` to stop contacting sub-plus.ir, which receives the title or IMDb id of
these videos. That site offers 32 languages, and Korean and Chinese are not among them. Online
subtitles are off for a language it does not list.

Subtitle files you post to the channel are used first. A `.srt`, `.ass` or `.zip` belongs to a
video when it replies to that video, or when its name gives the same title and episode. Post it
before the video is first probed, because Jellyfin does not notice a later one until it restarts.

### Embedded subtitles

Many clients, including Jellyfin Web and stock Fladder, request an embedded subtitle as a separate
file, and Jellyfin reads the whole video to extract it. To avoid that, the service does the work
once per file:

1. It extracts every track in the background, one file at a time, at
   `SUBTITLE_EXTRACT_MB_PER_SEC`. It pauses while anything plays.
2. It posts the result to the channel as `jftg-subs-<message>-<document>.zip`, in reply to the video.
3. It places the tracks in Jellyfin's subtitle cache, so Jellyfin finds nothing left to extract.

If Jellyfin's cache or this service's `DATA_DIR` is lost, the tracks come back from the channel
without reading any video. Do not delete these messages, or the file is read again. Deleting a
video also deletes its archive.

A track in your language with fewer than 2 cues a minute, such as an advertisement or signs only,
is replaced by a subtitle from your channel or from sub-plus.ir, if one exists.

The bots need permission to post in the channel, and the service must be able to write to
Jellyfin's data folder. When Jellyfin runs in Docker or on another machine, set `JELLYFIN_DATA_DIR`
to that folder as seen from this service. Set `SUBTITLE_EXTRACT_MB_PER_SEC=0` to turn this off.

## Limits

- A video is one Telegram message, so its size is capped by Telegram's upload limit.
  Files split across several messages are not joined.
- Telegram subtitle files in the channel are not attached to videos yet.
- Episodes are grouped by the parsed title, year and TMDB id. If those differ between
  episodes of one show, each difference becomes a separate show folder.
- `PlaybackInfo` exposes the stream URL, including its token, to Jellyfin users and clients,
  and Jellyfin writes it to its logs. Treat that token as shared with everyone who can use
  your server. To change it, delete `DATA_DIR/gateway.token` and restart the service.
- One language at a time is supported.
- A subtitle added to an item Jellyfin has already probed appears only after Jellyfin restarts.
- Do not edit or touch `.strm` files. A changed modification time makes Jellyfin delete the extracted
  subtitles of that item on every playback request until it restarts. The service keeps the time
  when it writes a new address.
- Image subtitles such as PGS are extracted the same way, but this has not been tested.
- The service does not start by itself after a reboot.
- Jellyfin reports the size of the `.strm` file, not the video, as the media source size.
- Bots cannot read channel history, so ids are walked in order. A run of more than 2000
  deleted messages at the end of the channel hides whatever follows it until a newer
  message appears within reach.

## Development

```shell
go test -race ./...
go test -tags e2e -v -timeout 15m ./test/e2e
```

The end-to-end test needs Docker and ffmpeg. It runs a real Jellyfin against the real
service, with only Telegram replaced by a fake that counts reads.

See [docs/architecture.md](docs/architecture.md) for the design and measured Jellyfin behaviour.
