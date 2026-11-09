import assert from "node:assert/strict"
import test from "node:test"
import { needsBuild } from "./stage-ui-dist.mjs"

test("a missing stamp rebuilds the panel", () => {
  assert.equal(needsBuild("", "abc"), true)
})

test("the same web tree skips the build", () => {
  assert.equal(needsBuild("abc", "abc"), false)
})

test("a changed web tree rebuilds the panel", () => {
  assert.equal(needsBuild("abc", "def"), true)
})
