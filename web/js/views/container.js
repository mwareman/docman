// One container: overview, live logs, live statistics, console and configuration.

import {
  h, add, clear, icon, bytes, rate, pct, num, when, ago, stateClass,
  toast, toastError, confirmDialog, modal, field, notice, spinner, copyText, debounce,
} from '../ui.js';
import { Containers, Volumes, socket } from '../api.js';
import { sparkline, dualSparkline } from '../charts.js';
import { createTerminal } from '../term.js';
import { specForm, volumeDatalist, untokenize } from './specform.js';
import { showInspect } from './inspect.js';
import { followJob } from './jobs.js';

const TABS = [
  ['overview', 'Overview', 'info'],
  ['logs', 'Logs', 'logs'],
  ['stats', 'Statistics', 'activity'],
  ['console', 'Console', 'terminal'],
  ['config', 'Configuration', 'settings'],
];

/**
 * Ask to recreate a container, then do it. Shared by the container's own page
 * and the container list.
 *
 * Whether to pull first follows where the image came from: an image from a
 * registry is pulled, so a newer version there is fetched before rebuilding;
 * an uploaded or locally built image is not, because it is in no registry and
 * the version on this host is the one to use.
 * @param {object} container a container view
 * @param {{onDone?: (newId: string) => void}} options
 */
export async function recreateContainer(container, { onDone } = {}) {
  const origin = container.image_origin || (container.image_uploaded ? 'uploaded' : 'unknown');
  const fromRegistry = origin === 'registry' || origin === 'repository' || origin === 'unknown';
  const input = h('input', { type: 'checkbox', checked: fromRegistry });
  let note = '';
  if (container.update_available && container.update_source === 'repository') {
    note = `A newer ${container.update_image} is in the GitHub repository it was installed from. It is downloaded and installed first, then the container is rebuilt from it.`;
  } else if (container.update_available && container.update_source === 'registry') {
    note = `A newer ${container.update_image} is available from its registry. It is pulled first, then the container is rebuilt from it.`;
  } else if (container.update_available) {
    note = `A newer ${container.update_image} is already on this host; the container is rebuilt from it.`;
  } else if (origin === 'uploaded') {
    note = 'This image was uploaded as an archive, so the recreate uses the version already on this host.';
  } else if (origin === 'local') {
    note = 'This image was built or loaded on this host and is not in a registry, so there is nothing to pull.';
  } else if (origin === 'repository') {
    note = 'The image was installed from a GitHub repository, so its newest pre-built version there is installed first.';
  } else if (origin === 'registry') {
    note = 'The image came from a registry, so its newest version is pulled first.';
  }
  const pullFirst = h('div', { style: { marginTop: '10px' } },
    h('label', { class: 'check' }, input, 'Pull the newest version of the image first'),
    note ? h('div', { class: 'small faint', style: { marginTop: '4px' }, text: note }) : null);
  const ok = await confirmDialog({
    title: `Recreate ${container.name}?`,
    message: 'The container is replaced by a new one with the same configuration. Named volumes and any fixed address are kept; anything written to the container filesystem is lost.',
    confirmLabel: 'Recreate',
    danger: false,
    extra: pullFirst,
  });
  if (!ok) return;
  try {
    // The server runs the pull and rebuild as a job and answers at once, so
    // they finish even if this page loses its connection along the way.
    const res = await Containers.recreate(container.id, { pull: input.checked });
    followJob(res.job, { onDone });
  } catch (err) {
    toastError(err);
  }
}

// A recreate replaces the container, so the id this page was opened with no
// longer exists: move to the replacement's id, keeping the tab.
function followRecreated(ctx, newId, tab) {
  if (!newId) return ctx.reload();
  const hash = `#/containers/${encodeURIComponent(newId)}/${tab}`;
  // Setting the hash re-routes by itself; only reload when it is unchanged.
  if (location.hash === hash) ctx.reload();
  else location.hash = hash;
}

