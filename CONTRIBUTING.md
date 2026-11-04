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

## Pull requests

1. Fork the repository and create a branch from `main`.
2. Keep changes focused; one topic per pull request.
3. Run `gofmt -l .` (it should print nothing), `go vet ./...` and `go test ./...`.
4. Add or update tests for behaviour you change.
5. Update `README.md` and `.env.example` when you add or change a setting.
6. Describe what changed and why in the pull request template.

By contributing you agree that your contributions are licensed under the
[GPL-3.0](LICENSE), the license of this project.