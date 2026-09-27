// Uploading large files in pieces, so they pass the request size limits of
// whatever proxy or WAF sits in front of DocMan — Cloudflare, cloudflared,
// nginx, lighttpd and the rest — without anyone having to know the limit.
//
// Each piece is its own request. A piece that is refused (413, or a proxy's
// own error page) or cut off (the connection dropped) is sent again, smaller,
// from the last byte DocMan confirmed; the size that failed becomes a ceiling
// never tried again. While pieces go through quickly they grow, up to half the
// smallest size that ever failed. So the upload settles on what the path in
// between allows, and the file only ever grows by whole, confirmed pieces.

import { ApiError, post, get, del } from './api.js';

const START = 8 * 1024 * 1024;
const MIN = 64 * 1024;
const MAX = 64 * 1024 * 1024;
const PIECE_TIMEOUT_MS = 90 * 1000; // under Cloudflare's 100 second limit
const MAX_FAILURES_AT_MIN = 6;

/**
 * Upload a file in pieces, then have DocMan import it.
 * @param {File} file
 * @param {object} target {kind: 'image'} or {kind: 'file', volume, path, overwrite}
 * @param {object} hooks
 *   onProgress({sent, total, pieceSize, rate}) while bytes go up;
 *   onImport(step) while DocMan imports; signal: an AbortSignal to cancel.
 * @returns {Promise<object>} the finished job, whose result lists loaded images
 */
export async function uploadInPieces(file, target, { onProgress, onImport, signal } = {}) {
  const session = await post('/api/uploads', { ...target, name: file.name, size: file.size });
  const cancel = () => del(`/api/uploads/${session.id}`).catch(() => {});
  if (signal) signal.addEventListener('abort', cancel, { once: true });

  let received = session.received || 0;
  let size = START;
  // The smallest piece size that has ever failed; nothing at or above it is tried again.
  let ceiling = Infinity;
  const largest = Math.min(MAX, session.max_chunk || MAX);
  let failuresAtMin = 0;
  const startedAt = Date.now();
  const report = (inFlight) => {
    if (!onProgress) return;
    const sent = Math.min(file.size, received + inFlight);
    const seconds = Math.max(0.5, (Date.now() - startedAt) / 1000);
    onProgress({ sent, confirmed: received, total: file.size, pieceSize: size, rate: sent / seconds });
  };
  report(0);

  while (received < file.size) {
    if (signal && signal.aborted) throw abortError();
    const end = Math.min(file.size, received + size);
    const began = Date.now();
    try {
      const reply = await sendPiece(session.id, received, file.slice(received, end), (loaded) => report(loaded), signal);
      received = reply.received;
      failuresAtMin = 0;
      // Quick pieces grow; slow ones stay put, well inside any time limit.
      if (Date.now() - began < 8000) size = Math.max(MIN, Math.min(size * 2, largest, Math.floor(ceiling / 2)));
      report(0);
    } catch (err) {
      if (err.name === 'AbortError') throw err;
      if (err.status === 409 && err.payload && typeof err.payload.received === 'number') {
        received = err.payload.received; // out of step; carry on from where DocMan is
        continue;
      }
      if (err.status === 404 || err.status === 410) throw err; // the upload is gone
      if (err.status === 401 || err.status === 403) throw err;
      // Refused or cut off: a proxy limit, or a dropped connection. Retry smaller.
      if (size <= MIN) {
        failuresAtMin++;
        if (failuresAtMin > MAX_FAILURES_AT_MIN) {
          throw new ApiError(err.status || 0, 'the upload keeps failing even in the smallest pieces; check the connection to DocMan and try again');
        }
        await pause(1000 * failuresAtMin, signal);
      } else {
        ceiling = Math.min(ceiling, size);
        size = Math.max(MIN, Math.floor(size / 2));
      }
      received = await confirmed(session.id, received);
      report(0);
    }
  }

  if (onImport) onImport('Checking the upload');
  const { job } = await post(`/api/uploads/${session.id}/complete`);
  return followImport(job, onImport, signal);
}

/** Send one piece; resolves with DocMan's reply, rejects with an ApiError. */
function sendPiece(id, offset, blob, onLoaded, signal) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    // POST, not PUT: a browser resends a PUT by itself when its connection
    // drops, which would hide a size limit and waste a retry on it.
    xhr.open('POST', `/api/uploads/${encodeURIComponent(id)}?offset=${offset}`);
    xhr.setRequestHeader('X-DocMan-CSRF', '1');
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.timeout = PIECE_TIMEOUT_MS;
    xhr.upload.onprogress = (event) => { if (event.lengthComputable) onLoaded(event.loaded); };
    const abort = () => xhr.abort();
    if (signal) signal.addEventListener('abort', abort, { once: true });
    const done = () => { if (signal) signal.removeEventListener('abort', abort); };
    xhr.onload = () => {
      done();
      let payload = null;
      try { payload = JSON.parse(xhr.responseText || 'null'); } catch { /* a proxy's own page */ }
      if (xhr.status >= 200 && xhr.status < 300 && payload && typeof payload.received === 'number') resolve(payload);
      else reject(new ApiError(xhr.status, (payload && payload.error) || `the piece was refused (HTTP ${xhr.status})`, '', payload || {}));
    };
    xhr.onerror = () => { done(); reject(new ApiError(0, 'the connection dropped while sending a piece')); };
    xhr.ontimeout = () => { done(); reject(new ApiError(0, 'a piece took too long to send')); };
    xhr.onabort = () => { done(); reject(abortError()); };
    xhr.send(blob);
  });
}

/** How much DocMan has safely stored, after a failed piece. */
async function confirmed(id, fallback) {
  for (let attempt = 0; attempt < 5; attempt++) {
    try {
      const state = await get(`/api/uploads/${encodeURIComponent(id)}`);
      return state.received;
    } catch (err) {
      if (err.status === 404) throw err;
      await pause(1000 * (attempt + 1));
    }
  }
  return fallback;
}

/** Follow the import job, riding out connection drops, until it ends. */
async function followImport(job, onImport, signal) {
  for (;;) {
    let current;
    try {
      current = await get(`/api/jobs/${encodeURIComponent(job.id)}`);
    } catch (err) {
      if (err.status === 404) throw new ApiError(404, 'DocMan restarted during the import; check the image list');
      if (onImport) onImport('Waiting for DocMan to answer…');
      await pause(2000, signal);
      continue;
    }
    if (current.state === 'running') {
      if (onImport) onImport(current.step);
      await pause(800, signal);
      continue;
    }
    if (current.state === 'done') return current;
    throw new ApiError(0, current.error || 'the import failed');
  }
}

function pause(ms, signal) {
  return new Promise((resolve, reject) => {
    const t = setTimeout(resolve, ms);
    if (signal) signal.addEventListener('abort', () => { clearTimeout(t); reject(abortError()); }, { once: true });
  });
}

function abortError() {
  const err = new Error('the upload was cancelled');
  err.name = 'AbortError';
  return err;
}