export function containerView(ctx) {
  const id = ctx.params.id;
  const tab = ctx.params.tab || 'overview';
  let disposer = null;

  ctx.flush();
  const tabsBar = h('div', { class: 'tabs' },
    ...TABS.map(([key, label, iconName]) => h('a', {
      href: `#/containers/${encodeURIComponent(id)}/${key}`,
      class: key === tab ? 'on' : '',
    }, icon(iconName), h('span', { text: label }))));

  const pane = h('div', { class: 'grow', style: { minHeight: '0', display: 'flex', flexDirection: 'column' } });
  add(ctx.host, [tabsBar, pane]);

  ctx.setCrumbs(
    h('a', { href: '#/containers', class: 'muted', text: 'Containers' }),
    h('h1', { text: id.length > 20 ? id.slice(0, 12) : id }),
  );

  Containers.one(id).then((container) => {
    ctx.setCrumbs(
      h('a', { href: '#/containers', class: 'muted', text: 'Containers' }),
      h('div', { class: 'row' },
        h('h1', { text: container.name }),
        h('span', { class: `badge ${stateClass(container.state)}`, text: container.state }),
        container.update_available
          ? h('span', { class: 'badge info', title: container.update_source === 'registry' ? `A newer ${container.update_image} is in its registry` : `A newer ${container.update_image} is on this host` }, icon('download'), 'Update available')
          : null,
      ),
    );
    ctx.setActions(...quickActions(container, ctx));
    disposer = mountTab(tab, pane, container, ctx);
  }).catch((err) => {
    // An old link or a page left open across a recreate: go to the container
    // that took this one's place, on the same tab.
    if (err.payload && err.payload.moved_to) {
      location.replace(`#/containers/${encodeURIComponent(err.payload.moved_to)}/${tab}`);
      return;
    }
    add(pane, [h('div', { class: 'content' }, notice(err.message, 'bad'))]);
  });

  return {
    dispose: () => {
      if (disposer && typeof disposer.dispose === 'function') disposer.dispose();
    },
  };
}

function mountTab(tab, pane, container, ctx) {
  switch (tab) {
    case 'logs': return logsTab(pane, container, ((ctx.state.settings || {}).settings || {}).log_tail);
    case 'stats': return statsTab(pane, container);
    case 'console': return consoleTab(pane, container);
    case 'config': return configTab(pane, container, ctx);
    default: return overviewTab(pane, container, ctx);
  }
}

function quickActions(container, ctx) {
  const running = container.state === 'running';
  const act = async (action, query = '') => {
    try {
      await Containers.action(container.id, action, query);
      toast(`${container.name}: ${action}`, 'ok', 2600);
      setTimeout(() => ctx.reload(), 500);
    } catch (err) {
      toastError(err);
    }
  };
  const nodes = [h('button', {
    class: 'btn', title: 'Show the full configuration Docker holds, as JSON',
    onClick: () => showInspect(`Inspect ${container.name}`, () => Containers.inspect(container.id), `${container.name}-inspect.json`),
  }, icon('code'), 'Inspect')];
  if (container.state === 'paused') {
    nodes.push(h('button', { class: 'btn primary', onClick: () => act('unpause') }, icon('play'), 'Resume'));
    nodes.push(h('button', { class: 'btn', onClick: () => act('stop') }, icon('stop'), 'Stop'));
  } else if (running) {
    nodes.push(h('button', { class: 'btn', onClick: () => act('stop') }, icon('stop'), 'Stop'));
    nodes.push(h('button', { class: 'btn', onClick: () => act('restart') }, icon('restart'), 'Restart'));
    nodes.push(h('button', { class: 'btn', onClick: () => act('pause') }, icon('pause'), 'Pause'));
  } else {
    nodes.push(h('button', { class: 'btn primary', onClick: () => act('start') }, icon('play'), 'Start'));
  }
  return nodes;
}

// ---------- overview ----------

