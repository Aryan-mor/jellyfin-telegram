<!-- PR title must follow Conventional Commits, e.g. "feat: add bot health check" or "fix(gateway): handle range past EOF". -->

## Summary

<!-- What does this PR change, and why? -->

## Type of change

- [ ] feat: new feature
- [ ] fix: bug fix
- [ ] docs: documentation only
- [ ] refactor: code change that neither fixes a bug nor adds a feature
- [ ] test: adding or fixing tests
- [ ] perf: performance improvement
- [ ] ci: CI or workflow change
- [ ] chore: maintenance, dependencies, tooling
- [ ] Breaking change (existing setups or settings stop working as before)

## Related issue

<!-- e.g. Closes #123. Open an issue first for larger changes. -->

## How it was tested

<!-- Commands run, OS, Jellyfin version, managed or own Jellyfin, manual steps. -->

## Screenshots / logs (if relevant)

<!-- Redact bot tokens, api_hash, stream tokens and channel ids. -->

## Checklist

- [ ] PR title follows Conventional Commits
- [ ] `gofmt -l .` prints nothing
- [ ] `go vet ./...`, `go build ./...` and `go test ./...` pass
- [ ] Tests added or updated for changed behaviour
- [ ] README.md / .env.example updated for new or changed settings
- [ ] No tokens, credentials or personal data included