import assert from "node:assert/strict";
import { test } from "node:test";
import type { UpdateStatus } from "@/service/api/update";
import { isUpdateComplete, isUpdatePending } from "./update-state";

const status = (patch: Partial<UpdateStatus> = {}) => ({
  phase: "idle", busy: false, current_version: "v1.0.0", target_version: "", ...patch
} as UpdateStatus);

test("active phases, busy before target, and pending target block checks", () => {
  for (const phase of ["checking", "backing_up", "downloading", "applying", "restarting", "verifying"]) {
    assert.equal(isUpdatePending(status({ phase })), true);
    assert.equal(isUpdateComplete(status({ phase, target_version: "v1.0.0" })), false);
  }
  assert.equal(isUpdatePending(status({ busy: true })), true);
  assert.equal(isUpdatePending(status({ target_version: "v2.0.0" })), true);
});

test("completed and failed historical targets allow future checks", () => {
  assert.equal(isUpdatePending(status({ target_version: "v1.0.0" })), false);
  for (const phase of ["failed", "rolled_back"]) {
    const st = status({ phase, target_version: "v2.0.0" });
    assert.equal(isUpdatePending(st), false);
    assert.equal(isUpdateComplete(st), false);
  }
});

test("completion requires healthy idle, target equality and no active operation", () => {
  assert.equal(isUpdateComplete(status()), false);
  assert.equal(isUpdateComplete(status({ target_version: "v2.0.0" })), false);
  assert.equal(isUpdateComplete(status({ target_version: "v1.0.0", busy: true })), false);
  assert.equal(isUpdateComplete(status({ target_version: "v1.0.0" })), true);
});