function overviewTab(pane, container, ctx) {
  const host = h('div', { class: 'content' });
  add(pane, [host]);
  const inspectHost = h('div', {});

  const addressCard = h('div', { class: 'card' });
  renderAddress(addressCard, container, ctx);

  add(host, [
    h('div', { class: 'grid c2' },
      h('div', { class: 'card' },
        h('div', { class: 'card-head' }, icon('box'), h('h2', { text: 'Container' })),
        h('div', { class: 'card-body' }, inspectHost),
      ),
      addressCard,
    ),
    h('div', { class: 'card', style: { marginTop: '14px' } },
      h('div', { class: 'card-head' }, icon('alert'), h('h2', { text: 'Lifecycle' })),
      h('div', { class: 'card-body' },
        container.update_available
          ? h('div', { style: { marginBottom: '12px' } }, notice(container.update_source === 'registry'
            ? `A newer version of ${container.update_image} is available from its registry. Choose Recreate to pull it and rebuild this container from it.`
            : `A newer version of ${container.update_image} is on this host. This container still runs the version it was created with; choose Recreate to switch it to the new one.`,
          'info'))
          : null,
        lifecycleControls(container, ctx)),
    ),
  ]);

  add(inspectHost, [spinner()]);
  Containers.inspect(container.id).then((inspect) => {
    clear(inspectHost);
    const config = inspect.Config || {};
    const state = inspect.State || {};
    const hostConfig = inspect.HostConfig || {};
    const rows = [
      ['ID', h('span', { class: 'mono small' }, inspect.Id ? inspect.Id.slice(0, 24) : '—',
        h('button', {
          class: 'btn sm ghost icon', title: 'Copy full id',
          onClick: () => copyText(inspect.Id, 'Container id copied'),
        }, icon('copy')))],
      ['Image', h('div', {}, h('div', { class: 'mono small', text: config.Image || '—' }),
        h('div', { class: 'small faint mono', text: (inspect.Image || '').replace('sha256:', '').slice(0, 12) }))],
      ['Command', h('code', { class: 'small', text: untokenize([...(config.Entrypoint || []), ...(config.Cmd || [])]) || '—' })],
      ['Created', `${when(Date.parse(inspect.Created) / 1000)} (${ago(Date.parse(inspect.Created) / 1000)})`],
      ['Started', state.StartedAt && !state.StartedAt.startsWith('0001')
        ? `${ago(Date.parse(state.StartedAt) / 1000)}${state.Running ? '' : ' (stopped)'}`
        : 'never'],
      ['Restart policy', `${(hostConfig.RestartPolicy || {}).Name || 'no'}${state.Restarting ? ' · restarting' : ''}`],
      ['Restarts', String(inspect.RestartCount || 0)],
      ['Exit code', state.Running ? '—' : String(state.ExitCode ?? '—')],
      ['User', config.User || 'root'],
      ['Working dir', config.WorkingDir || '/'],
      ['Log driver', (hostConfig.LogConfig || {}).Type || '—'],
    ];
    if (state.Health) {
      rows.push(['Health', h('span', {
        class: `badge ${state.Health.Status === 'healthy' ? 'ok' : state.Health.Status === 'starting' ? 'warn' : 'bad'}`,
        text: `${state.Health.Status} · ${state.Health.FailingStreak || 0} failing`,
      })]);
    }
    const dl = h('dl', { class: 'kv' });
    for (const [key, value] of rows) {
      add(dl, [h('dt', { text: key }), h('dd', {}, value)]);
    }
    add(inspectHost, [dl]);

    const mounts = container.mounts || [];
    if (mounts.length) {
      add(inspectHost, [
        h('div', { class: 'section-title', style: { marginTop: '14px' }, text: 'Storage' }),
        h('div', { class: 'col', style: { gap: '6px' } }, mounts.map((m) => h('div', { class: 'row small' },
          h('span', { class: 'badge plain', text: m.Type }),
          h('span', { class: 'mono truncate grow', text: m.Name || m.Source }),
          icon('chevron', 'ico'),
          h('span', { class: 'mono', text: m.Destination }),
          m.RW ? null : h('span', { class: 'badge warn', text: 'ro' }),
        ))),
      ]);
    }

    const labels = container.labels || {};
    const labelKeys = Object.keys(labels).filter((k) => !k.startsWith('org.opencontainers.image.'));
    if (labelKeys.length) {
      add(inspectHost, [
        h('div', { class: 'section-title', style: { marginTop: '14px' }, text: 'Labels' }),
        h('div', { class: 'chips' }, labelKeys.slice(0, 12).map((key) =>
          h('span', { class: 'pill', title: `${key}=${labels[key]}`, text: `${key}=${trim(labels[key], 28)}` }))),
      ]);
    }
  }).catch((err) => {
    clear(inspectHost);
    add(inspectHost, [notice(err.message, 'bad')]);
  });

  return { dispose() {} };
}

