import assert from "node:assert/strict";
import { test } from "node:test";
import { isRestartConnectionError, suppressUpdateRestartError, restartWaitExpired, UPDATE_STATUS_URL, RESTART_WAIT_TIMEOUT } from "./update-restart";

test("only expected update status gateway failures are silent", () => {
  for (const status of [502, 503, 504]) {
    const error = { response: { status }, config: { url: UPDATE_STATUS_URL, expectedUpdateRestart: () => true } };
    assert.equal(suppressUpdateRestartError(error), true);
    assert.equal(suppressUpdateRestartError({ ...error, config: { url: UPDATE_STATUS_URL } }), false);
    assert.equal(suppressUpdateRestartError({ ...error, config: { ...error.config, expectedUpdateRestart: () => false } }), false);
    assert.equal(suppressUpdateRestartError({ ...error, config: { ...error.config, url: "/api/v1/admin/orders" } }), false);
  }
});

test("login, permission, server bugs and cancelled requests remain visible", () => {
  for (const status of [400, 401, 403, 404, 429, 500]) {
    assert.equal(suppressUpdateRestartError({ response: { status }, config: { url: UPDATE_STATUS_URL, expectedUpdateRestart: () => true } }), false);
  }
  assert.equal(isRestartConnectionError({ code: "ERR_CANCELED" }), false);
  assert.equal(isRestartConnectionError({}), false);
});

test("network disconnect and timeout are classified; waiting state is read at failure time", () => {
  let waiting = false;
  const config = { url: UPDATE_STATUS_URL, expectedUpdateRestart: () => waiting };
  for (const code of ["ERR_NETWORK", "ECONNABORTED", "ETIMEDOUT"]) {
    assert.equal(suppressUpdateRestartError({ code, config }), false);
    waiting = true;
    assert.equal(suppressUpdateRestartError({ code, config }), true);
    waiting = false;
  }
});

test("restart wait expires at five minutes but not during long healthy downloads", () => {
  assert.equal(restartWaitExpired(1000, 1000 + RESTART_WAIT_TIMEOUT - 1), false);
  assert.equal(restartWaitExpired(1000, 1000 + RESTART_WAIT_TIMEOUT), true);
  assert.equal(restartWaitExpired(0, RESTART_WAIT_TIMEOUT * 2), false);
});

test("Axios interceptor keeps the failure result while suppressing only the expected toast", async () => {
  const { createFlatRequest } = await import("../../../packages/axios/src/index");
  const messages: string[] = [];
  const request = createFlatRequest({
    adapter: async config => {
      throw Object.assign(new Error("Request failed with status code 502"), {
        config, code: "ERR_BAD_RESPONSE", response: { status: 502, config, data: "Bad Gateway" }
      });
    }
  }, {
    onError: error => { if (!suppressUpdateRestartError(error)) messages.push(error.message); }
  });
  const quiet = await request({ url: UPDATE_STATUS_URL, expectedUpdateRestart: () => true });
  assert.ok(quiet.error);
  assert.equal(quiet.data, null);
  assert.deepEqual(messages, []);
  await request({ url: UPDATE_STATUS_URL, expectedUpdateRestart: () => false });
  await request({ url: "/api/v1/admin/orders", expectedUpdateRestart: () => true });
  assert.equal(messages.length, 2);
});
