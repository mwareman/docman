// Thin wrapper over DocMan's JSON API, its newline-delimited progress streams
// and its WebSockets.

export class ApiError extends Error {
  constructor(status, message, hint, payload) {
    super(message || `request failed (${status})`);
    this.name = 'ApiError';
    this.status = status;
    this.hint = hint || '';
    // The whole response body, so callers can read flags such as totp_required.
    this.payload = payload || {};
  }
}

const WRITE_HEADERS = { 'Content-Type': 'application/json', 'X-DocMan-CSRF': '1' };

/** Perform a JSON request. Returns the parsed body, or null for 204. */
export async function api(path, { method = 'GET', body, signal } = {}) {
  const init = { method, credentials: 'same-origin', signal, headers: { 'X-DocMan-CSRF': '1' } };
  if (body !== undefined) {
    init.headers = WRITE_HEADERS;
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (res.status === 204) return null;

  const text = await res.text();
  let payload = null;
  if (text) {
    try { payload = JSON.parse(text); } catch { payload = { error: text.slice(0, 400) }; }
  }
  if (!res.ok) {
    throw new ApiError(res.status, (payload && payload.error) || res.statusText, payload && payload.hint, payload);
  }
  return payload;
}

export const get = (path, opts) => api(path, opts);
export const post = (path, body, opts) => api(path, { ...opts, method: 'POST', body: body ?? {} });
export const put = (path, body, opts) => api(path, { ...opts, method: 'PUT', body: body ?? {} });
export const patch = (path, body, opts) => api(path, { ...opts, method: 'PATCH', body: body ?? {} });
export const del = (path, opts) => api(path, { ...opts, method: 'DELETE' });

/**
 * POST and read a newline-delimited JSON progress stream, calling onEvent for
 * each parsed object. Resolves when the stream ends.
 */
export async function stream(path, { method = 'POST', body, rawBody, contentType, onEvent, signal } = {}) {
  const headers = { 'X-DocMan-CSRF': '1' };
  let payload;
  if (rawBody !== undefined) {
    payload = rawBody;
    headers['Content-Type'] = contentType || 'application/octet-stream';
  } else if (body !== undefined) {
    payload = JSON.stringify(body);
    headers['Content-Type'] = 'application/json';
  }
  const res = await fetch(path, { method, headers, body: payload, credentials: 'same-origin', signal });
  if (!res.ok) {
    const text = await res.text();
    let parsed = null;
    try { parsed = JSON.parse(text); } catch { /* not JSON */ }
    throw new ApiError(res.status, (parsed && parsed.error) || text.slice(0, 400) || res.statusText);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let cut;
    while ((cut = buffer.indexOf('\n')) >= 0) {
      const line = buffer.slice(0, cut).trim();
      buffer = buffer.slice(cut + 1);
      if (!line) continue;
      try { onEvent && onEvent(JSON.parse(line)); } catch { /* ignore partial noise */ }
    }
  }
  const tail = buffer.trim();
  if (tail) {
    try { onEvent && onEvent(JSON.parse(tail)); } catch { /* ignore */ }
  }
}

/** Build the WebSocket URL for an API stream path. */
export function wsURL(path, params) {
  const url = new URL(path, location.href);
  url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  if (params) {
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined && value !== null && value !== '') url.searchParams.set(key, value);
    }
  }
  return url.toString();
}

/**
 * Open a managed WebSocket that reconnects with backoff until closed.
 * Handlers: onJSON(obj), onBinary(ArrayBuffer), onOpen(socket), onDown(reason).
 * Returns { close(), send(data), socket() }.
 */
export function socket(path, { params, onJSON, onBinary, onOpen, onDown, retry = true } = {}) {
  let ws = null;
  let closed = false;
  let attempt = 0;
  let timer = 0;

  const connect = () => {
    if (closed) return;
    try {
      ws = new WebSocket(wsURL(path, params));
    } catch (err) {
      schedule();
      return;
    }
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      attempt = 0;
      onOpen && onOpen(ws);
    };
    ws.onmessage = (event) => {
      if (typeof event.data === 'string') {
        if (!onJSON) return;
        try { onJSON(JSON.parse(event.data)); } catch { /* ignore */ }
      } else if (onBinary) {
        onBinary(event.data);
      }
    };
    ws.onclose = () => {
      if (closed) return;
      onDown && onDown('closed');
      schedule();
    };
    ws.onerror = () => { /* close follows */ };
  };

  const schedule = () => {
    if (closed || !retry) return;
    attempt = Math.min(attempt + 1, 6);
    const delay = Math.min(500 * 2 ** (attempt - 1), 15000);
    clearTimeout(timer);
    timer = setTimeout(connect, delay);
  };

  connect();
  return {
    close() {
      closed = true;
      clearTimeout(timer);
      if (ws && ws.readyState <= 1) ws.close();
    },
    send(data) {
      if (ws && ws.readyState === 1) ws.send(data);
    },
    socket: () => ws,
  };
}