function renderAddress(card, container, ctx) {
  clear(card);
  const pinned = !!container.pinned_ip;
  const body = h('div', { class: 'card-body' });
  add(card, [
    h('div', { class: 'card-head' }, icon('network'), h('h2', { text: 'Network' }),
      h('div', { class: 'grow' }),
      pinned ? h('span', { class: 'badge info', text: 'fixed address' }) : null),
    body,
  ]);

  const rows = h('dl', { class: 'kv' });
  const networks = Object.entries(container.networks || {});
  if (!networks.length) {
    add(rows, [h('dt', { text: 'Networks' }), h('dd', { class: 'faint', text: 'none' })]);
  }
  for (const [name, ip] of networks) {
    add(rows, [
      h('dt', { text: name }),
      h('dd', {}, h('span', { class: ip === container.pinned_ip ? 'pill accent' : 'pill', text: ip || 'no address' })),
    ]);
  }
  const published = (container.ports || []).filter((p) => p.PublicPort);
  add(rows, [
    h('dt', { text: 'Published' }),
    h('dd', {}, published.length
      ? h('div', { class: 'chips' }, published.map((p) => h('a', {
        class: 'pill', href: `${location.protocol}//${location.hostname}:${p.PublicPort}`,
        target: '_blank', rel: 'noreferrer',
      }, `${p.PublicPort} → ${p.PrivatePort}/${p.Type}`)))
      : h('span', { class: 'faint', text: 'no published ports' })),
  ]);
  add(body, [rows]);

  const pinButton = h('button', {
    class: pinned ? 'btn' : 'btn primary',
    onClick: async () => {
      pinButton.disabled = true;
      try {
        if (pinned) {
          const ok = await confirmDialog({
            title: 'Release the fixed address?',
            message: `${container.name} will go back to a docker-assigned address on the default bridge, which can change on restart.`,
            confirmLabel: 'Release address',
          });
          if (!ok) { pinButton.disabled = false; return; }
          await Containers.unpin(container.id);
          toast('Address released', 'ok');
        } else {
          const chosen = await modal({
            title: 'Fix this container’s address',
            confirmLabel: 'Fix address',
            render: ({ body: dialog }) => {
              const input = h('input', { type: 'text', class: 'mono', placeholder: 'leave empty for the next free address' });
              add(dialog, [
                h('p', { class: 'muted', style: { margin: 0 } },
                  'DocMan keeps a bridge network of its own for containers that need a stable address. ',
                  container.name, ' will be attached to it and taken off the default bridge.'),
                field('Address', input),
              ]);
              return { submit: () => ({ ip: input.value.trim() }) };
            },
          });
          if (!chosen) { pinButton.disabled = false; return; }
          const result = await Containers.pin(container.id, chosen.ip);
          toast(`${container.name} is now at ${result.ip}`, 'ok');
          if (result.note) toast(result.note, 'warn', 9000);
        }
        setTimeout(() => ctx.reload(), 400);
      } catch (err) {
        toastError(err);
        pinButton.disabled = false;
      }
    },
  }, icon('pin'), pinned ? 'Release fixed address' : 'Fix this address');

  add(body, [
    h('div', { class: 'row', style: { marginTop: '12px' } }, pinButton,
      h('a', { href: '#/network', class: 'btn ghost sm', text: 'All fixed addresses' })),
    pinned
      ? h('div', { class: 'small faint', style: { marginTop: '8px' } },
        `Reserved as ${container.pinned_ip}. DocMan reapplies it whenever the container is recreated.`)
      : null,
  ]);
}

function lifecycleControls(container, ctx) {
  const kill = h('button', {
    class: 'btn danger', onClick: async () => {
      const ok = await confirmDialog({
        title: `Kill ${container.name}?`,
        message: 'SIGKILL stops the process immediately without letting it shut down cleanly.',
        confirmLabel: 'Send SIGKILL',
      });
      if (!ok) return;
      try {
        await Containers.action(container.id, 'kill', '?signal=SIGKILL');
        toast('Signal sent', 'ok');
        setTimeout(() => ctx.reload(), 500);
      } catch (err) { toastError(err); }
    },
  }, icon('alert'), 'Kill');

  const recreate = h('button', {
    class: `btn${container.update_available ? ' primary' : ''}`,
    onClick: () => recreateContainer(container, {
      onDone: (id) => setTimeout(() => followRecreated(ctx, id, ctx.params.tab || 'overview'), 600),
    }),
  }, icon('refresh'), 'Recreate');

  const rename = h('button', {
    class: 'btn', onClick: async () => {
      const chosen = await modal({
        title: 'Rename container',
        confirmLabel: 'Rename',
        render: ({ body }) => {
          const input = h('input', { type: 'text', value: container.name, spellcheck: false });
          add(body, [field('Name', input, 'Letters, digits, dot, dash and underscore.')]);
          return { submit: () => ({ name: input.value.trim() }) };
        },
      });
      if (!chosen || !chosen.name || chosen.name === container.name) return;
      try {
        await Containers.rename(container.id, chosen.name);
        toast('Renamed', 'ok');
        setTimeout(() => ctx.reload(), 400);
      } catch (err) { toastError(err); }
    },
  }, icon('edit'), 'Rename');

  const remove = h('button', {
    class: 'btn danger', onClick: async () => {
      const withVolumes = h('label', { class: 'check', style: { marginTop: '10px' } },
        h('input', { type: 'checkbox' }), 'Also delete anonymous volumes');
      const ok = await confirmDialog({
        title: `Remove ${container.name}?`,
        message: 'The container is stopped and deleted. Named volumes are kept unless you say otherwise.',
        confirmLabel: 'Remove container',
        extra: withVolumes,
      });
      if (!ok) return;
      try {
        await Containers.remove(container.id, { force: true, volumes: withVolumes.querySelector('input').checked });
        toast(`${container.name} removed`, 'ok');
        location.hash = '#/containers';
      } catch (err) { toastError(err); }
    },
  }, icon('trash'), 'Remove');

  return h('div', { class: 'row wrap' }, recreate, rename, kill, remove);
}

