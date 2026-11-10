# Plan: Settings backup — export and import

## Context
The user wants one file that holds everything they configured: the genre/auto-add filter, the
Direct file channels, the News channels, their rules, mirrors, trust list and search switches. A
fresh install (the machine was just wiped) can then load it back. Import goes through the file
one channel at a time. A channel is added only if this install can really reach it. At the end
the panel shows a report of what was added, what was already there, what was skipped and why,
and what failed.

No export or import code exists today.

Decisions the user made:
- Settings in the file **replace** the current ones.
- Channels that are already present are left alone and reported as "already there".
- **Discovered** channels are exported and come back still marked as discovered.
- **Mirrors** are included. On import, every bot in the pool is tried until one is an admin.

## File format (`jellyfin-telegram-backup-YYYYMMDD.json`)
```json
{
  "format": "jellyfin-telegram-backup", "version": 1, "created": "...", "app_version": "...",
  "settings": {
    "direct_rules": { tgsource.Settings },
    "news_rules":   { newsfeed.Settings with user_id: 0 },
    "news_enabled": true,
    "auto_filter":  { autofilter.Filter },
    "env": { "TELEGRAM_SEARCH": "...", "TELEGRAM_SEARCH_*": "...", "TRENDING_SEARCH": "...",
             "TRENDING_MAX_PER_DAY": "...", "HASHTAG_INDEX_BOT": "...", "SUBTITLE_LANGUAGE": "...",
             "SUBTITLES_ONLINE": "...", "UI_LANGUAGE": "...", "UPLOAD_*": "...",
             "SIGNED_LIBRARY": "...", "INBOX_DELETE_ORIGINALS": "..." },
    "trust_keys": ["base64url ed25519 public key", "..."],
    "denied": [{"kind": "channel|key", "id": "..."}]
  },
  "direct":  [{"channel_id": 123, "username": "", "title": "", "enabled": true, "strict": false,
               "origin": "manual|discovered", "origin_note": ""}],
  "news":    [{"channel_id": 123, "username": "", "title": "", "enabled": true}],
  "mirrors": [{"channel_id": 123, "username": "", "title": "", "kinds": ["movie"], "bot_id": 0}]
}
```
- The `env` section is a fixed allow-list. Only keys that actually have a value get written.
- **Never exported:** bot tokens, API id and hash, account and bot sessions, `platform.key`, the
  gateway token, Jellyfin, Seerr, TMDB and arr keys, the main/home channel ids, and folder paths.

## Backend

### New package `internal/backup`
- `backup.go`: the `File` type and its sections, `Version = 1`, and `Parse(data) (File, error)`.
  `Parse` checks `format`, refuses a version newer than this build supports, and checks that
  `channel_id` values are positive.
- `export.go`: `Export(ctx, deps) (File, error)`. It reads from:
  - `cat.Sources` (`internal/catalog/sources.go:128`), `cat.NewsChannels` (`internal/catalog/news.go:70`)
    and `cat.Mirrors` (`internal/catalog/mirrors.go:104`);
  - the state keys `tgsource_settings` and `newsfeed_settings` through `cat.StateValue`
    (`sources.go:290`), clearing `user_id`;
  - `state["trust.keys"]`, or the trust store's own list method if `internal/sign/trust.go` exposes one;
  - `cat.AllTrust` (`trust.go:80`) for the rows where `denied` is set;
  - `autofilter.Load(envPath)` (`internal/autofilter/env.go:32`);
  - `.env`, read with `godotenv.Read` and kept to the allow-list.
- `import.go`: `Import(ctx, deps, File, step panel.Step) Report`. It runs in this order:
  1. **Settings.**
     - Validate with `Settings.Validate()` and `Filter.Clean()`, then save:
       - Direct rules with `tgsource.Service.Save` (`tgsource/service.go:120`).
       - News rules through the same path `saveNewsRules` uses (`cmd/jellyfin-telegram/sources.go:298`).
         Keep the `user_id` this install already has.
     - Write the filter and env keys with one `setup.UpdateEnv` call (`internal/setup/setup.go:465`).
     - Add the trust keys and the denied rows (`SetTrustDenied`, `trust.go:121`).
     - Each section gets its own report line, `ok` or `failed: reason`.
  2. **Direct channels, one at a time.**
     - If `cat.SourceByChannel` (`trust.go:175`) finds the channel, report "already there".
     - Otherwise, check access: `users.Lookup` (`internal/tguser/browse.go:192`) with `@username`,
       falling back to `-100<id>` if that fails or the username is empty, then
       `users.History(ctx, SourceRef{...}, 0, 1)` (`browse.go:234`) to prove the account can read
       the channel.
     - On success, add it with `tgsource.Service.Add` for manual channels or
       `cat.AddDiscoveredSource` (`trust.go:161`) for discovered ones, keeping `origin_note`. Then
       apply `enabled` and `strict` with `SetSourceEnabled` and `SetSourceStrict`
       (`sources.go:163,169`).
  3. **News channels.** Same checks.
     - The duplicate check scans `NewsChannels` by `channel_id`.
     - Add with `newsfeed.Service.Add` (`newsfeed/service.go:477`), then `SetNewsEnabled`
       (`news.go:94`).
  4. **Mirrors.**
     - Skip if a mirror with that `channel_id` already exists.
     - Build the list of bots to try from `pool.Statuses()` (`internal/tgpool/pool.go:121`). The
       file's `bot_id` goes first.
     - For each bot, call `pool.AcceptMirror(bot, channelID, username)` (`pool.go:396`). The first
       one that succeeds is used with `cat.AddMirror` (`mirrors.go:143`) and `SetMirrorUsername`.
     - If no bot works, report "no bot of this install is an admin of this channel".
