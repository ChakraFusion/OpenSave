import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, initApi } from './api.js';

// Another program on OpenSave's port answers with a web page. Read as {} it
// made OpenSave look empty — no games, first-run settings, "i is not
// iterable" from a scan — so it must be an error that says what happened.

const reply = (body, status = 200) =>
  Promise.resolve(new Response(body, { status, headers: { 'Content-Type': 'text/html' } }));

beforeEach(() => {
  globalThis.window = { go: { main: { App: { DaemonAddr: async () => ({ addr: '127.0.0.1:18777' }) } } } };
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete globalThis.window;
});

describe('api', () => {
  it('refuses a web page where OpenSave would answer', async () => {
    vi.stubGlobal('fetch', () => reply('<html>vendor utility</html>'));
    await expect(api.get('/api/presets/scan')).rejects.toThrow(/Something other than OpenSave/);
  });

  it('still takes an empty success as nothing to say', async () => {
    vi.stubGlobal('fetch', () => Promise.resolve(new Response(null, { status: 204 })));
    await expect(api.post('/api/sync/pause')).resolves.toEqual({});
  });

  it('passes an error of its own through', async () => {
    vi.stubGlobal('fetch', () => Promise.resolve(new Response('{"error":"no such game"}', { status: 404 })));
    await expect(api.get('/api/games/x')).rejects.toThrow('no such game');
  });
});

describe('initApi', () => {
  it('will not start against something that is not OpenSave', async () => {
    vi.stubGlobal('fetch', () => Promise.resolve(new Response('{"ok":true}', { status: 200 })));
    await expect(initApi()).rejects.toThrow(/Something other than OpenSave/);
  });

  it('starts against OpenSave', async () => {
    vi.stubGlobal('fetch', () => Promise.resolve(new Response('{"settings":{"deviceName":"PC"},"gameCount":0}', { status: 200 })));
    await expect(initApi()).resolves.toBe('http://127.0.0.1:18777');
  });
});