// ---------- logs ----------

function logsTab(pane, container, preferredTail) {
  let follow = true;
  let wrap = true;
  let filter = '';
  const lines = [];
  const MAX_LINES = 5000;

  // Start from the "Log lines to load" preference, offered alongside the usual sizes.
  const initialTail = /^\d+$/.test(String(preferredTail || '')) ? String(preferredTail) : '500';
  const tails = [...new Set(['100', '500', '2000', initialTail])].sort((a, b) => Number(a) - Number(b));
  const view = h('div', { class: 'stream' });
  const tailSelect = h('select', { style: { width: 'auto' } },
    ...[...tails, 'all'].map((value) =>
      h('option', { value, text: value === 'all' ? 'all lines' : `last ${value}`, selected: value === initialTail })));
  const timestamps = h('input', { type: 'checkbox' });
  const searchInput = h('input', {
    type: 'search', placeholder: 'Filter lines',
    onInput: debounce((event) => { filter = event.target.value.toLowerCase(); render(); }, 160),
  });
  const followButton = h('button', { class: 'btn sm on', onClick: () => setFollow(!follow) }, icon('activity'), 'Following');
  const status = h('span', { class: 'small faint' });

  const bar = h('div', { class: 'stream-bar' },
    tailSelect,
    h('label', { class: 'check small' }, timestamps, 'Timestamps'),
    h('label', { class: 'check small' }, h('input', {
      type: 'checkbox', checked: true,
      onChange: (event) => { wrap = event.target.checked; view.style.whiteSpace = wrap ? 'pre-wrap' : 'pre'; },
    }), 'Wrap'),
    h('div', { class: 'search grow', style: { maxWidth: '280px' } }, icon('search'), searchInput),
    followButton,
    h('button', { class: 'btn sm ghost', onClick: () => { lines.length = 0; render(); } }, 'Clear'),
    h('button', {
      class: 'btn sm ghost', title: 'Download what is on screen',
      onClick: () => downloadText(`${container.name}-logs.txt`, lines.map((l) => l.text).join('')),
    }, icon('download')),
    status,
  );
  add(pane, [bar, view]);

  const setFollow = (value) => {
    follow = value;
    followButton.classList.toggle('on', follow);
    followButton.classList.toggle('primary', follow);
    if (follow) view.scrollTop = view.scrollHeight;
  };
  setFollow(true);

  view.addEventListener('scroll', () => {
    const atBottom = view.scrollHeight - view.scrollTop - view.clientHeight < 30;
    if (!atBottom && follow) setFollow(false);
  });

  let renderScheduled = false;
  function render() {
    if (renderScheduled) return;
    renderScheduled = true;
    requestAnimationFrame(() => {
      renderScheduled = false;
      clear(view);
      const frag = document.createDocumentFragment();
      for (const line of lines) {
        if (filter && !line.text.toLowerCase().includes(filter)) continue;
        frag.append(renderLine(line));
      }
      view.append(frag);
      if (follow) view.scrollTop = view.scrollHeight;
    });
  }

  function renderLine(line) {
    const node = h('span', { class: line.stream === 2 ? 'err' : '' });
    add(node, ansiToNodes(line.text));
    return node;
  }

  let conn = null;
  function connect() {
    if (conn) conn.close();
    lines.length = 0;
    render();
    status.textContent = 'connecting…';
    const tail = tailSelect.value;
    conn = socket(`/api/stream/logs/${encodeURIComponent(container.id)}`, {
      params: { tail, timestamps: timestamps.checked ? '1' : '0' },
      onOpen: () => { status.textContent = 'live'; },
      onDown: () => { status.textContent = 'reconnecting…'; },
      onJSON: (message) => {
        if (message.type === 'error') {
          status.textContent = message.error;
          return;
        }
        if (message.type !== 'log') return;
        lines.push({ stream: message.stream, text: message.data });
        while (lines.length > MAX_LINES) lines.shift();
        render();
      },
    });
  }

  tailSelect.addEventListener('change', connect);
  timestamps.addEventListener('change', connect);
  connect();

  return { dispose: () => conn && conn.close() };
}