- **Pacing and errors.**
  - Wait about 2 seconds between Telegram lookups.
  - On `FLOOD_WAIT` (`tgerr.AsFloodWait`), wait out the time if it is under 60 seconds and retry
    once. Otherwise mark that channel failed with the wait time.
  - On `tguser.ErrNoFreeSession`, or when no account is signed in, skip all remaining channels
    with the reason "no signed-in Telegram account". This is one clear reason instead of N
    identical errors. It mirrors `record()` in `tgsource/service.go:227`.
  - A channel ID that is the library channel is skipped.
  - Each per-channel error goes through the existing `friendly` (`tgsource/service.go:276`), so
    the report reads in plain words. If needed, export it from that package.
- **Report type.** Each item is
  `{section: "settings|direct|news|mirror", name, channel, status: "added|exists|skipped|failed", reason}`,
  plus counts per status. It goes back as the job `result`.
- `deps` is a small struct of interfaces: catalog, users, the tgsource and newsfeed services,
  pool, env path, trust store and library channel. Tests can then use fakes.

### Wiring (`internal/panel/panel.go`, `cmd/jellyfin-telegram/`)
- `panel.Options` gets two new callbacks:
  - `BackupExport func(ctx) (any, error)`
  - `BackupImport func(*http.Request, map[string]any) (any, error)`
- New routes, behind `p.guard`:
  - `GET /panel/backup/export` returns the JSON with a `Content-Disposition: attachment` filename.
  - `POST /panel/backup/import` starts a job through `runJob`. While another job runs it returns
    409 (`ErrBusy`).
- Add `/panel/backup` to `pagePaths` (`panel.go:165`).
- New file `cmd/jellyfin-telegram/backup.go`:
  - `jobs.backupImport(...)` uses `panelJobs.start` (`panel_jobs.go:61`). It pushes one `wait`
    line per item, which turns `ok` or `bad`, and returns the report as the result.
  - Wire it in `settingsPanel` (`serve.go:566-634`).
- Most `.env` keys are read only at startup. If the import changed any of those, the result
  carries `"restart": true` and the panel shows its existing restart banner.

## Web (`web/src/`)
- New page `Backup()` in `panel/pages.tsx`, with a route in `panel/main.tsx` and a `Nav` under
  the Access group in `panel/shell.tsx:135`.
- **Export section.**
  - A short note says what is included and that secrets are never included.
  - The button opens `/panel/backup/export?code=` + `panelCode()` (`lib/api.ts:18`), so the
    browser saves the file.
- **Import section.**
  - An `<input type="file" accept=".json">`. The file is read with `File.text()` and
    `JSON.parse`.
  - Before anything changes, a preview shows the counts per section and a warning that the
    settings will be replaced.
  - **Start import** calls `api("/panel/backup/import", parsed)`.
- **While it runs.** It shows `ProgressSteps` (`components/widgets.tsx:34`) from `useJob`
  (`shell.tsx:49`); extend its `result` type for the report.
- **Report** (once the job finishes):
  - A summary row of added, already there, skipped and failed counts.
  - A table grouped by section: name, @username or -100id, status badge, reason.
  - A "Download report" button saves the report as a JSON file.
- **Strings.** All text goes through `t()`. Add the keys to `web/src/locales/{en,fa,de,tr}.json`
  using the `web/scripts` helpers.

## Tests
- `internal/backup/backup_test.go`:
  - Export and then Parse gives back the same data.
  - The export has no secret keys: assert that `TELEGRAM_BOT_TOKENS`, `API_HASH` and similar
    never appear, even when `.env` has them.
  - Parse refuses a wrong format and a newer version.
- `internal/backup/import_test.go`, with a fake users manager and pool:
  - A channel that already exists is reported as `exists` and not touched.
  - A Lookup error means `failed` with the friendly reason, and the import carries on.
  - Lookup succeeds but History fails: `failed`, and nothing is added.
  - No session: every channel left is `skipped` with one reason.
  - A discovered channel comes back with `origin=discovered` and its note.
  - Mirror: the file's bot is not an admin but a second bot is, so the second bot is used.
  - Settings are replaced and `user_id` is kept.
- `internal/panel/panel_test.go`: the export route needs the code, and `/panel/backup` is in
  `pagePaths`.

## Verification
1. `go test ./internal/backup/... ./internal/panel/... ./cmd/...`, `go vet ./...`, and build the
   web app (`npm run build` in `web/`).
2. Run the app against the user's real setup in a single browser tab. Export, open the JSON and
   check by eye that no tokens or sessions are in it.
3. Import the same file. Everything should come back as "already there", with settings `ok`.
   This changes nothing.
4. In the test, remove one Direct channel, then import again. It should come back as `added`.
   Test with a channel the account cannot read; it should show as `failed` with a clear reason.
5. Run `graphify update .` afterwards. Don't commit unless asked.
