# Plan: remove the torrent download chain

Goal: the project no longer downloads anything by torrent. No Radarr, Sonarr, qBittorrent,
Bitmagnet or Prowlarr, in code, config, UI, docs or Docker. Everything else (Telegram storage,
`.strm` files, channels, subtitles, Seerr as a plain request page if we keep it) keeps working.

## Decisions to confirm first
1. **Seerr and Fladder (decided).** Keep Seerr as an optional extra and remove only the DVR
   wiring (Radarr/Sonarr). Requests then end in the Telegram search/forward flow
   (`internal/tgsearch/seerr.go`). **Remove Fladder too**: code, `FLADDER_MANAGED`, its
   Docker image, the `/fladder/` route, UI and README mentions. With Fladder gone, check whether
   anything still needs Docker; if not, drop the Docker requirement from the docs.
2. **`internal/autoupload`.** It mixes torrent-file handling (`DeleteTorrent`, `cleanup`) with
   the generic "upload a local file to the channel" path. Check what Channels, the shared library
   and Local media use before deleting any of it. Delete only the torrent-specific parts.
3. **Telegram user account.** The sign-in on the Torrents tab is also used by Channels and
   search. It stays. Only the "download" use goes.

## Phase 0: map what depends on what (no edits)
- `graphify query` / `graphify path` for each symbol below, then list every caller.
- Files that mention torrent tooling (from grep): `.env.example`, `cmd/jellyfin-telegram/serve.go`,
  `docs/architecture.md`, `internal/autoupload/*`, `internal/catalog/uploads.go`,
  `internal/config/config.go` (+test), `internal/downloads/service.go`, `internal/front/front.go`,
  `internal/home/service.go`, `internal/seerr/dvr.go`, `internal/seerr/release.go`,
  `internal/stack/*` (docker, files, images, qbit, runner, search, seerrsync, servarr, password),
  `web/src/panel/pages.tsx`, `web/src/panel/shell.tsx`, `web/src/locales/{en,fa,de,tr}.json`,
  `web/scripts/patch-locales.mjs`, `README.md`.
- Built UI under `internal/webui/dist` is generated: rebuild it, do not hand edit.

## Phase 1: remove the code
1. Delete `internal/downloads`, `internal/seerr/dvr.go` (+test), `internal/seerr/release.go`.
2. In `internal/stack`: delete `qbit.go`, `servarr.go`(+test), `search.go`, `seerrsync.go` (if it
   only syncs DVR settings), the Bitmagnet/qBittorrent/Radarr/Sonarr parts of `runner.go`,
   `docker.go`, `images.go`, `files.go`, `password.go`(+test), `limits_test.go`, `live_test.go`.
   If nothing is left that needs Docker (Fladder needs it), say so in the docs and keep only that.
3. In `internal/autoupload`: remove the torrent source and `DeleteTorrent`/cleanup of downloaded
   copies; keep generic upload only if another feature calls it, otherwise delete the package.
4. Remove the wiring in `cmd/jellyfin-telegram/serve.go`, `internal/front/front.go`,
   `internal/home/service.go`, `internal/catalog/uploads.go`.
5. Config: drop every download setting from `internal/config/config.go` and `.env.example`
   (`DOWNLOADS_*`, qBittorrent, Servarr, Bitmagnet keys). Old `.env` files that still contain them
   must not break startup: ignore unknown keys and log one warning.
6. Migration: if a data folder holds old qBittorrent/Servarr data, leave it alone and mention it
   in the release notes. Do not delete user data automatically.

## Phase 2: remove the UI
- Remove the Torrents tab and its routes from `web/src/panel/pages.tsx` and `shell.tsx`.
- Remove download strings from all four locale files and `patch-locales.mjs`.
- `npm run build` in `web/`, commit the regenerated `internal/webui/dist`.

## Phase 3: docs
- README: delete "Downloads (Torrents tab)", the Seerr to Radarr to qBittorrent mentions
  (lines about the "torrent download chain", ~99 and ~175), and the Contents entry if any.
- `docs/architecture.md`: remove the download chain section.
- Add a short note in the release notes: "Downloads were removed. Existing installs keep working;
  old download settings are ignored."

## Phase 4: verify
- `go build ./...`, `go vet ./...`, `go test -race ./...`.
- `grep -ri -E "torrent|radarr|sonarr|bitmagnet|qbittorrent|prowlarr"` over source, config, docs
  and locales returns nothing (except the release note).
- Start the service on a copy of config with old download keys set: it starts, warns once, no panic.
- Panel loads without the Torrents tab; Channels, search and Local media still work.
- `graphify update .`

## Separate follow-up: channel and hashtag index off by default
- Default off. Turned on by a switch in the panel **and as an unticked option on the setup
  page**; both show the same short notice (own or licensed content, you are responsible,
  Telegram terms) and need one confirm click.
- **The Seerr to Telegram chain stays.** A Seerr request is searched in Telegram (public
  channels, hashtag/channel index) and forwarded into the library, using `internal/tgsearch`
  and the Seerr webhook. Only the DVR side (Radarr/Sonarr/qBittorrent) is cut. When the switch is
  off, Seerr requests simply find nothing in Telegram; when on, they work as today.
- Verify in Phase 4: with the switch on, a Seerr request still finds and forwards a file;
  with it off, no Telegram search or forward happens.
- Applies to channel index and hashtag index lookups and any forwarding that depends on them.
- Existing installs: keep the current state, or switch off with a release note. Decide when built.

## Not in this plan
- The `seerr-bundles` repo (Seerr stays).