const ANSI_COLORS = [
  '#0c1017', '#e05561', '#3fbf7f', '#d9a03a', '#4a9df0', '#b48ce8', '#39b8c2', '#c3ccd8',
  '#4a5769', '#ff7b86', '#5fe0a0', '#f5c65e', '#74bcff', '#cfb0ff', '#5fdce8', '#eef3fa',
];

/** Convert SGR colour sequences in log output into styled spans. */
function ansiToNodes(text) {
  const nodes = [];
  const pattern = /\x1b\[([0-9;]*)m|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[A-Za-z]/g;
  let cursor = 0;
  let style = null;
  let match;
  const push = (chunk) => {
    if (!chunk) return;
    if (!style) nodes.push(document.createTextNode(chunk));
    else nodes.push(h('span', { style, text: chunk }));
  };
  while ((match = pattern.exec(text)) !== null) {
    push(text.slice(cursor, match.index));
    cursor = match.index + match[0].length;
    if (match[1] === undefined) continue; // a non-colour sequence: drop it
    style = sgrStyle(match[1], style);
  }
  push(text.slice(cursor));
  return nodes;
}

function sgrStyle(paramText, current) {
  const codes = paramText === '' ? [0] : paramText.split(';').map((n) => parseInt(n, 10) || 0);
  let style = current ? { ...current } : null;
  for (let i = 0; i < codes.length; i++) {
    const code = codes[i];
    if (code === 0) style = null;
    else {
      style = style || {};
      if (code === 1) style.fontWeight = '600';
      else if (code === 2) style.opacity = '0.65';
      else if (code === 3) style.fontStyle = 'italic';
      else if (code === 4) style.textDecoration = 'underline';
      else if (code >= 30 && code <= 37) style.color = ANSI_COLORS[code - 30];
      else if (code >= 90 && code <= 97) style.color = ANSI_COLORS[code - 90 + 8];
      else if (code >= 40 && code <= 47) style.background = ANSI_COLORS[code - 40];
      else if (code === 39) delete style.color;
      else if (code === 49) delete style.background;
      else if (code === 38 && codes[i + 1] === 5) {
        style.color = ANSI_COLORS[codes[i + 2] % 16] || '#c3ccd8';
        i += 2;
      } else if (code === 38 && codes[i + 1] === 2) {
        style.color = `rgb(${codes[i + 2] || 0},${codes[i + 3] || 0},${codes[i + 4] || 0})`;
        i += 4;
      }
    }
  }
  return style && Object.keys(style).length ? style : null;
}

function downloadText(filename, text) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
  const link = h('a', { href: url, download: filename });
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// ---------- statistics ----------

function statsTab(pane, container) {
  const host = h('div', { class: 'content' });
  add(pane, [host]);

  const cpuCanvas = h('canvas', { style: { height: '60px' } });
  const memCanvas = h('canvas', { style: { height: '60px' } });
  const ioCanvas = h('canvas', { style: { height: '60px' } });
  const netCanvas = h('canvas', { style: { height: '60px' } });

  const cpuValue = h('div', { class: 'value tabular', text: '—' });
  const cpuNote = h('div', { class: 'note' });
  const memValue = h('div', { class: 'value tabular', text: '—' });
  const memNote = h('div', { class: 'note' });
  const memMeter = h('div', { class: 'meter' }, h('i'));
  const ioValue = h('div', { class: 'value tabular', text: '—' });
  const ioNote = h('div', { class: 'note' });
  const netValue = h('div', { class: 'value tabular', text: '—' });
  const netNote = h('div', { class: 'note' });
  const status = h('span', { class: 'small faint', text: 'connecting…' });
  const procHost = h('div', {});

  add(host, [
    h('div', { class: 'grid c2' },
      h('div', { class: 'stat' }, statHead('CPU', 'cpu', status), cpuValue, cpuNote, cpuCanvas),
      h('div', { class: 'stat' }, statHead('Memory', 'database'), memValue, memNote, memMeter, memCanvas),
      h('div', { class: 'stat' }, statHead('Disk I/O', 'disk'), ioValue, ioNote, ioCanvas),
      h('div', { class: 'stat' }, statHead('Network', 'network'), netValue, netNote, netCanvas),
    ),
    h('div', { class: 'card', style: { marginTop: '14px' } },
      h('div', { class: 'card-head' }, icon('activity'), h('h2', { text: 'Processes' }),
        h('div', { class: 'grow' }),
        h('button', { class: 'btn sm ghost', onClick: () => loadProcesses() }, icon('refresh'), 'Refresh')),
      h('div', { class: 'card-body' }, procHost),
    ),
  ]);

  const cpuChart = sparkline(cpuCanvas);
  const memChart = sparkline(memCanvas);
  const ioChart = dualSparkline(ioCanvas);
  const netChart = dualSparkline(netCanvas);

  async function loadProcesses() {
    clear(procHost);
    if (container.state !== 'running') {
      add(procHost, [h('div', { class: 'faint small', text: 'The container is not running.' })]);
      return;
    }
    add(procHost, [spinner()]);
    try {
      const top = await Containers.top(container.id);
      clear(procHost);
      const titles = top.Titles || [];
      const processes = top.Processes || [];
      add(procHost, [h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
        h('thead', {}, h('tr', {}, titles.map((t) => h('th', { text: t })))),
        h('tbody', {}, processes.map((proc) => h('tr', {}, proc.map((cell) =>
          h('td', { class: 'mono small', text: cell })))))))]);
      if (!processes.length) add(procHost, [h('div', { class: 'faint small', text: 'No processes reported.' })]);
    } catch (err) {
      clear(procHost);
      add(procHost, [notice(err.message, 'warn')]);
    }
  }
  loadProcesses();

  const conn = socket(`/api/stream/stats/${encodeURIComponent(container.id)}`, {
    onOpen: () => { status.textContent = 'live'; },
    onDown: () => { status.textContent = 'reconnecting…'; },
    onJSON: (message) => {
      if (message.type === 'error') { status.textContent = message.error; return; }
      if (message.type !== 'stats') return;
      const s = message.sample;
      status.textContent = 'live';

      cpuValue.textContent = pct(s.cpu_percent, 1);
      cpuNote.textContent = `${s.cpu_cores.toFixed(2)} of ${s.online_cpus} cores · ${num(s.pids)} processes`;
      cpuChart.push(s.cpu_percent);

      memValue.textContent = bytes(s.mem_usage);
      if (s.mem_limit) memValue.append(h('small', { text: `of ${bytes(s.mem_limit)}` }));
      memNote.textContent = s.mem_limit ? `${pct(s.mem_percent, 1)} of the limit` : 'no limit set';
      memMeter.className = `meter ${s.mem_percent > 90 ? 'bad' : s.mem_percent > 75 ? 'warn' : 'ok'}`;
      memMeter.firstChild.style.width = `${Math.min(100, s.mem_percent)}%`;
      memChart.push(s.mem_percent);

      ioValue.textContent = rate(s.blk_read_ps + s.blk_write_ps);
      ioNote.textContent = `read ${rate(s.blk_read_ps)} · write ${rate(s.blk_write_ps)} · total ${bytes(s.blk_read + s.blk_write)}`;
      ioChart.push(s.blk_read_ps, s.blk_write_ps);

      netValue.textContent = rate(s.net_rx_ps + s.net_tx_ps);
      netNote.textContent = `in ${rate(s.net_rx_ps)} · out ${rate(s.net_tx_ps)} · total ${bytes(s.net_rx + s.net_tx)}`;
      netChart.push(s.net_rx_ps, s.net_tx_ps);
    },
  });

  return { dispose: () => conn.close() };
}

