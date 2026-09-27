// Inspect: a container's full Docker configuration as JSON, as a collapsible
// tree or as raw text, with find, copy and download.

import { h, add, clear, icon, modal, notice, spinner, copyText, debounce } from '../ui.js';

/**
 * Open the inspect dialog.
 * @param {string} title shown in the header
 * @param {() => Promise<object>} load fetches the document
 * @param {string} filename for Download
 */
export function showInspect(title, load, filename) {
  return modal({
    title,
    subtitle: 'The complete configuration Docker holds for this container',
    wide: true,
    hideConfirm: true,
    cancelLabel: 'Close',
    render: ({ body }) => {
      add(body, [spinner()]);
      load().then((data) => {
        clear(body);
        mountViewer(body, data, filename);
      }).catch((err) => {
        clear(body);
        add(body, [notice(err.message, 'bad')]);
      });
      return null;
    },
  });
}

function mountViewer(body, data, filename) {
  const text = JSON.stringify(data, null, 2);
  let mode = 'tree';
  let query = '';

  const view = h('div', { class: 'json-view' });
  const count = h('span', { class: 'small faint' });
  const find = h('input', {
    type: 'search', placeholder: 'Find a key or value',
    onInput: debounce((event) => { query = event.target.value.trim().toLowerCase(); draw(); }, 150),
  });
  const bar = h('div', { class: 'switchbar' },
    ...[['tree', 'Tree'], ['raw', 'Raw JSON']].map(([key, label]) => h('button', {
      dataset: { key }, class: key === mode ? 'on' : '', text: label,
      onClick: () => {
        mode = key;
        [...bar.children].forEach((b) => b.classList.toggle('on', b.dataset.key === mode));
        draw();
      },
    })));
  const expand = (open) => view.querySelectorAll('details').forEach((d) => { d.open = open; });

  add(body, [
    h('div', { class: 'row wrap' },
      bar,
      h('div', { class: 'search grow', style: { maxWidth: '320px' } }, icon('search'), find),
      count,
      h('div', { class: 'grow' }),
      h('button', { class: 'btn sm ghost', onClick: () => expand(true) }, 'Expand all'),
      h('button', { class: 'btn sm ghost', onClick: () => expand(false) }, 'Collapse all'),
      h('button', { class: 'btn sm', onClick: () => copyText(text, 'JSON copied') }, icon('copy'), 'Copy'),
      h('button', {
        class: 'btn sm',
        onClick: () => {
          const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
          const link = h('a', { href: url, download: filename });
          document.body.append(link);
          link.click();
          link.remove();
          setTimeout(() => URL.revokeObjectURL(url), 1000);
        },
      }, icon('download'), 'Download'),
    ),
    view,
  ]);

  function draw() {
    clear(view);
    let hits = 0;
    const mark = (s) => {
      if (!query) return [document.createTextNode(s)];
      const out = [];
      const lower = s.toLowerCase();
      let from = 0;
      let at = lower.indexOf(query);
      while (at >= 0) {
        hits++;
        out.push(document.createTextNode(s.slice(from, at)), h('mark', { text: s.slice(at, at + query.length) }));
        from = at + query.length;
        at = lower.indexOf(query, from);
      }
      out.push(document.createTextNode(s.slice(from)));
      return out;
    };

    if (mode === 'raw') {
      add(view, [h('pre', { class: 'json-raw' }, mark(text))]);
    } else {
      add(view, [node(null, data, 0)]);
    }
    count.textContent = query ? `${hits} match${hits === 1 ? '' : 'es'}` : '';
    const first = view.querySelector('mark');
    if (first) first.scrollIntoView({ block: 'center' });

    function contains(value, key) {
      if (!query) return false;
      if (key !== null && String(key).toLowerCase().includes(query)) return true;
      if (value && typeof value === 'object') {
        return Object.entries(value).some(([k, v]) => contains(v, Array.isArray(value) ? null : k));
      }
      return JSON.stringify(value).toLowerCase().includes(query);
    }

    function keyLabel(key) {
      if (key === null) return [];
      return [h('span', { class: 'jk' }, mark(typeof key === 'number' ? String(key) : JSON.stringify(key))), ': '];
    }

    function node(key, value, depth) {
      if (value !== null && typeof value === 'object') {
        const isArray = Array.isArray(value);
        const entries = Object.entries(value);
        const size = entries.length;
        const summary = h('summary', {}, ...keyLabel(key),
          isArray ? '[' : '{',
          h('span', { class: 'jmeta', text: ` ${size} ${isArray ? (size === 1 ? 'item' : 'items') : (size === 1 ? 'key' : 'keys')} ` }),
          isArray ? ']' : '}');
        const details = h('details', { open: query ? contains(value, key) : depth < 1 }, summary);
        if (!size) {
          details.open = false;
          return h('div', { class: 'jrow' }, ...keyLabel(key), h('span', { class: 'jmeta', text: isArray ? '[]' : '{}' }));
        }
        // Children are built when first opened, so a large document stays quick.
        let built = false;
        const build = () => {
          if (built) return;
          built = true;
          add(details, entries.map(([k, v]) => node(isArray ? Number(k) : k, v, depth + 1)));
        };
        if (details.open) build();
        details.addEventListener('toggle', () => { if (details.open) build(); });
        return details;
      }
      const cls = value === null ? 'jnull' : typeof value === 'string' ? 'js' : typeof value === 'number' ? 'jn' : 'jb';
      return h('div', { class: 'jrow' }, ...keyLabel(key), h('span', { class: cls }, mark(JSON.stringify(value))));
    }
  }

  draw();
}
