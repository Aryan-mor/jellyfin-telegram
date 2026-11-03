# Architecture

## Components

| Package | Job |
|---|---|
| `internal/tgpool` | One MTProto connection per bot. Schedules every read, benches bots in flood wait |
| `internal/gateway` | HTTP range server. Combines the cache, read-ahead and Telegram reads |
| `internal/blockstore` | 1 MiB blocks on disk: an LRU area plus a pinned area |
| `internal/indexer` | Walks channel message ids and keeps the catalog and library current |
| `internal/naming` | Turns a file name and caption into a library path |
| `internal/library` | Writes `.strm`, `movie.nfo` and `poster.jpg` |
| `internal/catalog` | SQLite index of files |
| `internal/jellyfin` | API client and the prober that prepares new items |
| `internal/subtitles` | Identifies tracks from the file head and finds online subtitles |
| `internal/lang` | Language codes, names, scripts, encodings and text detection for any language |
| `internal/managed` | Downloads a pinned Jellyfin build, runs it on loopback, completes its first-run wizard |
| `internal/seerr` | Runs a pinned Seerr image and finishes its setup with that Jellyfin as the media server |
| `internal/fladder` | Runs the Fladder web client with this Jellyfin and Seerr already filled in |
| `internal/front` | One port for streams, the setup page, Seerr under `/seerr/`, Fladder under `/fladder/`, and a reverse proxy to Jellyfin |
| `internal/setup` | Browser wizard that writes `.env` after checking the bots against the channel |

## Reading a video

1. A client or ffmpeg requests a byte range from `/s/{token}/{channel}/{message}`.
2. The gateway maps the range to 1 MiB blocks and serves each from disk or Telegram.
3. Concurrent readers of one block share a single Telegram call.
4. After two consecutive blocks, read-ahead fetches the next `GATEWAY_READAHEAD_BLOCKS`.

Each block is scheduled on its own, so one stream can read from every bot at once. This is
how extra bots add speed. Whether Telegram throughput really scales this way must be
measured with `bench`.

### Telegram rules this relies on

- `upload.getFile` reads at most 1 MiB and must stay inside one 1 MiB chunk. The `precise`
  flag permits any 1 KB aligned offset within it.
- File references belong to the account that received them, so each bot looks up each
  message itself and refreshes on `FILE_REFERENCE_EXPIRED`.
- `messages.getHistory` is closed to bots. `channels.getMessages` is open, so the indexer
  walks ids in batches of 100.
- `gotd` follows `FILE_MIGRATE` itself. CDN support is not requested, so Telegram never
  answers with a CDN redirect.

## Measured Jellyfin behaviour

Measured on Jellyfin 12.4 using a logging range server and synthetic test files. These
results decide the metadata design and should be measured again after a major upgrade.

| Action | Requests to the stream URL | Bytes |
|---|---|---|
| Library scan and rescan | 0 | 0 |
| `POST /Items/{id}/Refresh` with FullRefresh | 0 | 0 |
| Chapter, trickplay and keyframe tasks | 0 | 0 |
| `PlaybackInfo` on an MKV | 1 | 1.25 to 2.25 MiB |
| `PlaybackInfo` on an MP4 with `moov` at the end | 3 | 3.75 MiB |
| Range request through `/Videos/{id}/stream?static=true` | 1 | Exactly the range |

Byte counts are what was written to the socket, an upper bound on what ffprobe consumed.

Other findings:

- `movie.nfo`, `tvshow.nfo`, an episode `.nfo`, `poster.jpg`, `fanart.jpg` and `-thumb.jpg`
  are all honoured next to a `.strm` file. Plain text and Markdown files are not read.
- `<streamdetails>` in an NFO does not fill in streams or runtime. Only a probe does.
- `[tmdbid-N]` in a folder name is enough for full TMDB metadata and images.
- `PlaybackInfo` publishes the raw URL in `Path` with `SupportsDirectPlay: true`, and the web
  client treats remote sources as reachable. The URL must therefore work from clients, not
  only from the server.

## Probing without a player

A library scan does not probe a `.strm` file, so a new item has no runtime, codecs or audio
tracks. The metadata refresh API does not probe either, but `PlaybackInfo` does. So after
each scan the prober calls `PlaybackInfo` once per new file, with no player attached, and
Jellyfin stores the result.

Jellyfin repeats that probe on every later `PlaybackInfo`. To keep it off Telegram, blocks
read during the prober's call are pinned and never evicted. Read-ahead is skipped while a
reader stays inside pinned blocks, because otherwise each repeat probe would look like
sequential playback and fetch extra data.

## Decisions

**`.strm` files, not a virtual drive.** A virtual drive needs FUSE or WinFsp. `.strm` works
the same on Windows and Linux with no kernel driver. The costs are the probe handling above
and the URL being visible to clients.

**`gotd/td`, used directly.** It is pure Go, so the service cross-compiles with CGO off. It
needs no native TDLib build per platform, and `gotgproto` would only add a beta wrapper.

