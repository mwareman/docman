// Following background jobs: container upgrades and configuration changes run
// on DocMan itself, not in the browser's request, so they finish even when the
// connection drops. This follows one to the end, through lost connections and
// page reloads, and says where the container ended up.

import { toast, toastError } from '../ui.js';
import { Jobs, Containers } from '../api.js';

const STORE = 'docman.jobs';
const GIVE_UP_MS = 20 * 60 * 1000;

/**
 * Follow a job until it ends.
 * @param {object} job as returned by the API
 * @param {{onDone?: (newId: string) => void}} options called with the
 *   container's new ID when the job finished, including a failed start
 */
export function followJob(job, { onDone } = {}) {
  remember(job);
  const verb = job.kind === 'update' ? 'Applying changes to' : 'Recreating';
  const note = toast(`${verb} ${job.container}…`, 'info', 0);
  const text = note && note.querySelector('.grow');
  const say = (message) => { if (text) text.textContent = message; };
  const started = Date.now();
  let offlineSince = 0;

  const finish = () => { forget(job.id); if (note) note.remove(); };

  const poll = async () => {
    let current;
    try {
      current = await Jobs.get(job.id);
      offlineSince = 0;
    } catch (err) {
      if (err.status === 404) {
        // DocMan restarted since the job began (it recreated itself, or was
        // restarted). The job is gone, but the container can be found by name.
        finish();
        try {
          const container = await Containers.one(job.container);
          toast(`${job.container} is back`, 'ok');
          if (onDone) onDone(container.id);
        } catch { toast(`DocMan restarted while ${job.container} was being recreated; check the container list`, 'warn', 9000); }
        return;
      }
      if (err.status === 401) {
        finish();
        toast(`Sign in again to see whether ${job.container} finished`, 'warn', 9000);
        return;
      }
      // The connection is down, most often because the container being
      // recreated is the proxy the browser reaches DocMan through.
      if (!offlineSince) offlineSince = Date.now();
      say(`Lost the connection to DocMan while ${job.container} is being recreated. The change continues on the server; reconnecting…`);
      if (Date.now() - started > GIVE_UP_MS) {
        finish();
        toast('DocMan could not be reached for a long time; reload the page to see the result', 'bad', 0);
        return;
      }
      setTimeout(poll, 2000);
      return;
    }

    if (current.state === 'running') {
      say(`${verb} ${job.container}: ${current.step}…`);
      setTimeout(poll, 1000);
      return;
    }
    finish();
    if (current.state === 'self') {
      awaitSelfRecreate(job.container);
    } else if (current.state === 'done') {
      toast(job.kind === 'update' ? `Configuration applied to ${job.container}` : `${job.container} recreated`, 'ok');
      if (onDone) onDone(current.new_id);
    } else {
      toastError({ message: current.error || `${job.container} could not be recreated` });
      // Recreated but would not start: the old ID is gone, so follow the new one.
      if (current.new_id && onDone) onDone(current.new_id);
    }
  };
  poll();
}

/**
 * Pick up jobs a previous page load was following, so a reload in the middle
 * of an upgrade still reports how it ended.
 */
export function resumeJobs() {
  for (const job of remembered()) {
    followJob(job, {
      onDone: (id) => {
        // Only move the page when it is showing the container that changed.
        const hash = location.hash;
        if (id && (hash.includes(encodeURIComponent(job.old_id)) || hash.includes(job.old_id.slice(0, 12)))) {
          location.hash = `#/containers/${encodeURIComponent(id)}/overview`;
        }
      },
    });
  }
}

/**
 * DocMan answers a recreate of its own container before a helper stops it,
 * so wait for the replacement to start answering, then load it.
 */
export function awaitSelfRecreate(name) {
  toast('DocMan is recreating itself; this page reloads when it is back', 'info', 0);
  const started = Date.now();
  const poll = async () => {
    try {
      // Any response at all, even a sign-in redirect, means the new instance is up.
      await fetch('/', { cache: 'no-store' });
      location.hash = `#/containers/${encodeURIComponent(name)}/overview`;
      location.reload();
    } catch {
      if (Date.now() - started < 120000) setTimeout(poll, 2000);
      else toast('DocMan has not come back; check the host with docker ps -a', 'bad', 0);
    }
  };
  // The old instance keeps answering for a few seconds before it is stopped.
  setTimeout(poll, 8000);
}

function remembered() {
  try { return JSON.parse(sessionStorage.getItem(STORE)) || []; } catch { return []; }
}
function remember(job) {
  const list = remembered().filter((j) => j.id !== job.id);
  list.push({ id: job.id, kind: job.kind, container: job.container, old_id: job.old_id });
  try { sessionStorage.setItem(STORE, JSON.stringify(list)); } catch { /* storage unavailable */ }
}
function forget(id) {
  try { sessionStorage.setItem(STORE, JSON.stringify(remembered().filter((j) => j.id !== id))); } catch { /* ignore */ }
}