function statHead(label, iconName, extra) {
  return h('div', { class: 'row' }, icon(iconName), h('span', { class: 'label', text: label }),
    h('div', { class: 'grow' }), extra || null);
}

// ---------- console ----------

function consoleTab(pane, container) {
  const shellInput = h('input', {
    type: 'text', class: 'mono', value: '', placeholder: 'automatic (bash, else sh)',
    style: { width: '260px' }, spellcheck: false,
  });
  const userInput = h('input', { type: 'text', placeholder: 'default user', style: { width: '140px' }, spellcheck: false });
  const status = h('span', { class: 'small faint', text: 'not connected' });
  const connectButton = h('button', { class: 'btn sm primary' }, icon('terminal'), 'Connect');
  const termHost = h('div', { class: 'term-host' });

  const bar = h('div', { class: 'stream-bar' },
    h('span', { class: 'small faint', text: 'Command' }), shellInput,
    h('span', { class: 'small faint', text: 'User' }), userInput,
    connectButton,
    h('button', {
      class: 'btn sm ghost', title: 'Clear the screen',
      onClick: () => { if (term) term.reset(); },
    }, 'Clear'),
    h('div', { class: 'grow' }),
    status,
  );
  add(pane, [bar, termHost]);

  let term = null;
  let conn = null;

  if (container.state !== 'running') {
    add(termHost, [h('div', { class: 'empty' },
      h('h3', { text: 'The container is not running' }),
      h('p', { text: 'Start it to open a console.', style: { margin: 0 } }))]);
    connectButton.disabled = true;
    return { dispose() {} };
  }

  const connect = () => {
    if (conn) { conn.close(); conn = null; }
    clear(termHost);
    term = createTerminal(termHost, {
      onData: (data) => conn && conn.send(JSON.stringify({ type: 'in', data })),
      onResize: (cols, rows) => conn && conn.send(JSON.stringify({ type: 'resize', cols, rows })),
    });
    term.notice(`Connecting to ${container.name}…`);
    status.textContent = 'connecting…';
    connectButton.disabled = true;

    conn = socket(`/api/stream/exec/${encodeURIComponent(container.id)}`, {
      retry: false,
      params: {
        cmd: shellInput.value.trim(),
        user: userInput.value.trim(),
        cols: term.cols,
        rows: term.rows,
      },
      onOpen: () => {
        status.textContent = 'connected';
        connectButton.disabled = false;
        connectButton.textContent = '';
        add(connectButton, [icon('refresh'), 'Reconnect']);
        term.focus();
      },
      onBinary: (buffer) => term && term.write(buffer),
      onJSON: (message) => {
        if (message.type === 'error') {
          status.textContent = 'failed';
          if (term) term.notice(`\r\n${message.error}`);
          connectButton.disabled = false;
        } else if (message.type === 'exit') {
          status.textContent = `exited (${message.code})`;
          if (term) term.notice(`\r\n[session ended with status ${message.code}]`);
        }
      },
      onDown: () => {
        status.textContent = 'disconnected';
        connectButton.disabled = false;
        if (term) term.notice('\r\n[connection closed]');
      },
    });
  };

  connectButton.addEventListener('click', connect);
  connect();

  return {
    dispose: () => {
      if (conn) conn.close();
      if (term) term.dispose();
    },
  };
}