**A private Jellyfin, not a fork.** Setup has to be one program and one port. The binary
downloads a pinned official build, listens with it on loopback, and reverse-proxies
everything else. A fork would mean rebuilding Jellyfin on every release. Stream addresses in
API answers are rewritten to the host the client actually used, so the same port works on the
LAN and through a tunnel. Public port mapping stays off.

**Sessions on disk.** Each bot keeps a session in `DATA_DIR/sessions`, so a restart does not
log every bot in again.

**Library paths are stable.** An edited name or caption keeps the existing path, so Jellyfin
keeps the item and its watch state. A replaced document keeps the path and is probed again.

**Mass deletion is refused.** If every known message looks deleted at once, the library is
left alone, since that points at a wrong channel or a Telegram fault.

## Measured on real Telegram

One run against a private channel with 4 bots, 4 videos and 4.4 GiB in total, with Jellyfin
12.4 in Docker on the same machine. Treat these as one sample, since speed depends on the
network and on Telegram.

| Measurement | Result |
|---|---|
| `bench` with 1 bot | 3.2 MB/s |
| `bench` with 4 bots | 11.7 MB/s, including about 5 s before the first block |
| Sustained read through Jellyfin, 4 bots | 16.6 and 19.1 MB/s in two runs |
| First byte at an uncached position | 0.29 and 0.57 s median, worst 1.05 s |
| Reading 4 MiB after a seek | Usually 1.1 to 2.3 s, one outlier of 6 s |
| Repeat `PlaybackInfo` with pinned blocks | 0.10 to 0.47 s |
| First HLS transcode segment | 0.5 s |
| Probing all 4 videos | 14 MiB read out of 4.4 GiB |
| Flood waits | 0 |

A bot's first read has to open a connection to the file's data center. `bench` shows this as about
5 s before its first block. The service now makes one small read with every bot at startup, and the
first bytes after a restart then took 0.28 s at uncached positions.

Telegram sometimes returns less than the requested block, so `FetchBlock` joins short
answers. `bench` made 277 calls for 256 blocks.

## Playback in a real client

Tested with Jellyfin 10.11.8 and its web client in Chrome. The relied-on behaviours are also
present in the v10.11.8 source: range forwarding, probing on `PlaybackInfo`, and no probe on
a metadata refresh.

- An H.264 MKV played with `DirectPlay` in real time, so the browser read from the gateway.
- Starting that file at 52 minutes began at once from the requested position.
- An MP4 with anamorphic video played as `Transcode` with reason
  `AnamorphicVideoNotSupported`. Video and audio were copied, so this was a remux.
- Stopping playback stopped ffmpeg and all Telegram reads.

With one tab, seeking to 3 positions resumed playback in 1.3 to 1.6 s each.

The same check on Jellyfin 12.4, served by the managed copy through the one port: one Chrome
tab, DirectPlay of the H.264 MP4, `video.seekable` covering the whole duration, and
`Playing/Seek` moving the picture from 30 minutes to 15 minutes. A range request to the
gateway returned `206` with `Accept-Ranges: bytes`.

Several open tabs of the web client share one session, so their position reports mix and
commands reach only one tab. Use a single tab when testing, or seeking appears broken.

### Reads of the whole file

**Subtitle extraction.** When a client asks for an embedded text subtitle as a separate document,
Jellyfin runs ffmpeg over the whole file. One 960 MiB MKV took 69 s unlimited. With an `Embed`
subtitle profile, `PlaybackInfo` reports delivery `Embed`, gives no subtitle URL and starts no
extraction, but Jellyfin Web and stock Fladder ask for `External`. See the subtitle archive below.

**Remux and transcode.** ffmpeg reads as fast as it can, not at playback speed. Jellyfin's Throttle
Transcodes setting does not help: its code requires a local input file, and a measured remux read
435 MiB in 45 s with it off and 575 MiB with it on.

So the gateway limits ffmpeg, recognised by its `Lavf` user agent: a 64 MiB burst, then three times
the file's average bitrate with a floor of 2 MiB/s. The budget belongs to the file, not the
connection, because ffmpeg reconnects whenever it seeks. It resets after a minute without reads. Probes are exempt. On a 5 Mbit/s file this read
148 MiB in 45 s with no late segment. An MP4 with its index at the end read 194 MiB in 31 s, and the first remux segment arrived in about 1 s. The burst can cause short flood waits. Blocks already in the cache are not limited, because they cost
Telegram nothing.

## Subtitles

The approach comes from Sushi: subtitles are metadata, not content. A server only needs to know
which languages a file has, and the first bytes of the file answer that.

1. ffprobe reads the container header through the gateway. Those blocks are pinned, because
   Jellyfin reads the same bytes on every `PlaybackInfo`.
2. Tracks tagged English or left untagged may be mislabeled. Up to four are checked against
   `SUBTITLE_LANGUAGE` by saving the first 12 MiB to a temporary file and running ffmpeg on it, so
   it cannot read further.
3. The rule in the README decides whether to fetch an online subtitle. The picker takes the matching
   season and episode, and otherwise prefers a full dialogue track from the closest release.
