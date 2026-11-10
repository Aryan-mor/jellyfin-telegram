# Plan: Manager bot in jellyfin-telegram

## Context
In setup and in the panel, the user picks one bot from the pool as the **manager**.

- **When idle,** it serves reads like the other pool bots, at lower priority.
- **Unlike the others,** it answers DMs, but only from allowed people: every signed-in account plus the ids and usernames listed in the panel.

What it does:
- adds and removes "news" and "direct" source channels, and changes some panel settings;
- searches the library and sends the file;
- resolves file-bot links in a forwarded post and returns the file, with an **Index this** button for videos.

A provider rewritten from sushi is a **future** phase and is only sketched here (P5).

## How the manager gets updates
**Recommended: Bot API long polling (`getUpdates`)** on the manager's token, next to its existing MTProto pool session.
- **Why:** keyboards, callbacks and `copyMessage` to a user are simple, and it is easy to test with httptest. It can reuse `botapi` from telegram-hashtag-index.
- **Not checked:** that Telegram allows both on one token. The P1 spike settles it.
- **Fallback:** remove `NoUpdates` (`internal/tgpool/bot.go:173`) for the manager only and add a gotd `updates.Manager`.
- Never call Bot API `logOut` or `close`.
- **Two instances on one token:** `getUpdates` returns 409. Log it, stop the loop, and don't crash serve.

## New package `internal/managerbot`
```
loop.go     getUpdates loop, offset in catalog state table
auth.go     allowed(fromID, username)
router.go   commands + callback dispatch, per-user limiter
sources.go  SourcesPort
library.go  LibraryPort + browse keyboards
deliver.go  copyMessage to user, optional auto-delete
resolve.go  ResolvePort, staging, "Index this"
text.go     i18n tables (en default, fa)
```
The wiring goes in a new `cmd/jellyfin-telegram/manager.go`, started from `serve.go` next to `startSources`/`startNews`.

## Pool priority
- In `internal/tgpool/pool.go`, add `low bool` to `slot`.
- `tryAcquire` makes two passes: the first skips low slots, the second includes them.
- A low slot always keeps one call free for DM work.
- `newPool` (`cmd/jellyfin-telegram/main.go`) calls `Pool.SetLow(botID)`.

## Settings and access
**New `.env` keys:**
- `MANAGER_BOT_ID`: the bot's numeric id, i.e. the token prefix, so the token isn't stored twice. Empty means the feature is off.
- `MANAGER_ALLOW`: ids and `@usernames`.
- `MANAGER_STAGING_CHANNEL`.

**Who is allowed:**
- The ids from `tguser.Manager.Views()` plus `MANAGER_ALLOW`.
- This is recomputed on every update, so a newly signed-in account works right away.
- A username is stored as its id the first time it matches.
- Everyone else gets silence.

**Reloading:**
- `MANAGER_BOT_ID` needs a restart, like bot changes today.
- `MANAGER_ALLOW` reloads live.

**Panel and setup:**
- The Bots page (`Bots()` in `web/src/panel/pages.tsx`) gets a "Manager bot" dropdown, reusing `botOptions`, and an allow-list field.
- The backend side is in `internal/panel/panel.go` (`readFile`/`fileSettings`, `snapshot`).
- The setup prefs step (`web/src/setup/main.tsx`) gets the same dropdown, saved in `internal/setup/setup.go` `handleSave`.

## Commands
- `/start`, `/help`
- `/news` and `/direct`: one row per source, with Pause/Resume and Remove buttons. Remove needs a second Confirm tap within 2 minutes.
- `/addnews @x`, `/adddirect @x`
- `/settings`: only a whitelist of keys.
- `/search <text>`, or just send any text.
- Forward a post to resolve its links.

**Callbacks** use `m1:<op>:<nonce>`. The nonce maps in memory to (user, payload, expiry), so a forwarded keyboard doesn't work for anyone else.

**Refactor first:** move the body of `sourceActions` (`cmd/jellyfin-telegram/sources.go`) into `sourceAction(action, body) error`. The panel and the bot then share the same rules, which come from `tgsource.Service.Add/Remove` and `newsfeed.Service.Add/Remove`.

## Library search and delivery
- **New `catalog.Search(q, limit)`:**
  - An FTS5 table over `files(file_name, caption, rel_path)`, kept in sync by triggers.
  - **Not checked:** that modernc's SQLite build includes FTS5. If it doesn't, fall back to `LIKE` on `rel_path`.
