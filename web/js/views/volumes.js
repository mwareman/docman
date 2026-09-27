// Volumes: what exists, which containers hold it, and creating or clearing them.

import {
  h, add, clear, icon, bytes, toast, toastError, confirmDialog, modal,
  field, notice, spinner, debounce, emptyState, copyText,
} from '../ui.js';
import { Volumes } from '../api.js';

export function volumesView(ctx) {
  ctx.setCrumbs('Volumes');

  let volumes = [];
  let query = '';
  let unusedOnly = false;

  const search = h('input', {
    type: 'search', placeholder: 'Filter by name or mount point',
    onInput: debounce((event) => { query = event.target.value.trim().toLowerCase(); draw(); }, 160),
  });

  ctx.setActions(
    h('div', { class: 'search' }, icon('search'), search),
    h('label', { class: 'check small' }, h('input', {
      type: 'checkbox',
      onChange: (event) => { unusedOnly = event.target.checked; draw(); },
    }), 'Unused only'),
    h('button', { class: 'btn', onClick: () => prune() }, icon('trash'), 'Prune unused'),
    h('button', { class: 'btn primary', onClick: () => create() }, icon('plus'), 'New volume'),
  );

  const body = h('tbody');
  const card = h('div', { class: 'card' },
    h('div', { class: 'table-wrap' },
      h('table', { class: 'tbl' },
        h('thead', {}, h('tr', {},
          h('th', { text: 'Volume' }),
          h('th', { text: 'Driver' }),
          h('th', { class: 'num', text: 'Size' }),
          h('th', { text: 'Attached to' }),
          h('th', { class: 'actions' }),
        )),
        body,
      )),
  );
  const emptyHost = h('div', {});
  add(ctx.host, [emptyHost, card]);

  function draw() {
    const rows = volumes.filter((v) => {
      if (unusedOnly && (v.used_by || []).length) return false;
      if (!query) return true;
      return `${v.Name} ${v.Mountpoint || ''}`.toLowerCase().includes(query);
    });
    clear(emptyHost);
    card.classList.toggle('hidden', rows.length === 0);
    if (!rows.length) {
      add(emptyHost, [emptyState(
        volumes.length ? 'Nothing matches that filter' : 'No volumes yet',
        volumes.length ? null : 'DocMan creates volumes for you when a deploy needs one, or you can add one here.',
        volumes.length ? null : h('button', { class: 'btn primary', onClick: () => create() }, icon('plus'), 'New volume'),
      )]);
      return;
    }
    clear(body);
    add(body, rows.map(row));
  }

  function row(volume) {
    const users = volume.used_by || [];
    return h('tr', {},
      h('td', {}, h('div', { class: 'row' },
        icon('database'),
        h('div', { style: { minWidth: 0 } },
          h('div', { class: 'truncate mono', style: { fontWeight: '560' }, text: volume.Name }),
          h('div', { class: 'small faint truncate', style: { maxWidth: '320px' }, text: volume.Mountpoint || '' }),
        ),
      )),
      h('td', {}, h('span', { class: 'badge plain', text: volume.Driver || 'local' })),
      h('td', { class: 'num', text: volume.UsageData && volume.UsageData.Size > 0 ? bytes(volume.UsageData.Size) : '—' }),
      h('td', {}, users.length
        ? h('div', { class: 'chips' }, users.slice(0, 3).map((use) => h('a', {
          class: 'pill', href: `#/containers/${encodeURIComponent(use.container)}`,
          title: `mounted at ${use.target}${use.read_only ? ' (read only)' : ''}`,
        }, use.container)), users.length > 3 ? h('span', { class: 'small faint', text: `+${users.length - 3}` }) : null)
        : h('span', { class: 'badge warn', text: 'unused' })),
      h('td', { class: 'actions' }, h('div', { class: 'btn-group' },
        h('a', {
          class: 'btn sm', href: `#/volumes/${encodeURIComponent(volume.Name)}`,
          title: 'Browse, download, upload and edit the files in this volume',
        }, icon('folder'), 'Explore'),
        h('button', { class: 'btn sm icon', title: 'Details', onClick: () => details(volume) }, icon('info')),
        h('button', {
          class: 'btn sm icon', title: 'Copy mount point',
          onClick: () => copyText(volume.Mountpoint || volume.Name, 'Mount point copied'),
        }, icon('copy')),
        h('button', { class: 'btn sm icon danger', title: 'Remove', onClick: () => remove(volume) }, icon('trash')),
      )),
    );
  }

  async function details(volume) {
    await modal({
      title: volume.Name,
      hideConfirm: true,
      cancelLabel: 'Close',
      render: ({ body: dialog }) => {
        const kv = h('dl', { class: 'kv' });
        const rows = [
          ['Driver', volume.Driver || 'local'],
          ['Mount point', h('code', { class: 'small', text: volume.Mountpoint || '—' })],
          ['Scope', volume.Scope || 'local'],
          ['Created', volume.CreatedAt || '—'],
          ['Size', volume.UsageData && volume.UsageData.Size > 0 ? bytes(volume.UsageData.Size) : 'not reported'],
        ];
        for (const [key, value] of rows) add(kv, [h('dt', { text: key }), h('dd', {}, value)]);

        const users = volume.used_by || [];
        add(dialog, [
          kv,
          h('div', { class: 'section-title', text: 'Mounted by' }),
          users.length
            ? h('div', { class: 'col', style: { gap: '6px' } }, users.map((use) => h('div', { class: 'row small' },
              h('span', { class: `dot ${use.state === 'running' ? 'ok' : 'bad'}` }),
              h('a', { href: `#/containers/${encodeURIComponent(use.container)}`, text: use.container }),
              icon('chevron', 'ico'),
              h('span', { class: 'mono', text: use.target }),
              use.read_only ? h('span', { class: 'badge warn', text: 'ro' }) : null,
            )))
            : h('span', { class: 'faint small', text: 'nothing is using this volume' }),
          Object.keys(volume.Options || {}).length
            ? h('div', {}, h('div', { class: 'section-title', text: 'Driver options' }),
              h('div', { class: 'chips' }, Object.entries(volume.Options).map(([k, v]) =>
                h('span', { class: 'pill', text: `${k}=${v}` }))))
            : null,
        ]);
        return null;
      },
    });
  }

  async function create() {
    const chosen = await modal({
      title: 'New volume',
      confirmLabel: 'Create volume',
      render: ({ body: dialog }) => {
        const name = h('input', { type: 'text', class: 'mono', placeholder: 'myapp-data', spellcheck: false });
        // Only the drivers this host actually has: local, plus any volume plugins.
        const driver = h('select', {}, h('option', { value: 'local', text: 'local — a directory on this host', selected: true }));
        const driverField = field('Driver', driver, 'The drivers installed on this host. Volume plugins add more.');
        const driverHint = driverField.querySelector('.help');
        Volumes.drivers().then((data) => {
          const drivers = data.drivers || ['local'];
          clear(driver);
          add(driver, drivers.map((d) => h('option', {
            value: d, text: d === 'local' ? 'local — a directory on this host' : `${d} — volume plugin`, selected: d === 'local',
          })));
          driverHint.textContent = drivers.length > 1
            ? 'The drivers installed on this host: local, and the volume plugins that are enabled.'
            : 'Only the built-in local driver is installed. Volume plugins, such as ones for network storage, add more.';
        }).catch(() => { /* keep local, which every host has */ });
        const options = h('textarea', { placeholder: 'one key=value per line, for example type=nfs', rows: 3 });
        add(dialog, [
          field('Name', name, 'Letters, digits, dot, dash and underscore.'),
          h('details', {},
            h('summary', { style: { cursor: 'pointer', fontSize: '13px', color: 'var(--text-dim)' } }, 'Driver and options'),
            h('div', { class: 'col', style: { marginTop: '10px' } }, driverField, field('Options', options)),
          ),
        ]);
        return {
          submit: () => {
            const parsed = {};
            for (const raw of options.value.split('\n')) {
              const line = raw.trim();
              const eq = line.indexOf('=');
              if (eq > 0) parsed[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
            }
            return { name: name.value.trim(), driver: driver.value || 'local', options: parsed };
          },
        };
      },
    });
    if (!chosen || !chosen.name) return;
    try {
      await Volumes.create({ name: chosen.name, driver: chosen.driver, options: chosen.options });
      toast(`Volume ${chosen.name} created`, 'ok');
      load();
      ctx.refreshCounts();
    } catch (err) { toastError(err); }
  }

  async function remove(volume) {
    const users = volume.used_by || [];
    const ok = await confirmDialog({
      title: `Delete ${volume.Name}?`,
      message: 'Everything stored in this volume is deleted permanently.',
      confirmLabel: 'Delete volume',
      extra: users.length
        ? notice(`${users.length} container(s) still mount this volume: ${users.map((u) => u.container).join(', ')}. Docker will refuse until they are removed.`, 'warn')
        : null,
    });
    if (!ok) return;
    try {
      await Volumes.remove(volume.Name, false);
      toast('Volume deleted', 'ok');
      load();
      ctx.refreshCounts();
    } catch (err) {
      toastError(err);
    }
  }

  async function prune() {
    const ok = await confirmDialog({
      title: 'Prune unused volumes?',
      message: 'Every volume that no container mounts is deleted, along with everything stored in it. This cannot be undone.',
      confirmLabel: 'Prune volumes',
    });
    if (!ok) return;
    try {
      const report = await Volumes.prune();
      const count = (report.VolumesDeleted || []).length;
      toast(count ? `Deleted ${count} volume(s), reclaimed ${bytes(report.SpaceReclaimed)}` : 'Nothing to prune', 'ok');
      load();
      ctx.refreshCounts();
    } catch (err) { toastError(err); }
  }

  function load() {
    Volumes.list().then((data) => {
      volumes = data.volumes || [];
      draw();
    }).catch((err) => {
      clear(emptyHost);
      add(emptyHost, [notice(err.message, 'bad')]);
    });
  }

  add(emptyHost, [spinner()]);
  load();
  return { dispose() {} };
}
