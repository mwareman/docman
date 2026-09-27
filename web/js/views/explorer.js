// Volume explorer: browse a volume like a file manager, and download, upload,
// create, rename, delete and edit what is in it.

import {
  h, add, clear, icon, bytes, ago, when, toast, toastError, confirmDialog, modal,
  field, notice, spinner, debounce, emptyState,
} from '../ui.js';
import { VolumeFiles } from '../api.js';
import { uploadInPieces } from '../upload.js';

const EDIT_LIMIT = 1024 * 1024;

export function explorerView(ctx) {
  const volume = ctx.params.name;
  let dir = normalize(ctx.params.path || '/');
  let listing = null;
  let query = '';

  ctx.setCrumbs(
    h('a', { href: '#/volumes', class: 'muted', text: 'Volumes' }),
    h('h1', { class: 'mono', text: volume }),
  );

  const fileInput = h('input', { type: 'file', multiple: true, class: 'hidden' });
  fileInput.addEventListener('change', () => { uploadFiles([...fileInput.files]); fileInput.value = ''; });
  const search = h('input', {
    type: 'search', placeholder: 'Filter this folder',
    onInput: debounce((event) => { query = event.target.value.trim().toLowerCase(); draw(); }, 120),
  });

  ctx.setActions(
    h('div', { class: 'search' }, icon('search'), search),
    h('button', { class: 'btn', title: 'Reload this folder', onClick: () => load() }, icon('refresh')),
    h('button', { class: 'btn', onClick: () => newFolder() }, icon('folder'), 'New folder'),
    h('button', { class: 'btn', onClick: () => newFile() }, icon('file'), 'New file'),
    h('button', { class: 'btn', title: 'Download this folder as a .tar archive', onClick: () => download(dir, 'dir') }, icon('download'), 'Download folder'),
    h('button', { class: 'btn primary', onClick: () => fileInput.click() }, icon('upload'), 'Upload'),
    fileInput,
  );

  const pathBar = h('nav', { class: 'explorer-path', 'aria-label': 'Folder' });
  const uploads = h('div', { class: 'col', style: { gap: '6px' } });
  const body = h('tbody');
  const table = h('div', { class: 'card explorer-drop' },
    h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
      h('thead', {}, h('tr', {},
        h('th', { text: 'Name' }), h('th', { class: 'num', text: 'Size' }),
        h('th', { text: 'Modified' }), h('th', { text: 'Permissions' }), h('th', { class: 'actions' }),
      )),
      body)),
    h('div', { class: 'explorer-drop-hint', text: 'Drop files to upload them here' }),
  );
  const status = h('div', {});
  add(ctx.host, [
    h('div', { class: 'row wrap', style: { marginBottom: '12px' } }, pathBar),
    uploads, status, table,
  ]);

  // Drag files anywhere onto the list to upload them into the open folder.
  let dragDepth = 0;
  table.addEventListener('dragenter', (e) => { if (hasFiles(e)) { e.preventDefault(); dragDepth++; table.classList.add('over'); } });
  table.addEventListener('dragover', (e) => { if (hasFiles(e)) e.preventDefault(); });
  table.addEventListener('dragleave', () => { dragDepth = Math.max(0, dragDepth - 1); if (!dragDepth) table.classList.remove('over'); });
  table.addEventListener('drop', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    dragDepth = 0;
    table.classList.remove('over');
    uploadFiles([...e.dataTransfer.files]);
  });

  function go(path) {
    dir = normalize(path);
    const hash = `#/volumes/${encodeURIComponent(volume)}?path=${encodeURIComponent(dir)}`;
    history.replaceState(null, '', hash);
    search.value = '';
    query = '';
    load();
  }

  function drawPath() {
    clear(pathBar);
    const parts = dir.split('/').filter(Boolean);
    const crumbs = [h('a', { href: '#', class: 'crumb', onClick: (e) => { e.preventDefault(); go('/'); } }, icon('database'), volume)];
    parts.forEach((part, i) => {
      const target = `/${parts.slice(0, i + 1).join('/')}`;
      crumbs.push(h('span', { class: 'faint', text: '/' }));
      crumbs.push(i === parts.length - 1
        ? h('span', { class: 'crumb on', text: part })
        : h('a', { href: '#', class: 'crumb', onClick: (e) => { e.preventDefault(); go(target); } }, part));
    });
    add(pathBar, [
      h('button', { class: 'btn sm ghost icon', title: 'Up one folder', disabled: dir === '/', onClick: () => go(parent(dir)) }, icon('arrowUp')),
      ...crumbs,
    ]);
  }

  function draw() {
    clear(body);
    clear(status);
    if (!listing) return;
    if (listing.docman_data) {
      add(status, [notice('This is DocMan’s own data volume: its database and TLS keys. Changing or deleting files here can break DocMan or lock everyone out.', 'warn')]);
    }
    if (listing.truncated) add(status, [notice('This folder holds more than 5,000 items; only the first 5,000 are shown.', 'warn')]);
    const rows = listing.entries.filter((e) => !query || e.name.toLowerCase().includes(query));
    if (!rows.length) {
      add(body, [h('tr', {}, h('td', { colSpan: 5 }, emptyState(
        listing.entries.length ? 'Nothing matches that filter' : 'This folder is empty',
        listing.entries.length ? null : 'Upload files or drop them here, or create a folder.',
      )))]);
      return;
    }
    add(body, rows.map(row));
  }

  function row(entry) {
    const path = join(dir, entry.name);
    const isDir = entry.type === 'dir';
    const nameCell = isDir
      ? h('a', { href: '#', class: 'explorer-name', onClick: (e) => { e.preventDefault(); go(path); } }, icon('folder'), entry.name)
      : h('span', { class: 'explorer-name' }, icon(entry.type === 'link' ? 'link' : 'file'), entry.name,
        entry.type === 'link' ? h('span', { class: 'faint small', text: ` → ${entry.target}` }) : null);
    const canEdit = entry.type === 'file' && entry.size <= EDIT_LIMIT;
    return h('tr', { class: isDir ? 'click' : '', onDblclick: () => (isDir ? go(path) : canEdit ? edit(path) : null) },
      h('td', {}, nameCell),
      h('td', { class: 'num small', text: isDir ? '—' : bytes(entry.size) }),
      h('td', { class: 'small faint nowrap', title: when(entry.mtime), text: ago(entry.mtime) }),
      h('td', { class: 'mono small faint nowrap', title: `owner ${entry.uid}, group ${entry.gid}`, text: `${entry.mode}  ${entry.uid}:${entry.gid}` }),
      h('td', { class: 'actions' }, h('div', { class: 'btn-group' },
        entry.type === 'file' ? h('button', {
          class: 'btn sm icon', title: canEdit ? 'Edit' : 'Too large to edit here (over 1 MB); download it instead', disabled: !canEdit,
          onClick: () => edit(path),
        }, icon('edit')) : null,
        entry.type !== 'other' ? h('button', {
          class: 'btn sm icon', title: isDir ? 'Download folder as .tar' : 'Download',
          onClick: () => download(path, isDir ? 'dir' : 'file'),
        }, icon('download')) : null,
        h('button', { class: 'btn sm icon', title: 'Rename or move', onClick: () => rename(entry, path) }, icon('tag')),
        h('button', { class: 'btn sm icon danger', title: 'Delete', onClick: () => removeEntry(entry, path) }, icon('trash')),
      )),
    );
  }

  async function load() {
    drawPath();
    clear(body);
    add(body, [h('tr', {}, h('td', { colSpan: 5 }, spinner()))]);
    try {
      listing = await VolumeFiles.list(volume, dir);
      draw();
    } catch (err) {
      listing = null;
      clear(body);
      add(body, [h('tr', {}, h('td', { colSpan: 5 }, notice(err.message, 'bad')))]);
    }
  }

  function download(path, kind) {
    const link = h('a', { href: VolumeFiles.downloadURL(volume, path, kind), download: '' });
    document.body.append(link);
    link.click();
    link.remove();
  }

  async function uploadFiles(files) {
    if (!files.length) return;
    for (const file of files) {
      const bar = h('div', { class: 'bar' }, h('i'));
      const label = h('div', { class: 'small grow', text: `Uploading ${file.name} · ${bytes(file.size)}` });
      const controller = new AbortController();
      const cancel = h('button', { class: 'btn sm ghost', onClick: () => controller.abort() }, 'Cancel');
      const item = h('div', { class: 'card', style: { padding: '8px 12px' } }, h('div', { class: 'row' }, label, cancel), bar);
      add(uploads, [item]);
      // Sent in pieces sized to get past any proxy in front of DocMan.
      const hooks = {
        signal: controller.signal,
        onProgress: ({ sent, total }) => {
          bar.firstChild.style.width = `${((sent / total) * 100).toFixed(1)}%`;
          label.textContent = `Uploading ${file.name} · ${bytes(sent)} of ${bytes(total)}`;
        },
        onImport: () => { cancel.remove(); label.textContent = `Saving ${file.name} into the volume…`; },
      };
      const target = (overwrite) => ({ kind: 'file', volume, path: dir, overwrite });
      try {
        try {
          await uploadInPieces(file, target(false), hooks);
        } catch (err) {
          if (!(err.payload && err.payload.exists)) throw err;
          const ok = await confirmDialog({
            title: `Replace ${file.name}?`,
            message: `A file called ${file.name} is already in this folder. Replacing it cannot be undone.`,
            confirmLabel: 'Replace',
          });
          if (!ok) { item.remove(); continue; }
          await uploadInPieces(file, target(true), hooks);
        }
        item.remove();
        toast(`Uploaded ${file.name}`, 'ok', 2500);
      } catch (err) {
        cancel.remove();
        label.textContent = err.name === 'AbortError' ? `${file.name}: cancelled` : `${file.name}: ${err.message}`;
        label.style.color = err.name === 'AbortError' ? '' : 'var(--bad)';
        setTimeout(() => item.remove(), 8000);
      }
    }
    load();
  }

  async function newFolder() {
    const chosen = await modal({
      title: 'New folder', subtitle: `In ${dir}`, confirmLabel: 'Create folder',
      render: ({ body: dialog }) => {
        const input = h('input', { type: 'text', spellcheck: false });
        add(dialog, [field('Name', input)]);
        setTimeout(() => input.focus(), 50);
        return { submit: () => ({ name: input.value.trim() }) };
      },
    });
    if (!chosen || !chosen.name) return;
    try {
      await VolumeFiles.mkdir(volume, join(dir, chosen.name));
      load();
    } catch (err) { toastError(err); }
  }

  async function newFile() {
    const chosen = await modal({
      title: 'New file', subtitle: `In ${dir}`, confirmLabel: 'Create and edit',
      render: ({ body: dialog }) => {
        const input = h('input', { type: 'text', spellcheck: false, placeholder: 'config.yml' });
        add(dialog, [field('Name', input)]);
        setTimeout(() => input.focus(), 50);
        return { submit: () => ({ name: input.value.trim() }) };
      },
    });
    if (!chosen || !chosen.name) return;
    if (chosen.name.includes('/')) { toast('A file name cannot contain /', 'bad'); return; }
    try {
      await VolumeFiles.save(volume, join(dir, chosen.name), '', 0, true);
      await load();
      edit(join(dir, chosen.name));
    } catch (err) { toastError(err); }
  }

  async function rename(entry, path) {
    const chosen = await modal({
      title: `Rename ${entry.name}`, confirmLabel: 'Rename',
      render: ({ body: dialog }) => {
        const input = h('input', { type: 'text', class: 'mono', value: path, spellcheck: false });
        add(dialog, [field('New path', input, 'Change the name, or the folders before it to move it within this volume.')]);
        setTimeout(() => { input.focus(); input.setSelectionRange(path.length - entry.name.length, path.length); }, 50);
        return { submit: () => ({ to: input.value.trim() }) };
      },
    });
    if (!chosen || !chosen.to || normalize(chosen.to) === path) return;
    try {
      await VolumeFiles.rename(volume, path, normalize(chosen.to));
      load();
    } catch (err) { toastError(err); }
  }

  async function removeEntry(entry, path) {
    const ok = await confirmDialog({
      title: `Delete ${entry.name}?`,
      message: entry.type === 'dir'
        ? 'The folder and everything in it are deleted permanently.'
        : 'The file is deleted permanently.',
      confirmLabel: 'Delete',
    });
    if (!ok) return;
    try {
      await VolumeFiles.remove(volume, path);
      toast(`${entry.name} deleted`, 'ok', 2500);
      load();
    } catch (err) { toastError(err); }
  }

  function edit(path) {
    openEditor(volume, path, () => load());
  }

  // The helper container behind the explorer runs only while it is open:
  // leaving the page, or closing the tab, stops it.
  const release = () => VolumeFiles.close(volume);
  window.addEventListener('pagehide', release);

  load();
  return {
    dispose() {
      window.removeEventListener('pagehide', release);
      release();
    },
  };
}

