// Overview: live host metrics with a per-container breakdown.

import { h, add, clear, icon, bytes, rate, pct, num, dur, notice } from '../ui.js';
import { System, socket } from '../api.js';
import { sparkline, dualSparkline, gauge, stackBar, seriesColor } from '../charts.js';

export function dashboardView(ctx) {
  ctx.setCrumbs('Overview');

  const cpuTile = tile('Host CPU', 'cpu');
  const memTile = tile('Memory', 'database');
  const ioTile = tile('Disk I/O', 'disk');
  const runTile = tile('Containers', 'box');

  const cpuCanvas = h('canvas');
  const memCanvas = h('canvas');
  const ioCanvas = h('canvas');
  const cpuGaugeCanvas = h('canvas', { style: { width: '54px', height: '54px' } });

  add(cpuTile.body, [h('div', { class: 'row' },
    h('div', { class: 'grow' }, cpuTile.value, cpuTile.note),
    cpuGaugeCanvas,
  ), cpuCanvas]);
  add(memTile.body, [memTile.value, memTile.note, memTile.meter, memCanvas]);
  add(ioTile.body, [ioTile.value, ioTile.note, ioCanvas]);
  add(runTile.body, [runTile.value, runTile.note, runTile.extra]);

  const perCpuHost = h('div', { class: 'row wrap', style: { gap: '4px' } });
  const breakdownBody = h('tbody');
  const shareHost = h('div', { class: 'col', style: { gap: '6px' } });
  const infoHost = h('dl', { class: 'kv' });
  const storageHost = h('div', { class: 'col' });
  const warnHost = h('div', {});

  add(ctx.host, [
    warnHost,
    h('div', { class: 'grid c4', style: { marginBottom: '14px' } },
      cpuTile.node, memTile.node, ioTile.node, runTile.node),
    h('div', { class: 'card', style: { marginBottom: '14px' } },
      h('div', { class: 'card-head' },
        icon('activity'),
        h('h2', { text: 'Container share of the host' }),
        h('div', { class: 'grow' }),
        h('span', { class: 'small faint', text: 'live' }),
      ),
      h('div', { class: 'card-body tight' }, shareHost),
      h('div', { class: 'table-wrap' },
        h('table', { class: 'tbl' },
          h('thead', {}, h('tr', {},
            h('th', { text: 'Container' }),
            h('th', { class: 'num', text: 'CPU' }),
            h('th', { class: 'num', text: 'Memory' }),
            h('th', { class: 'num', text: 'Disk read' }),
            h('th', { class: 'num', text: 'Disk write' }),
            h('th', { class: 'num', text: 'Net in' }),
            h('th', { class: 'num', text: 'Net out' }),
            h('th', { class: 'num', text: 'PIDs' }),
          )),
          breakdownBody,
        )),
    ),
    h('div', { class: 'grid c2' },
      h('div', { class: 'card' },
        h('div', { class: 'card-head' }, icon('cpu'), h('h2', { text: 'Per-core load' })),
        h('div', { class: 'card-body' }, perCpuHost),
      ),
      h('div', { class: 'card' },
        h('div', { class: 'card-head' }, icon('info'), h('h2', { text: 'Docker host' })),
        h('div', { class: 'card-body' }, infoHost),
      ),
      h('div', { class: 'card' },
        h('div', { class: 'card-head' }, icon('layers'), h('h2', { text: 'Storage' })),
        h('div', { class: 'card-body' }, storageHost),
      ),
    ),
  ]);

  const cpuChart = sparkline(cpuCanvas, { max: 100 });
  const memChart = sparkline(memCanvas, { max: 100 });
  const ioChart = dualSparkline(ioCanvas);
  const cpuGauge = gauge(cpuGaugeCanvas, { thickness: 6 });

  renderInfo(ctx.state.system, infoHost);
  loadStorage(storageHost);

  const conn = socket('/api/stream/host', {
    onJSON: (message) => {
      if (message.type !== 'host') return;
      if (message.host) applyHost(message.host);
      applyContainers(message.containers || [], message.host);
    },
    onDown: () => { /* the socket retries on its own */ },
  });

  function applyHost(sample) {
    cpuTile.value.textContent = pct(sample.cpu_percent, 1);
    cpuTile.note.textContent =
      `${sample.ncpu} cores · user ${pct(sample.cpu_user, 0)} · system ${pct(sample.cpu_system, 0)} · wait ${pct(sample.cpu_iowait, 0)}`;
    cpuChart.push(sample.cpu_percent);
    cpuGauge.set(sample.cpu_percent);

    const mem = sample.memory || {};
    const used = sample.mem_used || 0;
    const total = mem.total || 1;
    const share = (used / total) * 100;
    memTile.value.textContent = bytes(used);
    memTile.value.append(h('small', { text: `of ${bytes(total)}` }));
    memTile.note.textContent = `${pct(share, 0)} in use · cache ${bytes(mem.cached || 0)}`
      + (mem.swap_total ? ` · swap ${bytes(sample.swap_used || 0)}` : '');
    memTile.meter.className = `meter ${share > 90 ? 'bad' : share > 75 ? 'warn' : 'ok'}`;
    memTile.meter.firstChild.style.width = `${Math.min(100, share)}%`;
    memChart.push(share);

    ioTile.value.textContent = rate(sample.disk_read_bytes + sample.disk_write_bytes);
    ioTile.note.textContent =
      `read ${rate(sample.disk_read_bytes)} · write ${rate(sample.disk_write_bytes)} · ${Math.round(sample.disk_read_ops + sample.disk_write_ops)} iops`;
    ioChart.push(sample.disk_read_bytes, sample.disk_write_bytes);

    clear(perCpuHost);
    add(perCpuHost, (sample.per_cpu_percent || []).map((value, index) =>
      h('div', {
        title: `core ${index}: ${pct(value, 0)}`,
        style: {
          width: '26px', height: '46px', borderRadius: '5px',
          background: 'var(--surface-3)', position: 'relative', overflow: 'hidden',
        },
      },
        h('i', {
          style: {
            position: 'absolute', bottom: '0', left: '0', right: '0',
            height: `${Math.max(2, value)}%`,
            background: value > 85 ? 'var(--bad)' : value > 60 ? 'var(--warn)' : 'var(--accent)',
          },
        }),
      )));
    if (!(sample.per_cpu_percent || []).length) {
      add(perCpuHost, [h('span', { class: 'faint small', text: 'per-core figures are not available' })]);
    }

    if (sample.uptime) {
      const uptimeRow = infoHost.querySelector('[data-uptime]');
      if (uptimeRow) {
        uptimeRow.textContent = `${dur(sample.uptime)} · load ${sample.load1.toFixed(2)} ${sample.load5.toFixed(2)} ${sample.load15.toFixed(2)}`;
      }
    }
  }

  function applyContainers(samples, host) {
    const running = samples.length;
    runTile.value.textContent = String(ctx.state.counts.containers || running);
    runTile.note.textContent = `${running} running`;
    clear(runTile.extra);
    add(runTile.extra, [h('a', { href: '#/containers', class: 'small', text: 'Manage containers →' })]);

    const sorted = samples.slice().sort((a, b) => b.cpu_percent - a.cpu_percent);
    clear(breakdownBody);
    if (!sorted.length) {
      add(breakdownBody, [h('tr', {}, h('td', { colSpan: 8, class: 'faint center', text: 'No running containers' }))]);
    }
    add(breakdownBody, sorted.map((s) => h('tr', { class: 'click', onClick: () => { location.hash = `#/containers/${s.id}/stats`; } },
      h('td', {}, h('div', { class: 'row' },
        h('span', { class: 'dot', style: { background: seriesColor(s.name || s.id) } }),
        h('span', { class: 'truncate', text: s.name || s.id.slice(0, 12) }),
      )),
      h('td', { class: 'num', text: pct(s.cpu_percent, 1) }),
      h('td', { class: 'num', text: bytes(s.mem_usage) }),
      h('td', { class: 'num', text: rate(s.blk_read_ps) }),
      h('td', { class: 'num', text: rate(s.blk_write_ps) }),
      h('td', { class: 'num', text: rate(s.net_rx_ps) }),
      h('td', { class: 'num', text: rate(s.net_tx_ps) }),
      h('td', { class: 'num', text: num(s.pids) }),
    )));

    // CPU and memory share, stacked by container.
    clear(shareHost);
    const cores = host ? host.ncpu || 1 : 1;
    const cpuParts = sorted.slice(0, 10).map((s) => ({
      label: s.name, value: s.cpu_percent / cores, color: seriesColor(s.name || s.id),
      text: pct(s.cpu_percent, 1),
    }));
    const memTotal = host && host.memory ? host.memory.total : 0;
    const memParts = sorted.slice(0, 10).map((s) => ({
      label: s.name, value: s.mem_usage, color: seriesColor(s.name || s.id), text: bytes(s.mem_usage),
    }));
    if (memTotal) {
      const claimed = memParts.reduce((sum, p) => sum + p.value, 0);
      memParts.push({ label: 'host and cache', value: Math.max(0, memTotal - claimed), color: 'var(--surface-3)', text: bytes(Math.max(0, memTotal - claimed)) });
    }
    add(shareHost, [
      labelledBar('CPU', cpuParts),
      labelledBar('Memory', memParts),
      h('div', { class: 'chips', style: { marginTop: '2px' } },
        sorted.slice(0, 10).map((s) => h('span', { class: 'badge plain' },
          h('span', { class: 'dot', style: { background: seriesColor(s.name || s.id) } }),
          s.name || s.id.slice(0, 12)))),
    ]);
  }

  if (ctx.state.system && ctx.state.system.metrics_available === false) {
    add(warnHost, [notice(
      'Host CPU, memory and disk figures are unavailable, so only per-container statistics are shown.',
      'warn',
      h('div', { class: 'small faint', text: 'This happens when the docker engine is remote or not running on Linux.' }),
    )]);
    warnHost.style.marginBottom = '14px';
  }

  async function loadStorage(host) {
    try {
      const df = await System.df();
      clear(host);
      add(host, [
        h('dl', { class: 'kv' },
          h('dt', { text: 'Image layers' }), h('dd', { text: bytes(df.layers_bytes) }),
          h('dt', { text: 'Images' }), h('dd', { text: `${num(df.image_count)} · ${bytes(df.images_bytes)}` }),
          h('dt', { text: 'Volumes' }), h('dd', { text: `${num(df.volume_count)} · ${df.volumes_bytes ? bytes(df.volumes_bytes) : 'size not reported'}` }),
        ),
        h('div', { class: 'row', style: { marginTop: '10px' } },
          h('a', { href: '#/images', class: 'btn sm' }, icon('layers'), 'Images'),
          h('a', { href: '#/volumes', class: 'btn sm' }, icon('database'), 'Volumes'),
        ),
      ]);
    } catch (err) {
      clear(host);
      add(host, [h('div', { class: 'faint small', text: err.message })]);
    }
  }

  return { dispose: () => conn.close() };
}