// ---------- configuration ----------

function configTab(pane, container, ctx) {
  const host = h('div', { class: 'content' });
  add(pane, [host]);
  add(host, [spinner()]);

  Promise.all([Containers.spec(container.id), Volumes.list().catch(() => ({ volumes: [] }))])
    .then(([data, volumeData]) => {
      clear(host);
      const listID = 'docman-volume-list';
      const form = specForm(data.spec, {
        lockName: true, volumeListID: listID, pinnedIP: data.pinned_ip, selfName: container.name,
      });
      const problems = h('div', {});
      const startAfter = h('input', { type: 'checkbox', checked: data.running });
      const startedChecked = startAfter.checked;

      const apply = h('button', { class: 'btn primary' }, icon('check'), 'Apply changes');
      apply.addEventListener('click', async () => {
        clear(problems);
        const { spec, problems: found } = form.validate();
        if (found.length) {
          add(problems, [notice(found.join(' '), 'bad')]);
          return;
        }
        const ok = await confirmDialog({
          title: `Apply changes to ${container.name}?`,
          message: 'Docker cannot change these settings in place, so DocMan replaces the container with a new one carrying the same name, volumes and fixed address. Anything written to the container filesystem outside a volume is lost.',
          confirmLabel: 'Replace container',
          danger: false,
        });
        if (!ok) return;
        apply.disabled = true;
        try {
          // Left as it was, "start after applying" is not sent, so the server
          // keeps the container in exactly the state it was in, paused included.
          const start = startAfter.checked === startedChecked ? undefined : startAfter.checked;
          const res = await Containers.saveSpec(container.id, spec, start);
          followJob(res.job, {
            onDone: (id) => setTimeout(() => followRecreated(ctx, id, 'overview'), 300),
          });
        } catch (err) {
          toastError(err);
          apply.disabled = false;
        }
      });

      add(host, [
        notice(
          'Changing environment, command, ports or storage requires docker to build a new container. DocMan does that for you and keeps the name, volumes and any fixed address.',
          'info',
        ),
        data.pinned_ip
          ? h('div', { class: 'small faint', style: { margin: '8px 0' } },
            `Fixed address ${data.pinned_ip} will be reapplied.`)
          : null,
        volumeDatalist(listID, volumeData.volumes),
        h('div', { style: { marginTop: '14px' } }, form.node),
        problems,
        h('div', { class: 'row', style: { marginTop: '16px', position: 'sticky', bottom: '0' } },
          apply,
          h('label', { class: 'check' }, startAfter, 'Start after applying'),
          h('div', { class: 'grow' }),
          h('a', { class: 'btn ghost', href: `#/containers/${encodeURIComponent(container.id)}/overview`, text: 'Discard' }),
        ),
      ]);
    })
    .catch((err) => {
      clear(host);
      add(host, [notice(err.message, 'bad')]);
    });

  return { dispose() {} };
}

function trim(text, length) {
  const value = String(text || '');
  return value.length > length ? `${value.slice(0, length)}…` : value;
}