// ---------- editor ----------

/**
 * A text editor for one file. Unlike the ordinary dialogs it asks before
 * closing with unsaved changes, so Escape never throws work away.
 */
function openEditor(volume, path, onSaved) {
  let mtime = 0;
  let saved = '';
  let isJSON = false;
  let trailingNewline = false;
  const area = h('textarea', { class: 'editor-area mono', spellcheck: false, disabled: true });
  const info = h('span', { class: 'small faint' });
  const state = h('span', { class: 'small' });
  // JSON tools, shown only for JSON files.
  const jsonState = h('span', { class: 'small' });
  const compact = h('input', { type: 'checkbox' });
  const compactLabel = h('label', { class: 'check small hidden', title: 'The file was on one line; it is shown laid out so it is easier to read and edit' },
    compact, 'Save on one line, as it was');
  const formatButton = h('button', {
    class: 'btn sm ghost hidden', title: 'Lay the JSON out over several lines',
    onClick: () => {
      if (jsonProblem(area.value)) return;
      area.value = formatJSON(area.value);
      refreshState();
    },
  }, icon('code'), 'Format');
  const jsonTools = h('div', { class: 'row', style: { gap: '10px' } }, jsonState, formatButton, compactLabel);
  const saveButton = h('button', { class: 'btn primary', disabled: true }, icon('check'), 'Save');
  const closeButton = h('button', { class: 'btn ghost', text: 'Close' });
  const box = h('div', { class: 'modal wide editor', role: 'dialog', 'aria-modal': 'true' },
    h('header', {},
      h('div', { class: 'grow', style: { minWidth: 0 } },
        h('h2', { class: 'mono truncate', text: path }),
        info),
      h('button', { class: 'btn ghost icon', title: 'Close', onClick: () => close() }, icon('x'))),
    h('div', { class: 'body' }, area),
    h('footer', {}, jsonTools, state, h('div', { class: 'grow' }), closeButton, saveButton),
  );
  const scrim = h('div', { class: 'scrim' }, box);
  document.body.append(scrim);

  const dirty = () => area.value !== saved;
  const refreshState = () => {
    state.textContent = dirty() ? 'Unsaved changes' : '';
    state.style.color = dirty() ? 'var(--warn)' : '';
    saveButton.disabled = !dirty();
    if (isJSON) {
      const problem = jsonProblem(area.value);
      jsonState.textContent = problem ? `Not valid JSON: ${problem}` : 'Valid JSON';
      jsonState.style.color = problem ? 'var(--bad)' : 'var(--ok)';
      jsonState.title = problem;
      formatButton.disabled = !!problem;
    }
  };

  async function close() {
    if (dirty()) {
      const ok = await confirmDialog({
        title: 'Discard your changes?', message: 'The file has unsaved changes, which will be lost.', confirmLabel: 'Discard',
      });
      if (!ok) return;
    }
    document.removeEventListener('keydown', onKey, true);
    scrim.remove();
  }

  async function save() {
    if (!dirty()) return;
    let content = area.value;
    if (isJSON) {
      const problem = jsonProblem(content);
      if (problem) {
        const ok = await confirmDialog({
          title: 'Save JSON that is not valid?',
          message: `${problem}. An application reading this file will probably fail to load it.`,
          confirmLabel: 'Save anyway',
        });
        if (!ok) return;
      } else if (!compactLabel.classList.contains('hidden') && compact.checked) {
        // Back on one line, the way the application wrote it.
        content = compactJSON(content) + (trailingNewline ? '\n' : '');
      } else if (!compactLabel.classList.contains('hidden') && trailingNewline && !content.endsWith('\n')) {
        // Laid out for editing: keep the final line break the file had.
        content += '\n';
      }
    }
    saveButton.disabled = true;
    try {
      const result = await VolumeFiles.save(volume, path, content, mtime, false);
      mtime = result.mtime;
      saved = area.value;
      info.textContent = `${bytes(result.size)} · saved just now`;
      toast(`Saved ${path}`, 'ok', 2500);
      refreshState();
      if (onSaved) onSaved();
    } catch (err) {
      toastError(err);
      refreshState();
    }
  }

  function onKey(event) {
    if (!document.body.contains(scrim)) return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
      event.preventDefault();
      save();
    } else if (event.key === 'Escape' && !document.querySelector('.scrim .modal:not(.editor)')) {
      event.preventDefault();
      event.stopPropagation();
      close();
    } else if (event.key === 'Tab' && event.target === area) {
      // Tab indents instead of leaving the editor.
      event.preventDefault();
      area.setRangeText('\t', area.selectionStart, area.selectionEnd, 'end');
      refreshState();
    }
  }
  document.addEventListener('keydown', onKey, true);
  area.addEventListener('input', refreshState);
  saveButton.addEventListener('click', save);
  closeButton.addEventListener('click', () => close());

  VolumeFiles.content(volume, path).then((data) => {
    mtime = data.mtime;
    let text = data.content;
    isJSON = looksLikeJSON(path, text);
    trailingNewline = text.endsWith('\n');
    if (isJSON) {
      formatButton.classList.remove('hidden');
      // JSON written on a single line is shown laid out, and by default saved
      // back on one line so the file keeps the shape its application gave it.
      const oneLine = text.trim().length > 0 && !text.trim().includes('\n') && !jsonProblem(text);
      if (oneLine && /[{[]/.test(text)) {
        text = formatJSON(text.trim());
        compactLabel.classList.remove('hidden');
        compact.checked = true;
      }
    }
    saved = text;
    area.value = text;
    area.disabled = false;
    info.textContent = `${bytes(data.size)} · mode ${data.mode} · owner ${data.uid}:${data.gid} · modified ${ago(data.mtime)}`
      + (compactLabel.classList.contains('hidden') ? '' : ' · shown laid out; it is on one line in the file');
    area.focus();
    refreshState();
  }).catch((err) => {
    area.replaceWith(notice(err.message + (err.hint ? ` — ${err.hint}` : ''), 'bad'));
  });
}

// ---------- JSON layout ----------
//
// These only move whitespace: every string, number and key is copied through
// exactly as written, so formatting never rounds a large number, reorders
// keys or drops a duplicate, as parsing and re-serialising would.

/** Lay JSON out over several lines, indented by two spaces. */
export function formatJSON(text) {
  let out = '';
  let depth = 0;
  let inString = false;
  let escaped = false;
  const newline = () => `\n${'  '.repeat(depth)}`;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inString) {
      out += c;
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === '"') inString = false;
      continue;
    }
    if (c === ' ' || c === '\t' || c === '\n' || c === '\r') continue;
    if (c === '"') { inString = true; out += c; continue; }
    if (c === '{' || c === '[') {
      // Keep an empty object or array on one line.
      const next = nextSignificant(text, i + 1);
      if (text[next] === (c === '{' ? '}' : ']')) { out += c + text[next]; i = next; continue; }
      depth++;
      out += c + newline();
    } else if (c === '}' || c === ']') {
      depth = Math.max(0, depth - 1);
      out += newline() + c;
    } else if (c === ',') {
      out += `,${newline()}`;
    } else if (c === ':') {
      out += ': ';
    } else {
      out += c;
    }
  }
  return out;
}

