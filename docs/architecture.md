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
| `internal/front` | One port for streams, the setup page, the panel, Seerr under `/seerr/`, Fladder under `/fladder/`, and a reverse proxy to Jellyfin |
| `internal/setup` | Browser wizard that writes `.env` after checking the bots against the channel |
| `internal/panel` | Settings pages: channel, mirrors, bots, sources, local files, downloads |
| `internal/webui` | Embedded built panel and setup pages. Scripts and CSS are served at `/ui/` |
| `web/` | Vite and React source for those pages. `pnpm build` writes `internal/webui/dist`, which is committed so a Go build needs no Node |
| `internal/tguser` | User-account sessions that post a finished download into the channel. Separate from the bot pool |
| `internal/stack` | qBittorrent, Radarr, Sonarr, Bitmagnet and Postgres, started on demand and configured over their APIs |
| `internal/autoupload` | Polls Radarr and Sonarr, uploads one file per session, and deletes the disk copy after a probe |
| `internal/downloads` | Panel switch. A failure here is logged and does not stop playback |
| `internal/tgsource` | Walks other Telegram channels with a signed-in account and forwards films and series into the library channel server side. Progress and what was copied live in the catalog |
| `internal/newsfeed` | Watches a separate list of channels that post bot links, asks telegram-file-resolver to open each link, and forwards a video that passes the copy rules |

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

## Downloads

Off by default. The panel turns it on only when at least one signed-in user is an admin who can post, Docker is installed, and the download folder is writable with enough free space. The stack runs in a goroutine after the player is already serving, and an error there is shown in the panel.

qBittorrent, Radarr and Sonarr share one bind-mounted folder (`torrents/`, `media/movies`, `media/tv`) so an import can hardlink. Their databases stay on named volumes. Bitmagnet and Postgres share the Docker network `jellyfin-telegram`. Seerr reaches Radarr and Sonarr through the published loopback ports. `/radarr/` and `/sonarr/` are reverse-proxied on the same port as Jellyfin. qBittorrent's WebUI and Bitmagnet's HTTP port stay on loopback. The DHT port and qBittorrent's peer port are published, because otherwise nothing arrives.

The uploader polls Radarr and Sonarr every minute. Each file is posted by one user session, under that session's own pace and daily cap. A bot then reads the message back and compares the size and the first and last block with the local file. The local file and the torrent are deleted only after the catalog row is probed, so Seerr still sees the title as available. A crash after the send is caught by searching the channel for the same name and size before posting again. That search runs once per interrupted file, never for a fresh one, and a failed search stops the upload instead of counting as "not found".

Rules the uploader keeps:

- **Nothing is deleted on a doubt.** A file already in the channel under the same name and size is still read back and compared. A real difference sets the row to `mismatch`, which nothing moves on from. A network error only retries.
- **Size limits.** The cap is megabytes per minute of runtime in Radarr and Sonarr, so it is divided by 150 minutes for films and 60 for episodes, and it is rewritten whenever the largest account changes. A file that still exceeds what every account can send is marked `too_big` and stays on disk.
- **The torrent is found from the grab history** before the file is deleted, and only a record naming that movie or episode counts. A file holding several episodes unmonitors all of them.
- **The disk guard runs on every pass**, including when setting up the apps fails. It stops the torrents below the free-space minimum and later starts only those, remembered by a marker file in the download folder.
- **Sessions reconnect** with a growing delay after a dropped connection, but not after a revoked session or a lost admin right. A panel request that arrives with forwarding headers is not treated as this computer.
- **Missing titles are searched for again** every `DOWNLOADS_SEARCH_EVERY_MINUTES` (60), by the commands `MissingMoviesSearch` and `MissingEpisodeSearch`. Radarr and Sonarr search once when a title is added and afterwards read only the newest items of an indexer's feed. Bitmagnet adds hundreds of torrents a minute and learns of a release at an unpredictable time, so without the repeat a title it did not know yet would never be looked for again. It only runs while an account is ready to upload and the disk has room. Both commands were checked on the live servers.
- **Bitmagnet is not a search engine.** It stores what it overhears on the DHT, so a search can only find torrents it has already seen. Recent releases are about three to six times as dense in its index as releases from the 2000s, and single episodes outnumber whole seasons. A film that is still in cinemas is not searched for at all while its minimum availability is `released`.
- **Containers are removed when the program ends**, so nothing keeps downloading unwatched.

Checked against live containers (`go test -tags live ./internal/stack` with `JFTG_LIVE_DIR` set): the five containers start, Radarr and Sonarr get a root folder, one download client, an enabled Bitmagnet indexer and the size caps, qBittorrent accepts `max_ratio=0`, a hardlink across the bind mount on Windows has two links, and a second or forced pass changes nothing. Two things only a real server showed: the quality definition update answers 202 and applies a moment later, and the default Ultra-HD profile allows nothing but 2160p, so only the profile Seerr uses is changed.

