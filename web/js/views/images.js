// Images: what is on the host, where it came from, and getting it running.

import {
  h, add, clear, icon, bytes, ago, when, shortID, toast, toastError,
  confirmDialog, modal, field, notice, spinner, debounce, emptyState, copyText,
} from '../ui.js';
import { Images, stream } from '../api.js';
import { openDeployWizard, openUploadDialog } from './deploy.js';

export function imagesView(ctx) {
  ctx.setCrumbs('Images');

  let images = [];
  let query = '';
  let danglingOnly = false;

  const search = h('input', {
    type: 'search', placeholder: 'Filter by repository or tag',
    onInput: debounce((event) => { query = event.target.value.trim().toLowerCase(); draw(); }, 160),
  });

  // Registry update checks: when they last ran, and a button to run one now.
  const checkedLabel = h('span', { class: 'small faint nowrap' });
  const showChecked = (summary) => {
    checkedLabel.textContent = summary && summary.checked_at
      ? `Checked for updates ${ago(summary.checked_at)}`
      : 'Not checked for updates yet';
    checkedLabel.title = summary && summary.next_check_at ? `Next automatic check ${when(summary.next_check_at)}` : '';
  };
  const refreshButton = h('button', {
    class: 'btn', title: 'Ask each image’s registry whether a newer version is available',
    onClick: async () => {
      refreshButton.disabled = true;
      const label = refreshButton.lastChild;
      label.textContent = 'Checking…';
      try {
        const summary = await Images.checkUpdates();
        showChecked(summary);
        const count = (summary.checks || []).filter((c) => c.status === 'update').length;
        toast(count ? `${count} image${count === 1 ? ' has' : 's have'} a newer version in the registry` : 'Every image from a registry is up to date', 'ok');
        load();
      } catch (err) { toastError(err); }
      label.textContent = 'Refresh';
      refreshButton.disabled = false;
    },
  }, icon('refresh'), h('span', { text: 'Refresh' }));
  Images.updates().then(showChecked).catch(() => showChecked(null));

  ctx.setActions(
    h('div', { class: 'search' }, icon('search'), search),
    h('label', { class: 'check small' }, h('input', {
      type: 'checkbox',
      onChange: (event) => { danglingOnly = event.target.checked; draw(); },
    }), 'Untagged only'),
    checkedLabel,
    refreshButton,
    h('button', { class: 'btn', onClick: () => prune() }, icon('trash'), 'Prune'),
    h('button', {
      class: 'btn', title: 'Import a docker save archive without creating a container',
      onClick: () => openUploadDialog({ onDone: () => load() }),
    }, icon('upload'), 'Upload image'),
    h('button', {
      class: 'btn primary',
      onClick: () => openDeployWizard({ onDone: () => ctx.refreshCounts() }),
    }, icon('plus'), 'New container'),
  );

  const body = h('tbody');
  const card = h('div', { class: 'card' },
    h('div', { class: 'table-wrap' },
      h('table', { class: 'tbl' },
        h('thead', {}, h('tr', {},
          h('th', { text: 'Image' }),
          h('th', { text: 'Tags' }),
          h('th', { text: 'Version' }),
          h('th', { class: 'num', text: 'Size' }),
          h('th', { text: 'Created' }),
          h('th', { text: 'In use' }),
          h('th', { class: 'actions' }),
        )),
        body,
      )),
  );
  const emptyHost = h('div', {});
  add(ctx.host, [emptyHost, card]);

  function draw() {
    const rows = images.filter((img) => {
      const untagged = !img.RepoTags || !img.RepoTags.length || img.RepoTags[0] === '<none>:<none>';
      if (danglingOnly && !untagged) return false;
      if (!query) return true;
      return [img.reference, ...(img.RepoTags || []), shortID(img.Id)].join(' ').toLowerCase().includes(query);
    });
    clear(emptyHost);
    card.classList.toggle('hidden', rows.length === 0);
    if (!rows.length) {
      add(emptyHost, [emptyState(
        images.length ? 'Nothing matches that filter' : 'No images on this host',
        images.length ? null : 'Pull one from a registry or upload an archive to get started.',
        images.length ? null : h('button', {
          class: 'btn primary',
          onClick: () => openDeployWizard({ onDone: () => load() }),
        }, icon('download'), 'Get an image'),
      )]);
      return;
    }
    clear(body);
    add(body, rows.map(row));
  }

  function row(img) {
    const tags = (img.RepoTags || []).filter((t) => t && t !== '<none>:<none>');
    const untagged = tags.length === 0;
    return h('tr', {},
      h('td', {}, h('div', { class: 'row' },
        icon('layers'),
        h('div', { style: { minWidth: 0 } },
          h('div', { class: 'truncate', style: { fontWeight: '560' }, text: untagged ? 'untagged image' : repoOf(tags[0]) }),
          h('div', { class: 'small faint mono', text: shortID(img.Id) }),
        ),
      )),
      h('td', {}, tags.length
        ? h('div', { class: 'chips' }, tags.slice(0, 4).map((t) => h('span', { class: 'pill', text: tagOf(t) })),
          tags.length > 4 ? h('span', { class: 'small faint', text: `+${tags.length - 4}` }) : null)
        : h('span', { class: 'badge warn', text: 'dangling' })),
      h('td', {}, versionCell(img)),
      h('td', { class: 'num', text: bytes(img.Size) }),
      h('td', { class: 'small faint', text: ago(img.Created) }),
      h('td', {}, img.in_use
        ? h('span', {
          class: `badge ${untagged ? 'warn' : 'ok'}`,
          // Name the containers, which matters most for an untagged image:
          // it is an old version those containers are still running.
          title: `${untagged ? 'Still running this old, untagged version: ' : 'Used by: '}${(img.in_use_by || []).join(', ')}`
            + (untagged ? '. Recreate them to move them to the current version.' : ''),
          text: `${img.in_use} container${img.in_use === 1 ? '' : 's'}`,
        })
        : h('span', { class: 'faint small', text: '—' })),
      h('td', { class: 'actions' }, h('div', { class: 'btn-group' },
        canPull(img) ? h('button', {
          class: `btn sm${img.update && img.update.status === 'update' ? ' primary' : ''}`,
          title: `Pull the newest ${img.reference} from its registry`,
          onClick: () => pullNewest(img),
        }, icon('download'), 'Pull') : null,
        h('button', {
          class: 'btn sm', title: 'Deploy a container from this image', disabled: untagged,
          onClick: () => openDeployWizard({ image: img.reference, onDone: () => ctx.refreshCounts() }),
        }, icon('play'), 'Deploy'),
        h('button', { class: 'btn sm icon', title: 'Details', onClick: () => details(img) }, icon('info')),
        h('button', { class: 'btn sm icon', title: 'Add a tag', onClick: () => retag(img) }, icon('tag')),
        h('button', { class: 'btn sm icon danger', title: 'Remove', onClick: () => remove(img) }, icon('trash')),
      )),
    );
  }

  // Registry images can be pulled again for their newest version; uploads and
  // images built on the host have no registry to pull from.
  function canPull(img) {
    return img.origin === 'registry' || img.origin === 'repository'
      || (img.origin === 'unknown' && (img.RepoDigests || []).length > 0);
  }

  function versionCell(img) {
    const u = img.update;
    if (img.origin === 'uploaded') {
      return h('span', { class: 'badge plain', title: 'Imported from an archive; it is not checked against a registry', text: 'uploaded' });
    }
    if (!u) {
      if (img.origin === 'local' || !(img.RepoDigests || []).length) {
        return h('span', { class: 'faint small', title: 'Built or loaded on this host, with no registry to check', text: 'local' });
      }
      return h('span', { class: 'faint small', title: 'Choose Refresh to check its registry now', text: 'not checked yet' });
    }
    const checked = `Checked ${ago(u.checked_at)}`;
    if (img.origin === 'repository') {
      const detail = u.detail ? `${u.detail.charAt(0).toUpperCase()}${u.detail.slice(1)}.` : '';
      if (u.status === 'update') {
        return h('span', { class: 'badge info', title: `${checked}. ${detail} Choose Pull to install it.` }, icon('download'), 'Newer version available');
      }
      if (u.status === 'current') {
        return h('span', { class: 'badge ok', title: `${checked}. Installed ${u.local_digest ? `version ${u.local_digest} ` : ''}from a GitHub repository. ${detail}`, text: 'Up to date' });
      }
      return h('span', { class: 'faint small', title: `${checked}. ${detail}`, text: 'can’t check' });
    }
    if (u.status === 'update') {
      return h('span', { class: 'badge info', title: `${checked}. The registry has a newer ${u.reference}; choose Pull to fetch it.` },
        icon('download'), 'Newer version available');
    }
    if (u.status === 'current') {
      return h('span', { class: 'badge ok', title: `${checked}. This is the newest ${u.reference} in its registry.`, text: 'Up to date' });
    }
    const local = img.origin === 'local';
    return h('span', {
      class: 'faint small',
      title: `${checked}. ${u.detail ? u.detail.charAt(0).toUpperCase() + u.detail.slice(1) : 'The registry could not be checked'}.`,
      text: local ? 'local' : 'can’t check',
    });
  }

  async function pullNewest(img) {
    const reference = img.reference;
    const result = await modal({
      title: `Pull the newest ${reference}?`,
      subtitle: 'Only the image is downloaded; containers keep running the version they have',
      wide: true,
      hideConfirm: true,
      cancelLabel: 'Close',
      render: ({ body, close }) => {
        const log = h('div', { class: 'progress-log' });
        const bar = h('div', { class: 'bar' }, h('i'));
        const after = h('div', {});
        add(body, [bar, log, after]);
        const layers = new Map();
        stream('/api/images/pull', {
          body: { image: reference },
          onEvent: (event) => {
            if (event.error || event.errorDetail) {
              log.textContent += `\n${event.error || event.errorDetail.message}`;
              return;
            }
            if (event.id && event.progressDetail && event.progressDetail.total) {
              layers.set(event.id, event.progressDetail);
              let done = 0; let total = 0;
              for (const d of layers.values()) { done += d.current || 0; total += d.total || 0; }
              if (total) bar.firstChild.style.width = `${Math.min(100, (done / total) * 100)}%`;
            }
            const line = [event.id, event.status, event.progress].filter(Boolean).join(' ');
            if (line) {
              log.textContent = `${log.textContent}${line}\n`.split('\n').slice(-200).join('\n');
              log.scrollTop = log.scrollHeight;
            }
          },
        }).then(async () => {
          bar.firstChild.style.width = '100%';
          const upToDate = /Image is up to date/i.test(log.textContent);
          const pinned = /is pinned to version/i.test(log.textContent);
          try { await Images.checkUpdates(reference); } catch { /* the list shows the last result */ }
          add(after, [notice(pinned
            ? log.textContent.trim().split('\n').filter((l) => /pinned/.test(l)).pop()
            : upToDate
            ? `${reference} was already the newest version.`
            : `The newest ${reference} is on this host. Containers using it show Update available until you recreate them.`,
          'ok')]);
          load();
        }).catch((err) => add(after, [notice(err.message, 'bad')]));
        return null;
      },
    });
    return result;
  }

  async function details(img) {
    await modal({
      title: img.reference || shortID(img.Id),
      wide: true,
      hideConfirm: true,
      cancelLabel: 'Close',
      render: ({ body: dialog }) => {
        add(dialog, [spinner()]);
        Images.inspect(img.reference && img.reference !== '<none>' ? img.reference : img.Id).then((data) => {
          clear(dialog);
          const d = data.defaults || {};
          const inspect = data.inspect || {};
          const kv = h('dl', { class: 'kv' });
          const rows = [
            ['Digest', h('span', { class: 'mono small' }, shortID(inspect.Id),
              h('button', { class: 'btn sm ghost icon', onClick: () => copyText(inspect.Id, 'Digest copied') }, icon('copy')))],
            ['Platform', `${d.os || '?'}/${d.architecture || '?'}`],
            ['Size', bytes(img.Size)],
            ['Created', inspect.Created ? `${ago(Date.parse(inspect.Created) / 1000)}` : '—'],
            ['Entrypoint', h('code', { class: 'small', text: (d.entrypoint || []).join(' ') || '—' })],
            ['Command', h('code', { class: 'small', text: (d.cmd || []).join(' ') || '—' })],
            ['User', d.user || 'root'],
            ['Working dir', d.working_dir || '/'],
            ['Health check', d.healthcheck ? 'yes' : 'no'],
          ];
          for (const [key, value] of rows) add(kv, [h('dt', { text: key }), h('dd', {}, value)]);

          const env = (d.env || []).map((line) => {
            const eq = line.indexOf('=');
            return eq >= 0 ? [line.slice(0, eq), line.slice(eq + 1)] : [line, ''];
          });
          const ports = d.ports || [];
          const volumes = d.volumes || [];
          const layers = data.history || [];
          const labels = Object.entries(d.labels || {});

          // One panel at a time, so each gets the whole dialog to scroll in.
          const panels = {
            overview: () => [kv, labels.length ? h('div', {},
              h('div', { class: 'section-title', text: 'Labels' }),
              table(['Label', 'Value'], labels.map(([k, v]) => [
                h('td', { class: 'mono small wrap', text: k }),
                h('td', { class: 'small wrap', text: v }),
              ]))) : null],
            env: () => [env.length
              ? table(['Variable', 'Default value'], env.map(([k, v]) => [
                h('td', { class: 'mono small wrap', style: { width: '34%' }, text: k }),
                h('td', { class: 'mono small wrap', text: v || '(empty)' }),
              ]))
              : empty('The image sets no environment variables.')],
            io: () => [
              h('div', { class: 'section-title', text: 'Exposed ports' }),
              ports.length
                ? h('div', { class: 'chips' }, ports.map((p) => h('span', { class: 'pill', text: `${p.container_port}/${p.protocol}` })))
                : empty('The image exposes no ports.'),
              h('div', { class: 'section-title', text: 'Declared volumes' }),
              volumes.length
                ? h('div', { class: 'col', style: { gap: '4px' } }, volumes.map((v) => h('code', { class: 'small', text: v })))
                : empty('The image declares no volumes.'),
              h('p', { class: 'small faint', style: { margin: 0 } },
                'Deploying from this image publishes these ports and gives each declared path a named volume, both of which you can change first.'),
            ],
            layers: () => {
              const hideEmpty = h('input', { type: 'checkbox', checked: true });
              const host = h('div', {});
              const draw = () => {
                clear(host);
                const rows = layers
                  .map((entry, index) => ({ entry, index: layers.length - index }))
                  .filter(({ entry }) => !hideEmpty.checked || entry.Size > 0);
                add(host, [rows.length
                  ? table(['#', 'Size', 'Created by'], rows.map(({ entry, index }) => [
                    h('td', { class: 'small faint num', text: String(index) }),
                    h('td', { class: 'small num nowrap', text: bytes(entry.Size) }),
                    h('td', { class: 'mono small wrap', text: layerCommand(entry.CreatedBy) }),
                  ]))
                  : empty('No layers to show.')]);
              };
              hideEmpty.addEventListener('change', draw);
              draw();
              return [
                h('label', { class: 'check small' }, hideEmpty,
                  `Hide steps that add no data (${layers.filter((l) => !l.Size).length} of ${layers.length})`),
                host,
              ];
            },
          };
          const tabs = [
            ['overview', 'Overview'],
            ['env', `Environment (${env.length})`],
            ['io', `Ports and volumes (${ports.length + volumes.length})`],
            ['layers', `Layers (${layers.length})`],
          ];
          const pane = h('div', { class: 'col', style: { gap: '12px' } });
          const bar = h('div', { class: 'switchbar' });
          const show = (key) => {
            [...bar.children].forEach((b) => b.classList.toggle('on', b.dataset.key === key));
            clear(pane);
            add(pane, panels[key]());
            dialog.scrollTop = 0;
          };
          add(bar, tabs.map(([key, label]) => h('button', { dataset: { key }, text: label, onClick: () => show(key) })));
          add(dialog, [bar, pane]);
          show('overview');
        }).catch((err) => {
          clear(dialog);
          add(dialog, [notice(err.message, 'bad')]);
        });
        return null;
      },
    });
  }

  async function retag(img) {
    const chosen = await modal({
      title: 'Add a tag',
      subtitle: img.reference,
      confirmLabel: 'Add tag',
      render: ({ body: dialog }) => {
        const input = h('input', { type: 'text', class: 'mono', placeholder: 'myapp:stable', spellcheck: false });
        add(dialog, [field('New reference', input, 'Tagging does not copy the image; both names point at the same layers.')]);
        return { submit: () => ({ reference: input.value.trim() }) };
      },
    });
    if (!chosen || !chosen.reference) return;
    try {
      const source = img.reference && img.reference !== '<none>' ? img.reference : img.Id;
      const result = await Images.tag(source, chosen.reference, '');
      toast(`Tagged ${result.reference}`, 'ok');
      load();
    } catch (err) { toastError(err); }
  }

  async function remove(img) {
    const tags = (img.RepoTags || []).filter((t) => t && t !== '<none>:<none>');
    const force = h('label', { class: 'check', style: { marginTop: '10px' } },
      h('input', { type: 'checkbox', checked: false }), 'Force, even if containers still reference it');
    const ok = await confirmDialog({
      title: `Remove ${img.reference || shortID(img.Id)}?`,
      message: tags.length > 1
        ? `This image carries ${tags.length} tags. Removing by one tag only untags it; the layers stay until the last tag is gone.`
        : 'The image layers are deleted if nothing else needs them.',
      confirmLabel: 'Remove image',
      extra: img.in_use ? h('div', {}, notice(`${img.in_use} container(s) use this image.`, 'warn'), force) : force,
    });
    if (!ok) return;
    try {
      const target = tags.length ? tags[0] : img.Id;
      await Images.remove(target, force.querySelector('input').checked);
      toast('Image removed', 'ok');
      load();
      ctx.refreshCounts();
    } catch (err) { toastError(err); }
  }

  async function prune() {
    const unused = h('label', { class: 'check', style: { marginTop: '10px' } },
      h('input', { type: 'checkbox' }), 'Also remove tagged images that no container uses');
    const ok = await confirmDialog({
      title: 'Prune images?',
      message: 'Untagged, dangling images are removed. This cannot be undone, but nothing in use is touched.',
      confirmLabel: 'Prune',
      extra: unused,
    });
    if (!ok) return;
    try {
      const report = await Images.prune(unused.querySelector('input').checked);
      toast(`Reclaimed ${bytes(report.SpaceReclaimed)}`, 'ok');
      load();
      ctx.refreshCounts();
    } catch (err) { toastError(err); }
  }

  function load() {
    Images.list().then((data) => {
      images = data.images || [];
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

function table(headers, rows) {
  return h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
    h('thead', {}, h('tr', {}, headers.map((t) => h('th', { text: t })))),
    h('tbody', {}, rows.map((cells) => h('tr', {}, cells)))));
}

function empty(text) {
  return h('div', { class: 'faint small', text });
}

/** A layer's build step, without the shell wrapper docker records around it. */
function layerCommand(createdBy) {
  return (createdBy || '')
    .replace(/^\/bin\/sh -c #\(nop\)\s*/, '')
    .replace(/^\/bin\/sh -c /, 'RUN ')
    .replace(/\s+# buildkit$/, '')
    .trim() || '—';
}

function repoOf(reference) {
  const slash = reference.lastIndexOf('/');
  const colon = reference.lastIndexOf(':');
  return colon > slash ? reference.slice(0, colon) : reference;
}

function tagOf(reference) {
  const slash = reference.lastIndexOf('/');
  const colon = reference.lastIndexOf(':');
  return colon > slash ? reference.slice(colon + 1) : 'latest';
}