- **Browsing:** results are grouped by the title folder in `rel_path`. The flow is seasons, then episodes in pages of 10, then quality.
- **Delivery:** Bot API `copyMessage` from the library channel to the user. Auto-deleting the file after N minutes is optional.
- **Later (P3b):** ideas from sushi's catalog bot (`sushi/be/cmd/catalog-bot`): inline mode with a 350 ms wait and 8 results with posters, and the `m_`/`t_` deep links.

## Forwarded post → resolve → return → "Index this"
1. **Find the links.**
   - Add `tguser.StartURLsFrom(text, urls)` and have `StartURLs` (`internal/tguser/startlink.go`) call it, so links in Bot API entities and buttons can be read too.
   - Remove duplicates with `CanonicalStart`.
2. **Fetch the files.**
   - Borrow an account with `Manager.Lend` and call `newsfeed.Resolver.Open`, which uses `telegram-file-resolver`.
   - The files land in that account's Saved Messages.
   - This step has to run on a user account, because bots can't message other bots.
3. **Stage them.**
   - `ForwardSaved` into a private **staging channel**. The user account is a member and the manager bot is an admin.
   - Not the library channel: intake indexes every raw video posted there, so "Index this" would stop being a choice.
4. **Send them to the user.**
   - The bot copies the staged message to the requester.
   - Videos get an `Index this` button with callback `ix:<stagingMsgID>`.
5. **Index this.**
   - The bot copies the message from staging into the library channel, and `intake.Service.Pass` picks it up within a minute.
   - **Not checked:** that intake accepts a message a bot copied there.
   - If `SIGNED_LIBRARY` is off, the bot says indexing is off.
6. **Clean up.** Staging messages older than 24 hours are deleted.

**Limits:**
- One resolve per account at a time, and at most 3 queued per user.
- One status message is edited as the work moves along.
- `ErrSlowDown` is shown to the user and not retried in a loop.

**Staging channel setup:** `internal/setup/auto.go` creates it, or the user picks one in the panel.

## Phases
- **P1:**
  - Spike: run Bot API polling and MTProto on one token.
  - Dropdown and new keys.
  - `low` slot.
  - `loop.go`, `auth.go`, `/start`.
- **P2:**
  - Pull out `sourceAction`.
  - Source commands, with confirm.
  - `/settings` whitelist.
- **P3:**
  - FTS search, browse keyboards, `copyMessage` delivery.
  - P3b: inline mode.
- **P4:**
  - `StartURLsFrom`.
  - Staging channel.
  - Resolve queue.
  - "Index this".
- **P5 (future, only sketched):** a public repo, `telegram-file-provider`, a Go rewrite of sushi's SushiProviderBot.
  - **How it connects:** it reads files only through `FileIndex{Search, Get}` and sends only through `Delivery{Copy}`. jellyfin-telegram provides both.
  - **Where it runs:** inside the manager bot's router, or as its own bot with its own token.
  - **Take from sushi:**
    - the TMDB search client (`be/internal/adapter/tmdb/search.go`);
    - the release filename parser (`be/internal/core/classify/filename.go`).
  - **Leave out:** Postgres, sushi's `internal/` packages, and the channel ids committed in its repo.

## Risks
- Bot API and MTProto may not run on one token at once. The P1 spike decides; the fallback is gotd updates.
- The staging channel needs both the account and the bot as members. Setup checks this.
- The resolver can hit flood limits.
- Usernames can change. Each one is stored as its id on first match, and the panel warns about it.

## Verification
**Unit tests:**
- `tryAcquire` uses a low slot only when the other bots are busy or flood-limited.
- Auth: a Views id, an allowed id, a username stored as its id, and a stranger who gets silence.
- Callback nonces expire and only work for their owner.
- The `sourceAction` refactor passes the existing panel tests unchanged.
- FTS search on sample `rel_path`s.

**New fake:** an httptest Bot API server covering `getUpdates`, `sendMessage`, `copyMessage`, `editMessageText` and `answerCallbackQuery`. The existing `test/e2e` fake only covers media calls.

**Commands:** `go test -race ./...`, then `graphify update .`.

**Live test.** Use one browser tab, mask tokens, and don't run it while the real service runs a second bot user.
- DM the bot from a signed-in account, from an allowed username, and from a stranger, who gets no reply.
- Add, pause and remove a news source.
- Search and receive a file.
- Forward a post with a file-bot link, get the file back, press Index this, and check it appears in Jellyfin.