/** Put JSON back on one line, with no spaces outside strings. */
export function compactJSON(text) {
  let out = '';
  let inString = false;
  let escaped = false;
  for (const c of text) {
    if (inString) {
      out += c;
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === '"') inString = false;
    } else if (c === '"') {
      inString = true;
      out += c;
    } else if (c !== ' ' && c !== '\t' && c !== '\n' && c !== '\r') {
      out += c;
    }
  }
  return out;
}

function nextSignificant(text, from) {
  let i = from;
  while (i < text.length && ' \t\n\r'.includes(text[i])) i++;
  return i;
}

/** Why text is not valid JSON, or '' when it is. */
function jsonProblem(text) {
  if (!text.trim()) return '';
  try { JSON.parse(text); return ''; } catch (err) { return err.message; }
}

/**
 * A file to treat as JSON: named .json (or a JSON format such as
 * .webmanifest), or valid JSON in a file with no extension. JSON-with-comments
 * formats (.jsonc, .json5) are left alone, since comments would not survive.
 */
function looksLikeJSON(path, content) {
  if (/\.(json|webmanifest|har)$/i.test(path)) return true;
  const start = content.trimStart()[0];
  return !/\.[^/]+$/.test(path) && (start === '{' || start === '[') && !jsonProblem(content);
}

// ---------- paths ----------

function normalize(path) {
  const parts = [];
  for (const part of String(path || '/').replace(/\\/g, '/').split('/')) {
    if (!part || part === '.') continue;
    if (part === '..') parts.pop();
    else parts.push(part);
  }
  return `/${parts.join('/')}`;
}

function join(dir, name) {
  return normalize(`${dir}/${name}`);
}

function parent(path) {
  return normalize(`${path}/..`);
}

function hasFiles(event) {
  return [...(event.dataTransfer?.types || [])].includes('Files');
}
