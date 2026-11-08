import assert from "node:assert/strict"
import test from "node:test"
import { nextVersion, parseTag } from "./release.mjs"

test("parse ignores names that are not a version", () => {
  assert.equal(parseTag("latest"), null)
  assert.equal(parseTag("v1.0.42-nightly.3")?.nightly, 3)
})

test("stable bumps the newest stable and skips nightlies", () => {
  assert.equal(nextVersion("stable", ["v1.0.9", "v1.0.10-nightly.4", "v1.0.8"]), "1.0.10")
})

test("nightly counts on top of the current stable", () => {
  assert.equal(nextVersion("nightly", ["v1.0.10", "v1.0.10-nightly.2", "v1.0.9-nightly.9"]), "1.0.10-nightly.3")
})

test("first nightly on a new stable starts at 1", () => {
  assert.equal(nextVersion("nightly", ["v1.0.11", "v1.0.10-nightly.4"]), "1.0.11-nightly.1")
})
