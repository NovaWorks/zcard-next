import assert from 'node:assert/strict';
import http from 'node:http';
import { createFlatRequest } from '../admin/packages/axios/src/index';
import { authResponseStatus, createSessionRecovery, singleFlightRefresh } from '../admin/src/service/request/session-recovery';

async function scenario(mode: 'success' | 'refresh-fails' | 'retry-fails' | 'http500') {
  let authorization: string | null = 'Bearer old';
  let refreshes = 0, logouts = 0, calls = 0;
  const state = { refreshTokenPromise: null as Promise<boolean> | null, errMsgStack: [] as string[] };
  const server = http.createServer((req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/auth/refresh') {
      refreshes++;
      setTimeout(() => { res.statusCode = mode === 'refresh-fails' ? 401 : 200; res.end(JSON.stringify({ access_token: 'new' })); }, 20);
      return;
    }
    if (req.url === '/auth/login') { res.statusCode = 401; res.end('{}'); return; }
    calls++;
    const status = mode === 'http500' ? 500 : mode === 'retry-fails' || req.headers.authorization !== 'Bearer new' ? 401 : 200;
    const send = () => { res.statusCode = status; res.end(JSON.stringify({ ok: status === 200 })); };
    if (req.url === '/slow' && status === 401) setTimeout(send, 60); else send();
  });
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as { port: number }).port;
  const recover = createSessionRecovery({
    authorization: () => authorization,
    refresh: () => singleFlightRefresh(state, async () => {
      const result = await request<{access_token: string}>({url:'/auth/refresh', method:'post'});
      if (result.error || !result.data?.access_token) return false;
      authorization = `Bearer ${result.data.access_token}`;
      return true;
    }),
    logout: async () => { logouts++; authorization = null; },
  });
  const request = createFlatRequest<any, any>({baseURL:`http://127.0.0.1:${port}`, validateStatus:authResponseStatus}, {
    onRequest: config => { if (authorization) config.headers.set('Authorization', authorization); return config; },
    isBackendSuccess: response => response.status >= 200 && response.status < 300,
    onBackendFail: recover,
    onError: () => {},
  });
  try {
    const login = await request({url:'/auth/login'});
    assert.ok(login.error); assert.equal(refreshes,0); assert.equal(logouts,0);
    const results = mode === 'success' ? await Promise.all([request({url:'/resource'}),request({url:'/resource'}),request({url:'/slow'})]) : await Promise.all([request({url:'/resource'}), request({url:'/resource'})]);
    if (mode === 'success') {
      assert.ok(results.every(r=>r.data?.ok)); assert.equal(refreshes,1); assert.equal(logouts,0); assert.equal(calls,6);
    } else {
      assert.ok(results[0].error);
      assert.equal(refreshes, mode === 'http500' ? 0 : 1);
      assert.equal(logouts, mode === 'http500' ? 0 : 1);
      assert.equal(calls, mode === 'retry-fails' ? 4 : 2);
    }
    assert.equal(state.refreshTokenPromise,null);
    console.log('PASS auth recovery:',mode);
  } finally { server.closeAllConnections(); await new Promise<void>(resolve=>server.close(()=>resolve())); }
}
async function main() {
  for (const mode of ['success','refresh-fails','retry-fails','http500'] as const) await scenario(mode);
}
main().catch(error=>{ console.error(error); process.exitCode=1; });