4. The subtitle is sent with `POST /Videos/{id}/Subtitles`, then the first `PlaybackInfo` runs.

The order in step 4 is required. `MediaSourceManager` holds a singleton `DirectoryService` whose
folder cache never expires, and every `PlaybackInfo` on a `.strm` refreshes the item from that cache.
A subtitle added after the first `PlaybackInfo` stays invisible until Jellyfin restarts. With
`SaveSubtitlesWithMedia` off, each upload goes to a metadata folder belonging to that one item, so a
new episode works even when its season folder is already cached.

No language is assumed. Everything specific to one comes from `internal/lang` or from the setting,
so a change of language only changes data. Two details are worth knowing:

- The sub-plus.ir search is fuzzy even for an IMDb id: `tt0111301` also returned `tt0111701` and
  `tt0117301`, which are different films. Only a pack whose own id matches is accepted, and a
  picked `.srt` must also end between 60% and 115% of the running time. One pack for a 102 minute
  film stopped at minute 59. An unknown `l` value is also not an error upstream, as it silently
  answers in Persian, so only documented values are sent.
- The language detector is reliable on a few kilobytes and wrong on whole files, where it called
  Persian Uyghur. Its input is capped at 4000 bytes.

Measured with real channel files, with outcomes for each language:

| File | fa | en | de | fr | ru | ko |
|---|---|---|---|---|---|---|
| Street Fighter, no subtitle tracks | online | online | not found | online | not found | off |
| Where the Mask Ends, 33 tracks without Persian | not found | embedded | embedded | embedded | embedded | embedded |

Measured on Jellyfin 10.11.8:

| Path | Time | Telegram reads | ffmpeg extraction |
|---|---|---|---|
| Extraction from an embedded track | 69 s | 960 MiB | Yes |
| Uploaded online subtitle, 1039 cues | 0.28 s | None for the subtitle | No |

Jellyfin marks an uploaded subtitle as available, not as the default, so showing it automatically
depends on the user's subtitle preferences.

### Subtitle archive

Identifying tracks needs only the head of a file, but their text is spread through all of it. So
each file is read in full exactly once, in the background, and the result is kept in the channel.
`internal/subarchive` handles one video at a time and takes the cheapest source available:

1. An archive already in the channel, which needs no video read.
2. Tracks Jellyfin already extracted, found complete in its cache.
3. A single ffmpeg pass over the file, sent with the `X-Jftg-Background` header. The gateway limits
   that request to `SUBTITLE_EXTRACT_MB_PER_SEC` and holds it for 20 s after any other read.

The tracks go to `<data>/subtitles/<first 2 of id>/<dashed id>/<index>.<ext>`, which is where
Jellyfin looks. Four details matter:

- **All extractable tracks are needed.** On a request for one embedded track, `SubtitleEncoder`
  extracts every track missing from the cache.
- **The index moves.** Each external subtitle shifts the index Jellyfin shows for embedded tracks, so
  archives store the position in the file and the index is worked out when installing.
- **Empty files count as missing.** A track with no cues gets a placeholder.
- **The `.strm` modification time must not change.** When it does, `MetadataService` deletes the
  extracted data of that item. It then saves the old time from its singleton folder cache, sees a
  change again, and deletes again on every `PlaybackInfo` until Jellyfin restarts. The service keeps
  the old time when it writes a new address. A check at startup and every hour restores anything
  missing from the channel.

A track in the user's language below 2 cues a minute is treated as hollow. Normal dialogue runs near
10 a minute. A real file had 50 cues in 71 minutes, and its track was replaced by a 1105 cue
subtitle. The replacement is applied again on every restore.

Measured with 4 real files, on Jellyfin 10.11.8:

| Measurement | Result |
|---|---|
| Extracting from a 657 MiB file at the 3 MB/s limit | 4 min 11 s, one track |
| Archive of a file with 33 tracks | 726 KB |
| Restoring 3 files from the channel after the cache was lost | Under 3 s, 0 MiB of video read |
| Fetching all 33 tracks through Jellyfin afterwards | 3.6 s, 0 video blocks, 0 extractions |
| Replacing the gateway token | 0 cache deletions, 0 extractions, old token refused |
| Playing in Web with an embedded track selected | No extraction |

## Not verified yet

- Dragging the seek bar by hand. Seeking was tested with the remote seek command in one tab.
- Playing an `Embed` track in a real player such as mpv. Only the absence of extraction was measured.
- TV and mobile clients.
- A high-bitrate file. The largest bitrate tested was about 5 Mbit/s. The measured speed
  is above what a 100 Mbit/s remux needs, but that has not been played.
- Image subtitles such as PGS, and subtitle files posted to the channel, on real data. The channel
  test file had none of either. Matching posted files has unit tests only.
- Versions of one episode, which are written with a `tg<id>` suffix.
- Long sessions, where `FLOOD_PREMIUM_WAIT` may appear after tens of gigabytes.
- A whole library. Extraction has run on 3 files.
