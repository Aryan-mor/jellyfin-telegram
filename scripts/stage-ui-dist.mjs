// Git runs this from the commit hook. A push cannot add files to a commit that
// already left the machine, so the panel bundle has to be part of the commit.
import { createHash } from "node:crypto"
import { execFileSync } from "node:child_process"
import { readFileSync, writeFileSync } from "node:fs"

const stampPath = ".git/ui-dist-stamp"

export function needsBuild(stamp, hash) {
  return stamp !== hash
}

function git(args) {
  return execFileSync("git", args, { encoding: "utf8" }).trim()
}

function webHash() {
  return createHash("sha256").update(git(["ls-files", "-s", "--", "web"])).digest("hex")
}

function readStamp() {
  try {
    return readFileSync(stampPath, "utf8").trim()
  } catch {
    return ""
  }
}

function unstagedWeb() {
  const tracked = git(["diff", "--name-only", "--", "web"])
  const untracked = git(["ls-files", "--others", "--exclude-standard", "--", "web"])
  return [tracked, untracked].filter(Boolean).join("\n")
}

export function main() {
  const dirty = unstagedWeb()
  if (dirty) {
    console.error("Stage or stash edits under web/ before committing. The panel build uses those files.")
    console.error(dirty)
    process.exit(1)
  }
  const hash = webHash()
  if (!needsBuild(readStamp(), hash)) return
  execFileSync("pnpm", ["run", "ui:build"], { stdio: "inherit", shell: true })
  execFileSync("git", ["add", "-A", "--", "internal/webui/dist"], { stdio: "inherit" })
  writeFileSync(stampPath, hash + "\n")
}

if (process.argv[1] && process.argv[1].endsWith("stage-ui-dist.mjs")) main()
