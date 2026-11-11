## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).

## CI gofmt (do this before you finish)

Push to `main` and every pull request run `.github/workflows/ci.yml`. The `test` job step `gofmt` fails when `gofmt -l .` prints any file. That is the usual red CI after a Go edit: struct fields and `const` blocks lose alignment (`cmd/jellyfin-telegram/serve.go`, `internal/tgsource/service.go` are the pattern).

Before you end a task that touches `.go` files:

1. Format the files you changed: `gofmt -w <files>`.
2. `gofmt -l <files>` must print nothing.
3. Do not stop while those files would fail the `gofmt` step.

On Windows, `gofmt -l .` can list extra files because of CRLF. Format only the files you edited. Check `git diff` is alignment, not a repo-wide rewrite.