// ---------- endpoint shortcuts ----------

export const State = {
  fetch: () => get('/api/state'),
  login: (username, password, code) => post('/api/auth/login', { username, password, code: code || '' }),
  logout: () => post('/api/auth/logout'),
  bootstrapVerify: (key) => post('/api/bootstrap/verify', { key }),
  bootstrapAdmin: (key, username, password) => post('/api/bootstrap/admin', { key, username, password }),
};

export const Containers = {
  list: () => get('/api/containers'),
  one: (id) => get(`/api/containers/${encodeURIComponent(id)}`),
  inspect: (id) => get(`/api/containers/${encodeURIComponent(id)}/inspect`),
  spec: (id) => get(`/api/containers/${encodeURIComponent(id)}/spec`),
  saveSpec: (id, spec, start) => put(`/api/containers/${encodeURIComponent(id)}/spec`, { spec, start }),
  action: (id, action, query = '') => post(`/api/containers/${encodeURIComponent(id)}/${action}${query}`),
  recreate: (id, body) => post(`/api/containers/${encodeURIComponent(id)}/recreate`, body || {}),
  rename: (id, name) => post(`/api/containers/${encodeURIComponent(id)}/rename`, { name }),
  remove: (id, { force, volumes } = {}) =>
    del(`/api/containers/${encodeURIComponent(id)}?force=${force ? 1 : 0}&volumes=${volumes ? 1 : 0}`),
  create: (payload) => post('/api/containers', payload),
  logs: (id, tail) => get(`/api/containers/${encodeURIComponent(id)}/logs?tail=${tail || 500}`),
  top: (id) => get(`/api/containers/${encodeURIComponent(id)}/top`),
  pin: (id, ip) => post(`/api/containers/${encodeURIComponent(id)}/ip`, { ip: ip || '' }),
  unpin: (id) => del(`/api/containers/${encodeURIComponent(id)}/ip`),
};

export const Images = {
  list: () => get('/api/images'),
  inspect: (name) => get(`/api/images/${name.split('/').map(encodeURIComponent).join('/')}`),
  tag: (image, repo, tag) => post('/api/images/tag', { image, repo, tag }),
  remove: (image, force) => post('/api/images/remove', { image, force }),
  prune: (unused) => post(`/api/images/prune?unused=${unused ? 1 : 0}`),
  deployPlan: (image, name) => post('/api/images/deploy-plan', { image, name }),
  updates: () => get('/api/images/updates'),
  checkUpdates: (reference) => post('/api/images/updates/check', { reference: reference || '' }),
};

export const Volumes = {
  list: () => get('/api/volumes'),
  create: (payload) => post('/api/volumes', payload),
  remove: (name, force) => del(`/api/volumes/${encodeURIComponent(name)}?force=${force ? 1 : 0}`),
  prune: () => post('/api/volumes/prune'),
  drivers: () => get('/api/system/volume-drivers'),
};

const vq = (name, path, extra = '') =>
  `/api/volumes/${encodeURIComponent(name)}/files${extra}?path=${encodeURIComponent(path)}`;