function tile(label, iconName) {
  const value = h('div', { class: 'value tabular', text: '—' });
  const note = h('div', { class: 'note' });
  const meter = h('div', { class: 'meter' }, h('i'));
  const extra = h('div', { class: 'small' });
  const body = h('div', { class: 'col', style: { gap: '4px' } });
  const node = h('div', { class: 'stat' },
    h('div', { class: 'row' }, icon(iconName, 'ico'), h('span', { class: 'label', text: label })),
    body,
  );
  return { node, body, value, note, meter, extra };
}

function labelledBar(label, parts) {
  const bar = h('div', {});
  stackBar(bar, parts);
  return h('div', {},
    h('div', { class: 'small faint', style: { marginBottom: '4px' }, text: label }),
    bar,
  );
}

function renderInfo(system, host) {
  clear(host);
  if (!system) {
    add(host, [h('div', { class: 'faint small', text: 'Docker information is unavailable.' })]);
    return;
  }
  const info = system.docker || {};
  const rows = [
    ['Engine', `${info.ServerVersion || '?'} (API ${(system.docker_version || {}).APIVersion || '?'})`],
    ['Host', info.Name || '—'],
    ['Platform', `${info.OperatingSystem || info.OSType || '?'} · ${info.Architecture || '?'}`],
    ['Kernel', info.KernelVersion || '—'],
    ['Storage driver', info.Driver || '—'],
    ['Cgroups', `${info.CgroupVersion || '?'} (${info.CgroupDriver || '?'})`],
    ['Socket', system.endpoint || '—'],
    ['DocMan', `${(system.docman || {}).version || 'dev'} · ${(system.docman || {}).platform || ''}`],
  ];
  for (const [key, value] of rows) {
    add(host, [h('dt', { text: key }), h('dd', { text: value })]);
  }
  add(host, [h('dt', { text: 'Uptime' }), h('dd', { 'data-uptime': '1', text: '—' })]);
}