End-to-end run on 2026-11-04 with Sintel (CC-BY, 129 MB) and one signed-in non-Premium account:
Seerr request, Radarr, qBittorrent (web seed, about 20 s), import with a hardlink, upload at 8 MB/s
(15 s), a bot reading back the first and last block (`verified`), the indexer, Jellyfin's probe, then
cleanup. Afterwards Radarr had no file and was unmonitored, the torrent and the local file were gone,
Jellyfin listed the film (14.8 min, h264, aac, a subtitle track), range reads through the gateway
answered 206 at both ends, and Seerr showed it as available. Radarr removed the finished torrent
itself after the import, so the program's own torrent deletion is a second safety net.

Four more things only the live run showed:

- A queue that goes quiet must say why. A file now carries "waiting: @account rests for 4m" when no
  account can take it.
- An upload cannot run under the context of the pass that started it. A pass lasts seconds and an
  upload minutes, so every upload died with its pass. Uploads now have their own context, which ends
  when the switch is turned off or the program stops.
- Seerr's Node gives each connection attempt 250 ms. Reaching a published loopback port through
  Docker Desktop is sometimes slower, so Seerr failed at random with an empty error, even towards
  Jellyfin, and once stopped the whole program during setup. Seerr now runs with
  `--no-network-family-autoselection` and reaches Radarr and Sonarr by container name over the
  shared network. Its setup is retried five times.
- Updating a server in Seerr must not send its id in the body. Seerr refuses it as read-only.

## Shared library

Off by default (`SIGNED_LIBRARY`). Several installations, each with its own Jellyfin and bots, can read one
channel. Files that people post are posted again by a bot with a caption that every installation can read,
so a title is identified once and its subtitles are extracted once. With it on, only such posts are indexed.

The state of a file is the **first line of its caption**, so anyone who opens the channel sees it, and nothing
else is posted:

| On | Status | Meaning |
|---|---|---|
| raw file | 🔎 `review` | a bot is identifying the file. A signed claim line sits under it |
| raw file | 📤 `indexed` | its signed post exists |
| raw file | ⛔ `rejected: reason` | it could not be identified |
| signed post | 💬 `subtitles` | its subtitles are being prepared. A claim line sits under it |
| signed post | ✅ `ready` | finished. Only these are copied to mirrors |

A signed post looks like this. What a person wrote on a raw file stays below its status line.

```
✅ Ready
The Hardacres
🎬 Episode · TMDB 231751 · IMDb tt28489780 · 480p
🔊 Audio: fa (dubbed) · en (original)
💬 Soft: fa (srt) · Hard: unknown
jftg:2;k=e;t=231751;i=tt28489780;q=480p;se=1;ep=6;fu=<file_unique_id>;h=<content hash>;oh=<OpenSubtitles hash>;du=2760;ss=fa.srt@3;au=fa.dub@1,en.orig@2;hs=unknown;sa=yes;st=ready;ah=<archive hash>;d=<doc>;z=<size>;pk=<public key>;s=<signature>
#tm_tv_231751_1_6_fa_fa
```

The line says what a reader can check for itself, and where to look:

- `fu` is Telegram's `file_unique_id`, so a bot that speaks only the Bot API can match the file. It must agree with `d`.
- `h` and `oh` name the content, from the first and last blocks of the file (`internal/fileid`). They survive a new
  upload, which `d` and `fu` do not. `oh` is the OpenSubtitles hash, so subtitle sites can be asked too.
- `du` is the running time, and `ss` lists the soft subtitle tracks with their stream numbers (`fa.srt@3`), so a reader
  need not probe the file to find them.
- `au` lists the audio tracks as `lang.role@stream`. The role is `dub` for a track in another language than the film or
  series was made in, `orig` for the original, and left out when nobody can tell. The original language comes from
  Seerr (TMDB) when it runs; a tag in a track title ("Dubbed", "Original", "دوبله") wins over it. Without either,
  only the languages are listed. A post with several audio tracks or several subtitle tracks says so in these two
  fields, and a line without `au` is read as before.
- `ah` names the subtitle archive that answers the post, so it is checked after it is downloaded.
- `pk` is the key that signed the line. It travels with the line, so a post that is only forwarded, with no sender and
  no reply link, still verifies anywhere.

The hashtag ends the caption: `#tm_m_<tmdb>` for a film, `#tm_tv_<tmdb>_<season>_<episode>` for an episode.
When the tracks are known, a language suffix follows: audio, then subtitle, as lower-case ISO 639-1
codes where a two-letter code exists (`#tm_tv_231751_1_6_fa_fa` is Persian audio and Persian subtitles).
Audio with no subtitles stops after the audio code (`…_en`). Subtitles with an unknown audio language
write `none` in the audio slot (`…_none_fa`). Neither known leaves the original hashtag, with no suffix.
The audio code is the first audio track. The subtitle code is the preferred subtitle language when that
track is present, otherwise the first subtitle track. The signature
covers every field, including the document id, the size and the status, so a caption copied onto another file fails, and a post cannot be
marked ready without the key. Forwards and copies keep the document id, so a mirror copy still verifies.

