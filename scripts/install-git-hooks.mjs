// Copies the versioned hooks into this clone. Git does not run .githooks on its own.
import { chmodSync, copyFileSync, mkdirSync } from "node:fs"
import { execFileSync } from "node:child_process"

const hooksDir = execFileSync("git", ["rev-parse", "--git-path", "hooks"], { encoding: "utf8" }).trim()
mkdirSync(hooksDir, { recursive: true })
const dest = `${hooksDir}/pre-commit`
copyFileSync(".githooks/pre-commit", dest)
try {
  chmodSync(dest, 0o755)
} catch {
  // Windows does not need the executable bit.
}
console.log(`Installed ${dest}`)
