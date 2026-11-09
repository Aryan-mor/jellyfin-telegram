// Publishes one release from the main branch that is already on GitHub.
//   node scripts/release.mjs stable
//   node scripts/release.mjs nightly
// Stable versions are 1.0.N. Nightly versions are 1.0.N-nightly.K on top of the
// current stable. The GitHub workflow builds the files; this only picks the number.
import { execFileSync } from "node:child_process"

const tagRe = /^v?(\d+)\.(\d+)\.(\d+)(?:-nightly\.(\d+))?$/

export function parseTag(tag) {
  const m = tagRe.exec(tag)
  if (!m) return null
  return {
    major: Number(m[1]),
    minor: Number(m[2]),
    patch: Number(m[3]),
    nightly: m[4] === undefined ? null : Number(m[4]),
  }
}

function compare(a, b) {
  for (const key of ["major", "minor", "patch"]) {
    if (a[key] !== b[key]) return a[key] - b[key]
  }
  return 0
}

// nextVersion is the tag to publish, without a leading v.
export function nextVersion(channel, tags) {
  const parsed = tags.map(parseTag).filter(Boolean)
  const stables = parsed.filter((p) => p.nightly === null).sort(compare)
  const stable = stables.at(-1) ?? { major: 1, minor: 0, patch: 0 }
  const base = `${stable.major}.${stable.minor}.${stable.patch}`
  if (channel === "stable") return `${stable.major}.${stable.minor}.${stable.patch + 1}`
  if (channel !== "nightly") throw new Error("channel must be stable or nightly")
  const n = parsed
    .filter((p) => p.nightly !== null && p.major === stable.major && p.minor === stable.minor && p.patch === stable.patch)
    .reduce((max, p) => Math.max(max, p.nightly), 0)
  // Two digits so GitHub's same-day tag sort (lexicographic) keeps .10 above .09.
  return `${base}-nightly.${String(n + 1).padStart(2, "0")}`
}

function run(cmd, args, opts = {}) {
  const out = execFileSync(cmd, args, { encoding: "utf8", ...opts })
  return String(out ?? "").trim()
}

function fail(message) {
  console.error(message)
  process.exit(1)
}

function main() {
  const channel = process.argv[2]
  if (channel !== "stable" && channel !== "nightly") {
    fail("Use: pnpm release:stable   or   pnpm release:nightly")
  }
  try {
    run("gh", ["auth", "status"], { stdio: ["ignore", "pipe", "pipe"] })
  } catch {
    fail("GitHub CLI is not logged in. Install gh, then run: gh auth login")
  }
  const dirty = run("git", ["status", "--porcelain"])
  if (dirty) fail("Commit or stash local edits first. The release is built from GitHub, not from this folder.")
  // `git fetch origin main` writes FETCH_HEAD only and leaves origin/main stale.
  run("git", ["fetch", "origin", "+refs/heads/main:refs/remotes/origin/main"], { stdio: "inherit" })
  const head = run("git", ["rev-parse", "HEAD"])
  const remote = run("git", ["rev-parse", "origin/main"])
  if (head !== remote) fail(`This commit is not origin/main.\n  local  ${head}\n  github ${remote}\nPush main, then release.`)

  const repo = run("gh", ["repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner"])
  const listed = JSON.parse(run("gh", ["release", "list", "--repo", repo, "--limit", "200", "--json", "tagName"]))
  const version = nextVersion(channel, listed.map((r) => r.tagName))
  console.log(`${channel} ${version}`)
  run("gh", [
    "workflow", "run", "release.yml", "--repo", repo, "--ref", "main",
    "-f", `channel=${channel}`, "-f", `version=${version}`,
  ], { stdio: "inherit" })
  console.log(`Release v${version} is building on GitHub.`)
  console.log(`https://github.com/${repo}/actions/workflows/release.yml`)
}

if (process.argv[1] && process.argv[1].endsWith("release.mjs")) main()