**A claim is an edit of the caption.** A bot that takes a file edits the first line to the work's status and
signs a claim line under it: `jftg-claim:1;op=review;m=<message>;ttl=7200;pk=<key>;s=<signature>`. The claim carries
its own public key, so any installation can check it without a list of keys. It lapses `ttl` seconds after the
edit date of the message. A claim written now does not win yet: the next pass reads the caption again, and the claim
that is still there is the one that counts. Two installations that edit at the same moment therefore agree, because
a caption holds one. Subtitle work claims the signed post in the same way. When it is done the post is signed again as
`ready`. One pass takes up at most five files.

`internal/mirror` copies a post only when its status line and its signed line both say ready. The indexer reads
`subtitles` posts again on every sync tick (`RefreshStatus`), so the catalog sees `ready` soon after a bot writes it.

### The subtitle archive has a caption too

The archive is posted as a reply to the signed post, and its caption says what it is:

```
💬 Subtitles: The Hardacres
jftg-subs:1;vd=<video document id>;vh=<video content hash>;ah=<archive hash>;ss=fa.srt@3;pk=<public key>;s=<signature>
#tm_tv_231751_1_6_fa_fa
```

`vd` and `vh` tie it to its video, so it still finds the video after a forward, a copy or a new upload. `ah` names its
own bytes: `ah` of the video post and of the archive agree, and a reader checks the downloaded bytes against it before it
installs anything, so a wrong archive from another installation is ignored with a warning. It carries the same hashtag
as the video, so one search finds both. An archive is also found by the document id at the end of its file name, not by
the message id of the video.

A mirror copies a ready video together with its archive, one right after the other. A post that says it has an archive
(`sa=yes`) waits until the archive has been seen. An archive that shows up after its video went is sent later.

### Nobody vouches for a key: the post is checked against its file

A signature proves who signed, not that the line is true. So an installation does not keep a list of keys
(`internal/sign`, `Checker`). It looks at the file instead:

1. The signature must verify under the key in the line, and the line must name this document and size.
2. This installation's own key is believed. A key added by hand is believed too, for those who want that.
3. Any other key: the post is compared with its file. That reads the first and last block (two or three blocks) and the
   head of the file: the content hash, the OpenSubtitles hash, the running time and the subtitle tracks must be what
   the caption says. The result is kept, so the same post is checked once.
4. A key earns a record. After 20 posts that were all right, only about one in twenty is checked. A key with three wrong
   posts is ignored for a day. A check that could not be made, because Telegram did not answer, is not a verdict and
   changes nothing.

### What was measured on real Telegram (2026-11-06 and 2026-11-07)

- **A bot cannot read another bot's message in a supergroup.** `channels.getMessages` returns it only to the bot that
  posted it. Turning Group Privacy off for the reader did not help. Telegram's FAQ says bots do not see other bots'
  messages in groups, and the newer Bot-to-Bot Communication Mode is documented for updates only. So a library in a
  supergroup, or in its topics, cannot be shared by bots. A bot also cannot turn topics on (`toggleForum`) or list them
  (`getForumTopics`).
- **In a channel there is no such limit.** All four bots read every subtitle archive that a bot had posted in the real
  library channel, and the sender of a channel post is anonymous.
- On the real channel, with a scratch catalog: bots **edit the caption of a file a person posted**; the edit date comes
  back and a claim written in one pass is recognised in the next; a file went `review` to `indexed` in two passes; and
  `sendMedia` with the document **keeps the same document id**, so a repost is the same file. A full caption was 346
  characters.
- Telegram's `file_unique_id` is the document id with a type, so it equals the id the bots read over MTProto.
  `internal/fileid` computes it, and the content hashes.
- **A second installation with another key**, which had never seen the first, checked three posts against their files
  ("checked against the file, right") and indexed them with no title lookup. A post with a valid signature and a wrong
  content hash was refused by both installations and never counted. A mirror of that second installation copied
  only the post that was `ready`.
- Jellyfin answers a series search with two entries for one show: the TMDB entry has a name in the server's metadata
  language, and the entry with the original name has only an IMDb id. `pickMeta` joins entries of the same year. Before
  that a file named after its show was rejected as unidentified.
- Three qualities of one episode were each extracted once (260, 445 and 828 MiB), archived with a signed caption, and
  marked ready. A second installation then mirrored each video followed by its archive two seconds later. All three
  archives have the same `ah`, because the subtitle text is the same.
- Versions of one episode: after a probe the alternate version shows its URL, not its path. A match by URL now counts
  when the item is in the file's own folder, so the subtitle worker no longer skips it.

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
- The real upload speed of a user session, which may change the default pace.
- A film found by Bitmagnet itself. Its index starts empty and no release has been found by search yet. The release in the end-to-end run below was handed to Radarr directly.
- Series: Sonarr's import, season packs, a file holding two episodes, and `episode?episodeFileId=` on a real import. The film path is checked, the series path only has unit tests and request-shape checks.
- A Premium account, a file larger than the account can send (`too_big`), flood waits, and long sessions.
- A `mismatch` after a real corrupted upload. It has a unit test only.
- Seerr, inside its container, reaching Radarr and Sonarr on the loopback ports through `host.docker.internal`.
- A bot reading a Premium upload larger than 2 GB.
- The download stack on Linux. It is built with `CGO_ENABLED=0`, and the containers are the same images.
