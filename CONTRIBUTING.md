# Contributing to jellyfin-telegram

Thanks for your interest in improving jellyfin-telegram.

## Ground rules

- Be respectful. This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
- Never paste real bot tokens, `api_hash` values, session files or `.env` contents in issues,
  pull requests or logs. Redact them.
- Security problems go through [SECURITY.md](SECURITY.md), not public issues.

## Reporting bugs and requesting features

Use the issue templates. For bugs, include your OS, Go version, Jellyfin version, whether
Jellyfin is managed or your own, and the output of `jellyfin-telegram check` (redacted).

## Development setup

1. Install Go 1.26 or newer and Git.
2. Clone and build:

   ```shell
   git clone https://github.com/Aryan-mor/jellyfin-telegram.git
   cd jellyfin-telegram
   go build ./...
   ```

3. Run the tests:

   ```shell
   go test ./...
   go test -race ./...
   go test -tags e2e -v -timeout 15m ./test/e2e   # needs Docker and ffmpeg
   ```

Read [docs/architecture.md](docs/architecture.md) before larger changes.

## Branch naming

Create a branch from `main` named after the change type:

- `feat/short-description` for features
- `fix/short-description` for bug fixes
- `docs/...`, `refactor/...`, `test/...`, `perf/...`, `ci/...`, `chore/...` for the rest

## Pull request title

PR titles must follow [Conventional Commits](https://www.conventionalcommits.org/). This is
checked automatically by the `pr-title` workflow. Allowed types: `feat`, `fix`, `docs`,
`refactor`, `test`, `chore`, `perf`, `ci`. A scope is optional.

```text
feat: add bot health check to the panel
fix(gateway): handle range requests past end of file
docs: explain Tailscale setup
```

PRs are squash-merged, so the title becomes the commit message on `main`.

## Pull requests

1. Fork the repository and create a branch as described above.
2. Keep changes focused; one topic per pull request.
3. Run `gofmt -l .` (it should print nothing), `go vet ./...`, `go build ./...` and `go test ./...`.
4. Add or update tests for behaviour you change.
5. Update `README.md` and `.env.example` when you add or change a setting.
6. Fill in every section of the pull request template.

## Merge requirements

`main` is protected. A pull request can be merged only when:

- the **CI** workflow (`test` job: gofmt, vet, build, test) passes,
- the **PR title** check (`pr-title`) passes,
- it has **one approving review from the maintainer** (code owner, see `.github/CODEOWNERS`).

New commits pushed after an approval dismiss it, so the latest changes are always reviewed.
Force pushes to and deletion of `main` are blocked.

By contributing you agree that your contributions are licensed under the
[GPL-3.0](LICENSE), the license of this project.