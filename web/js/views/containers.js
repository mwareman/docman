// Container list: live state, quick actions, and the entry point for deploys.

import {
  h, add, clear, icon, bytes, pct, stateClass, toast, toastError,
  confirmDialog, debounce, emptyState,
} from '../ui.js';
import { Containers, socket } from '../api.js';
import { openDeployWizard } from './deploy.js';
import { recreateContainer } from './container.js';
import { showInspect } from './inspect.js';

export function containersView(ctx) {
  ctx.setCrumbs('Containers');

  let all = [];
  let stats = new Map();
  let filter = 'all';
  let query = '';

  const search = h('input', {
    type: 'search', placeholder: 'Filter by name, image or address',
    onInput: debounce((event) => { query = event.target.value.trim().toLowerCase(); draw(); }, 160),
  });
  const filterBar = h('div', { class: 'switchbar' },
    ...[['all', 'All'], ['running', 'Running'], ['stopped', 'Stopped']].map(([key, label]) =>
      h('button', {
        class: filter === key ? 'on' : '',
        text: label,
        onClick: (event) => {
          filter = key;
          [...filterBar.children].forEach((b) => b.classList.remove('on'));
          event.currentTarget.classList.add('on');
          draw();
        },
      })),
  );

  ctx.setActions(
    h('div', { class: 'search' }, icon('search'), search),
    filterBar,
    h('button', {
      class: 'btn primary',
      onClick: () => openDeployWizard({ onDone: () => { toast('Container created', 'ok'); ctx.refreshCounts(); } }),
    }, icon('plus'), 'New container'),
  );

  const body = h('tbody');
  const table = h('div', { class: 'card' },
    h('div', { class: 'table-wrap' },
      h('table', { class: 'tbl' },
        h('thead', {}, h('tr', {},
          h('th', { text: 'Container' }),
          h('th', { text: 'Image' }),
          h('th', { text: 'Address' }),
          h('th', { text: 'Ports' }),
          h('th', { class: 'num', text: 'CPU' }),
          h('th', { class: 'num', text: 'Memory' }),
          h('th', { text: 'State' }),
          h('th', { class: 'actions', text: '' }),
        )),
        body,
      )),
  );
  const emptyHost = h('div', {});
  add(ctx.host, [emptyHost, table]);

  function matches(container) {
    if (filter === 'running' && container.state !== 'running') return false;
    if (filter === 'stopped' && container.state === 'running') return false;
    if (!query) return true;
    const haystack = [
      container.name, container.image, container.primary_ip, container.pinned_ip,
      ...Object.values(container.networks || {}),
      ...(container.ports || []).map((p) => `${p.PublicPort || ''}:${p.PrivatePort}`),
    ].join(' ').toLowerCase();
    return haystack.includes(query);
  }

  function draw() {
    const rows = all.filter(matches);
    clear(emptyHost);
    table.classList.toggle('hidden', rows.length === 0);
    if (rows.length === 0) {
      add(emptyHost, [emptyState(
        all.length ? 'Nothing matches that filter' : 'No containers on this host yet',
        all.length ? 'Try a different search or state filter.' : 'Deploy one from an image to get started.',
        all.length ? null : h('button', {
          class: 'btn primary',
          onClick: () => openDeployWizard({ onDone: () => ctx.reload() }),
        }, icon('plus'), 'New container'),
      )]);
      return;
    }
    clear(body);
    add(body, rows.map(row));
  }

  function row(container) {
    const sample = stats.get(container.id);
    const open = (tab) => { location.hash = `#/containers/${container.id}${tab ? `/${tab}` : ''}`; };
    const running = container.state === 'running';

    return h('tr', { class: 'click', onClick: (event) => { if (!event.target.closest('button')) open(); } },
      h('td', {}, h('div', { class: 'row' },
        h('span', { class: `dot ${stateClass(container.state) === 'ok' ? 'ok' : stateClass(container.state) === 'bad' ? 'bad' : 'warn'}` }),
        h('div', { style: { minWidth: 0 } },
          h('div', { class: 'truncate', style: { fontWeight: '560' } }, container.name,
            container.self ? h('span', { class: 'badge info', style: { marginLeft: '6px' }, text: 'DocMan' }) : null),
          h('div', { class: 'small faint mono', text: container.id.slice(0, 12) }),
        ),
      )),
      h('td', {}, h('div', { class: 'truncate small', style: { maxWidth: '220px' }, text: container.image })),
      h('td', {}, addressCell(container)),
      h('td', {}, portsCell(container)),
      h('td', { class: 'num', text: sample ? pct(sample.cpu_percent, 1) : '—' }),
      h('td', { class: 'num', text: sample ? bytes(sample.mem_usage) : '—' }),
      h('td', {}, h('div', { class: 'chips' },
        h('span', { class: `badge ${stateClass(container.state)}`, text: container.status || container.state }),
        container.update_available ? h('a', {
          class: 'badge info', href: `#/containers/${container.id}/overview`,
          title: container.update_source === 'registry'
            ? `A newer ${container.update_image} is available from its registry. Recreate pulls it and rebuilds the container from it.`
            : `A newer ${container.update_image} is on this host. Recreate the container to switch to it.`,
          onClick: (event) => event.stopPropagation(),
        }, icon('download'), 'Update available') : null)),
      h('td', { class: 'actions' }, h('div', { class: 'btn-group' },
        container.state === 'paused'
          ? iconBtn('play', 'Resume', () => act(container, 'unpause'))
          : running
            ? iconBtn('stop', 'Stop', () => act(container, 'stop'))
            : iconBtn('play', 'Start', () => act(container, 'start')),
        iconBtn('restart', 'Restart', () => act(container, 'restart'), !running),
        iconBtn('refresh', container.update_available ? 'Recreate with the newer image' : 'Recreate',
          () => recreateContainer(container), false, container.update_available ? 'primary' : ''),
        iconBtn('code', 'Inspect', () => showInspect(`Inspect ${container.name}`,
          () => Containers.inspect(container.id), `${container.name}-inspect.json`)),
        iconBtn('logs', 'Logs', () => open('logs')),
        iconBtn('terminal', 'Console', () => open('console'), !running),
        iconBtn('trash', 'Remove', () => remove(container), false, 'danger'),
      )),
    );
  }

  function addressCell(container) {
    const nodes = [];
    if (container.pinned_ip) {
      nodes.push(h('span', { class: 'pill accent', title: 'Fixed address' }, container.pinned_ip));
    } else if (container.primary_ip) {
      nodes.push(h('span', { class: 'pill', text: container.primary_ip }));
    } else {
      nodes.push(h('span', { class: 'faint small', text: '—' }));
    }
    const names = Object.keys(container.networks || {});
    if (names.length) {
      nodes.push(h('div', { class: 'small faint truncate', style: { maxWidth: '160px' }, text: names.join(', ') }));
    }
    return h('div', {}, nodes);
  }

  function portsCell(container) {
    const published = (container.ports || []).filter((p) => p.PublicPort);
    if (!published.length) return h('span', { class: 'faint small', text: '—' });
    const seen = new Set();
    const chips = [];
    for (const p of published) {
      const key = `${p.PublicPort}:${p.PrivatePort}/${p.Type}`;
      if (seen.has(key)) continue;
      seen.add(key);
      const href = `${location.protocol}//${location.hostname}:${p.PublicPort}`;
      chips.push(h('a', {
        class: 'pill', href, target: '_blank', rel: 'noreferrer',
        title: `host ${p.PublicPort} → container ${p.PrivatePort}/${p.Type}`,
        onClick: (event) => event.stopPropagation(),
      }, `${p.PublicPort}→${p.PrivatePort}`));
    }
    return h('div', { class: 'chips' }, chips.slice(0, 4),
      chips.length > 4 ? h('span', { class: 'small faint', text: `+${chips.length - 4}` }) : null);
  }

  async function act(container, action) {
    try {
      await Containers.action(container.id, action);
      toast(`${container.name}: ${action}`, 'ok', 2600);
    } catch (err) {
      toastError(err);
    }
  }

  async function remove(container) {
    const withVolumes = h('label', { class: 'check', style: { marginTop: '10px' } },
      h('input', { type: 'checkbox' }), 'Also delete this container’s anonymous volumes');
    const ok = await confirmDialog({
      title: `Remove ${container.name}?`,
      message: container.state === 'running'
        ? 'The container is running. It will be stopped and deleted. Named volumes are kept.'
        : 'The container will be deleted. Named volumes are kept.',
      confirmLabel: 'Remove container',
      extra: withVolumes,
    });
    if (!ok) return;
    try {
      await Containers.remove(container.id, {
        force: true,
        volumes: withVolumes.querySelector('input').checked,
      });
      toast(`${container.name} removed`, 'ok');
      ctx.refreshCounts();
    } catch (err) {
      toastError(err);
    }
  }

  const conn = socket('/api/stream/containers', {
    onJSON: (message) => {
      if (message.type === 'error') return;
      if (message.type !== 'containers') return;
      all = message.containers || [];
      stats = new Map((message.stats || []).map((s) => [s.id, s]));
      draw();
    },
  });

  Containers.list().then((data) => {
    if (all.length) return;
    all = data.containers || [];
    draw();
  }).catch(toastError);

  return { dispose: () => conn.close() };
}

function iconBtn(name, title, onClick, disabled = false, extra = '') {
  return h('button', {
    class: `btn sm icon ${extra}`.trim(),
    title,
    'aria-label': title,
    disabled,
    onClick: (event) => { event.stopPropagation(); onClick(); },
  }, icon(name));
}