/** The volume explorer: files inside one volume. */
export const VolumeFiles = {
  list: (name, path) => get(vq(name, path)),
  content: (name, path) => get(vq(name, path, '/content')),
  save: (name, path, content, mtime, create) => put(vq(name, path, '/content'), { content, mtime: mtime || 0, create: !!create }),
  mkdir: (name, path) => post(`/api/volumes/${encodeURIComponent(name)}/files/mkdir`, { path }),
  rename: (name, from, to) => post(`/api/volumes/${encodeURIComponent(name)}/files/rename`, { from, to }),
  remove: (name, path) => del(vq(name, path)),
  /** Stop the volume's explorer helper; keepalive lets it outlive a closing tab. */
  close: (name) => fetch(`/api/volumes/${encodeURIComponent(name)}/explorer`, {
    method: 'DELETE', credentials: 'same-origin', keepalive: true, headers: { 'X-DocMan-CSRF': '1' },
  }).catch(() => {}),
  downloadURL: (name, path, kind) => `${vq(name, path, '/download')}${kind === 'dir' ? '&kind=dir' : ''}`,
  /**
   * Upload one file into a folder, reporting progress. Resolves with the
   * response; rejects with an ApiError whose payload.exists says the name is taken.
   */
  upload: (name, dir, file, { overwrite = false, onProgress } = {}) => new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const url = `${vq(name, dir, '/upload')}&name=${encodeURIComponent(file.name)}${overwrite ? '&overwrite=1' : ''}`;
    xhr.open('POST', url);
    xhr.setRequestHeader('X-DocMan-CSRF', '1');
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.upload.onprogress = (event) => { if (onProgress && event.lengthComputable) onProgress(event.loaded / event.total); };
    xhr.onload = () => {
      let payload = {};
      try { payload = JSON.parse(xhr.responseText || '{}'); } catch { /* not JSON */ }
      if (xhr.status >= 200 && xhr.status < 300) resolve(payload);
      else reject(new ApiError(xhr.status, payload.error || xhr.statusText, payload.hint, payload));
    };
    xhr.onerror = () => reject(new ApiError(0, 'the upload was interrupted'));
    xhr.send(file);
  }),
};

export const Networks = {
  list: () => get('/api/networks'),
  addresses: () => get('/api/addresses'),
  reconcile: () => post('/api/addresses/reconcile'),
};

export const System = {
  info: () => get('/api/system'),
  df: () => get('/api/system/df'),
  audit: (limit) => get(`/api/audit?limit=${limit || 200}`),
  settings: () => get('/api/settings'),
  saveSettings: (values) => patch('/api/settings', values),
};

export const Account = {
  me: () => get('/api/account'),
  setPassword: (current, next) => post('/api/account/password', { current, new: next }),
  setUsername: (username) => post('/api/account/username', { username }),
  sessions: () => get('/api/account/sessions'),
  revokeSessions: () => post('/api/account/sessions/revoke'),
  totpBegin: () => post('/api/account/totp/begin'),
  totpEnable: (code) => post('/api/account/totp/enable', { code }),
  totpDisable: (password, code) => post('/api/account/totp/disable', { password: password || '', code: code || '' }),
  passkeys: () => get('/api/passkeys'),
  registerBegin: () => post('/api/passkeys/register/begin'),
  registerFinish: (payload) => post('/api/passkeys/register/finish', payload),
  renamePasskey: (id, name) => patch(`/api/passkeys/${id}`, { name }),
  deletePasskey: (id) => del(`/api/passkeys/${id}`),
  tokens: () => get('/api/tokens'),
  createToken: (name, days) => post('/api/tokens', { name, expires_in_days: days || 0 }),
  deleteToken: (id, purge) => del(`/api/tokens/${id}?purge=${purge ? 1 : 0}`),
};

export const Jobs = {
  get: (id) => get(`/api/jobs/${encodeURIComponent(id)}`),
  list: () => get('/api/jobs'),
};

export const Registries = {
  list: () => get('/api/registries'),
  create: (body) => post('/api/registries', body),
  update: (id, body) => put(`/api/registries/${id}`, body),
  remove: (id) => del(`/api/registries/${id}`),
  makeDefault: (id) => post(`/api/registries/${id}/default`),
  test: (id) => post(`/api/registries/${id}/test`),
  search: (id, q) => get(`/api/registries/${id}/search?q=${encodeURIComponent(q)}`),
  tags: (id, repo) => get(`/api/registries/${id}/tags?repo=${encodeURIComponent(repo)}`),
  /** The pre-built image archives a GitHub repository registry offers. */
  archives: (id) => get(`/api/registries/${id}/archives`),
};

export const Users = {
  list: () => get('/api/users'),
  create: (username, password) => post('/api/users', { username, password }),
  reset: (id, password) => post(`/api/users/${id}/reset`, { password }),
  remove: (id) => del(`/api/users/${id}`),
};
