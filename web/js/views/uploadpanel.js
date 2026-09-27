// Uploading a docker save archive: sent in pieces sized to whatever proxy or
// WAF sits in front of DocMan, then imported by Docker on the server.

import { h, add, clear, icon, bytes, toast, notice } from '../ui.js';
import { uploadInPieces } from '../upload.js';

/**
 * The upload panel. While an upload is under way the dialog is locked, so the
 * only way out is Cancel; once the upload part is complete Cancel goes, and
 * when the import is done the dialog unlocks again.
 * @param {(first: string, loaded: string[]) => void} next called once imported
 * @param {{setLocked?: (on: boolean) => void, moveOn?: boolean, doneText?: (tags: string[]) => string}} options
 */
export function uploadPanel(next, { setLocked = () => {}, moveOn = false, doneText } = {}) {
  const input = h('input', { type: 'file', accept: '.tar,.tar.gz,.tgz,application/x-tar,application/gzip', class: 'hidden' });
  const drop = h('div', { class: 'drop' },
    icon('upload', 'ico'),
    h('div', { style: { marginTop: '8px', fontWeight: '560' } }, 'Drop a docker image archive here'),
    h('div', { class: 'small faint' }, 'or click to choose a file — the output of ',
      h('code', { class: 'pill', text: 'docker save' })),
  );
  const chosen = h('div', { class: 'small faint' });
  const start = h('button', { class: 'btn primary', disabled: true }, icon('upload'), 'Upload and import');
  const picker = h('div', { class: 'col' }, drop, input, chosen, start);

  // Progress: two stages, uploading then importing.
  const upBar = h('div', { class: 'bar' }, h('i'));
  const upText = h('div', { class: 'small' });
  const upDetail = h('div', { class: 'small faint' });
  const importBar = h('div', { class: 'bar' }, h('i'));
  const importText = h('div', { class: 'small faint' });
  const cancel = h('button', { class: 'btn danger' }, icon('x'), 'Cancel upload');
  const stages = h('div', { class: 'col upload-stages hidden' },
    h('div', { class: 'upload-stage' },
      h('div', { class: 'row' }, h('strong', { text: '1. Uploading' }), h('div', { class: 'grow' }), upText),
      upBar, upDetail),
    h('div', { class: 'upload-stage' },
      h('div', { class: 'row' }, h('strong', { text: '2. Importing into Docker' }), h('div', { class: 'grow' }), importText),
      importBar),
    h('div', {}, cancel),
  );
  const outcome = h('div', {});
  let file = null;
  let controller = null;

  const pick = (chosenFile) => {
    file = chosenFile;
    chosen.textContent = file ? `${file.name} · ${bytes(file.size)}` : '';
    start.disabled = !file;
  };
  drop.addEventListener('click', () => input.click());
  input.addEventListener('change', () => pick(input.files[0] || null));
  drop.addEventListener('dragover', (event) => { event.preventDefault(); drop.classList.add('over'); });
  drop.addEventListener('dragleave', () => drop.classList.remove('over'));
  drop.addEventListener('drop', (event) => {
    event.preventDefault();
    drop.classList.remove('over');
    pick(event.dataTransfer.files[0] || null);
  });

  // Leaving the page would abandon the upload, so the browser asks first.
  const guard = (event) => { event.preventDefault(); event.returnValue = ''; };
  const busy = (on) => {
    setLocked(on);
    if (on) window.addEventListener('beforeunload', guard);
    else window.removeEventListener('beforeunload', guard);
  };
  const setBar = (bar, share) => {
    bar.firstChild.style.width = `${Math.max(0, Math.min(100, share * 100)).toFixed(1)}%`;
  };

  const run = async () => {
    if (!file) return;
    controller = new AbortController();
    clear(outcome);
    picker.classList.add('hidden');
    stages.classList.remove('hidden');
    cancel.classList.remove('hidden');
    cancel.disabled = false;
    setBar(upBar, 0);
    setBar(importBar, 0);
    upText.textContent = '0%';
    upDetail.textContent = `Starting · ${bytes(file.size)}`;
    importText.textContent = 'After the upload';
    busy(true);
    const began = Date.now();
    try {
      const job = await uploadInPieces(file, { kind: 'image' }, {
        signal: controller.signal,
        onProgress: ({ sent, total, pieceSize, rate }) => {
          setBar(upBar, sent / total);
          const left = rate > 0 ? (total - sent) / rate : 0;
          upText.textContent = `${Math.floor((sent / total) * 100)}%`;
          upDetail.textContent = `${bytes(sent)} of ${bytes(total)} · ${bytes(rate)}/s`
            + (sent < total && left > 1 ? ` · about ${duration(left)} left` : '')
            + ` · pieces of ${bytes(pieceSize)}`;
        },
        onImport: (step) => {
          // Once the import starts the upload is complete: nothing is left to cancel.
          cancel.classList.add('hidden');
          setBar(upBar, 1);
          upText.textContent = 'Complete';
          upDetail.textContent = `All ${bytes(file.size)} received and checked`;
          importText.textContent = step;
          const pct = /(\d+)%/.exec(step || '');
          if (pct) setBar(importBar, Number(pct[1]) / 100);
        },
      });
      const loaded = job.result || [];
      stages.classList.add('hidden');
      const tags = loaded.filter((ref) => !ref.startsWith('sha256:'));
      add(outcome, [h('div', { class: 'upload-done', role: 'status' },
        h('div', { class: 'upload-done-icon' }, icon('check')),
        h('div', {},
          h('div', { class: 'upload-done-title', text: 'Upload complete' }),
          h('div', { text: tags.length ? `Imported ${tags.join(', ')}` : 'Imported an untagged image' }),
          h('div', { class: 'small faint', text: `${bytes(file.size)} in ${duration((Date.now() - began) / 1000)}` }),
          doneText ? h('div', { class: 'small', style: { marginTop: '6px' }, text: doneText(tags) }) : null,
          moveOn ? h('div', { class: 'small faint', style: { marginTop: '6px' }, text: 'Opening the new container form…' }) : null,
        ))]);
      busy(false);
      toast(`Imported ${loaded.join(', ') || file.name}`, 'ok');
      if (moveOn) setTimeout(() => next(loaded[0], loaded), 1500);
      else next(loaded[0], loaded);
    } catch (err) {
      busy(false);
      stages.classList.add('hidden');
      picker.classList.remove('hidden');
      if (err.name === 'AbortError') {
        add(outcome, [notice('Upload cancelled. Nothing was imported.', 'info')]);
      } else {
        add(outcome, [notice(`${err.message}${err.hint ? ` — ${err.hint}` : ''}`, 'bad')]);
      }
    } finally {
      controller = null;
    }
  };

  start.addEventListener('click', run);
  cancel.addEventListener('click', () => {
    if (!controller) return;
    cancel.disabled = true;
    controller.abort();
  });

  return h('div', { class: 'col' },
    picker,
    stages,
    outcome,
    notice('Large archives are sent in pieces sized automatically to pass any proxy or firewall in front of DocMan, then imported on the server. Keep this window open until the upload stage is complete.', 'info'),
  );
}

export function duration(seconds) {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s} s`;
  if (s < 3600) return `${Math.floor(s / 60)} min ${s % 60} s`;
  return `${Math.floor(s / 3600)} h ${Math.floor((s % 3600) / 60)} min`;
}
