import { readFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"

const root = new URL("../src", import.meta.url)
function files(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) files(p, out)
    else if (/\.(tsx|ts)$/.test(name)) out.push(p)
  }
  return out
}

const re = /\bt\(\s*(["'`])((?:\\.|(?!\1).)*)\1/g
const keys = new Set()
for (const file of files(root.pathname.replace(/^\/([A-Za-z]:)/, "$1"))) {
  const s = readFileSync(file, "utf8")
  let m
  while ((m = re.exec(s))) {
    keys.add(m[2].replace(/\\n/g, "\n").replace(/\\"/g, '"').replace(/\\'/g, "'"))
  }
}
console.log([...keys].sort().join("\n"))
console.error("COUNT", keys.size)
