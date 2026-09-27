// Small DOM and formatting helpers shared by every view.

/** Create an element. Props: class, text, html, style, dataset, on<Event>, or any DOM property. */
export function h(tag, props, ...kids) {
  const node = document.createElement(tag);
  if (props) {
    for (const [key, value] of Object.entries(props)) {
      if (value === null || value === undefined || value === false) continue;
      if (key === 'class') node.className = value;
      else if (key === 'text') node.textContent = value;
      else if (key === 'html') node.innerHTML = value;
      else if (key === 'style' && typeof value === 'object') Object.assign(node.style, value);
      else if (key === 'dataset') Object.assign(node.dataset, value);
      else if (key.startsWith('on') && typeof value === 'function') {
        node.addEventListener(key.slice(2).toLowerCase(), value);
      } else {
        // Some DOM properties (input.list, for one) are read-only, so fall
        // back to the attribute rather than throwing in strict mode.
        try {
          if (key in node) node[key] = value;
          else node.setAttribute(key, value);
        } catch {
          node.setAttribute(key, value);
        }
      }
    }
  }
  add(node, kids);
  return node;
}

/** Append children, flattening arrays and skipping empty values. */
export function add(parent, kids) {
  for (const kid of kids.flat(4)) {
    if (kid === null || kid === undefined || kid === false || kid === '') continue;
    parent.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return parent;
}

export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
  return node;
}

export const frag = (...kids) => add(document.createDocumentFragment(), kids);

// ---------- icons ----------

const ICONS = {
  dashboard: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  box: '<path d="M21 8v8l-9 5-9-5V8l9-5z"/><path d="M3 8l9 5 9-5"/><path d="M12 13v8"/>',
  layers: '<path d="M12 3 3 8l9 5 9-5z"/><path d="M3 13l9 5 9-5"/>',
  database: '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6"/><path d="M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
  network: '<circle cx="12" cy="5" r="2.2"/><circle cx="5" cy="19" r="2.2"/><circle cx="19" cy="19" r="2.2"/><path d="M12 7.2v3.3M6.2 16.9 11 11.5M17.8 16.9 13 11.5"/>',
  settings: '<path d="M4 6h16M4 12h16M4 18h16"/><circle cx="9" cy="6" r="2"/><circle cx="15" cy="12" r="2"/><circle cx="8" cy="18" r="2"/>',
  user: '<circle cx="12" cy="8" r="3.6"/><path d="M4.5 20a7.5 7.5 0 0 1 15 0"/>',
  logout: '<path d="M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3"/><path d="M10 17l-5-5 5-5"/><path d="M5 12h10"/>',
  play: '<path d="M8 5l11 7-11 7z"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="2"/>',
  pause: '<path d="M9 5v14M15 5v14"/>',
  restart: '<path d="M20 12a8 8 0 1 1-2.34-5.66"/><path d="M20 4v4h-4"/>',
  terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M13 15h4"/>',
  logs: '<path d="M6 3h9l4 4v14H6z"/><path d="M9 8h4M9 12h7M9 16h5"/>',
  activity: '<path d="M3 12h4l3-7 4 14 3-7h4"/>',
  trash: '<path d="M4 7h16"/><path d="M9 7V5h6v2"/><path d="M6 7l1 13h10l1-13"/><path d="M10 11v6M14 11v6"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  search: '<circle cx="11" cy="11" r="6.5"/><path d="M16 16l4.5 4.5"/>',
  refresh: '<path d="M4 12a8 8 0 0 1 13.66-5.66L20 8"/><path d="M20 4v4h-4"/><path d="M20 12a8 8 0 0 1-13.66 5.66L4 16"/><path d="M4 20v-4h4"/>',
  pin: '<path d="M12 21s6-6.2 6-11a6 6 0 1 0-12 0c0 4.8 6 11 6 11z"/><circle cx="12" cy="10" r="2.2"/>',
  edit: '<path d="M4 20h4l11-11-4-4L4 16z"/><path d="M14 5l4 4"/>',
  x: '<path d="M6 6l12 12M18 6L6 18"/>',
  check: '<path d="M5 13l4 4 10-10"/>',
  alert: '<path d="M12 4l9 16H3z"/><path d="M12 10v5M12 18h.01"/>',
  info: '<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5M12 8h.01"/>',
  chevron: '<path d="M9 5l7 7-7 7"/>',
  upload: '<path d="M12 16V4"/><path d="M7 9l5-5 5 5"/><path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/>',
  download: '<path d="M12 4v12"/><path d="M7 11l5 5 5-5"/><path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h8"/>',
  key: '<circle cx="8" cy="15" r="3.5"/><path d="M10.5 12.5 19 4M16 4h4v4"/>',
  cpu: '<rect x="6" y="6" width="12" height="12" rx="2"/><rect x="10" y="10" width="4" height="4"/><path d="M9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3"/>',
  disk: '<circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="2.5"/>',
  clock: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3.5 2"/>',
  link: '<path d="M9 15l6-6"/><path d="M11 6l1.5-1.5a4 4 0 0 1 5.66 5.66L16 12"/><path d="M13 18l-1.5 1.5a4 4 0 0 1-5.66-5.66L8 12"/>',
  shield: '<path d="M12 3l8 3v6c0 5-4 8-8 9-4-1-8-4-8-9V6z"/>',
  tag: '<path d="M11 3H5a2 2 0 0 0-2 2v6l10 10 8-8L11 3z"/><circle cx="8" cy="8" r="1.3"/>',
  history: '<path d="M4 12a8 8 0 1 0 8-8 8 8 0 0 0-8 8z"/><path d="M12 8v4l3 2"/>',
  folder: '<path d="M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2.5h8.5A1.5 1.5 0 0 1 21 9v9.5a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18.5z"/>',
  file: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/>',
  code: '<path d="M8 8l-4 4 4 4M16 8l4 4-4 4M13.5 5l-3 14"/>',
  arrowUp: '<path d="M12 19V5M6 11l6-6 6 6"/>',
  users: '<circle cx="9" cy="8" r="3.2"/><path d="M3 19.5a6 6 0 0 1 12 0"/><path d="M15.5 5.2a3.2 3.2 0 0 1 0 6"/><path d="M17.5 13.8a6 6 0 0 1 3.5 5.7"/>',
  help: '<circle cx="12" cy="12" r="8.5"/><path d="M9.6 9.4a2.5 2.5 0 0 1 4.8.9c0 1.7-2.4 2.2-2.4 3.7"/><path d="M12 17h.01"/>',
  external: '<path d="M14 4h6v6"/><path d="M20 4l-9 9"/><path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>',
};

export function icon(name, cls = 'ico') {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.7');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('class', cls);
  svg.innerHTML = ICONS[name] || '';
  return svg;
}

export function logoMark() {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 32 32');
  svg.setAttribute('aria-hidden', 'true');
  svg.innerHTML =
    '<rect width="32" height="32" rx="8" fill="currentColor" opacity=".1"/>' +
    '<path d="M6 12.5 16 7l10 5.5v7L16 25 6 19.5z" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linejoin="round"/>' +
    '<path d="M16 13.2 21 16v3.2L16 22l-5-2.8V16z" fill="currentColor" opacity=".8"/>';
  svg.style.color = 'var(--accent)';
  return svg;
}

// ---------- formatting ----------

export function bytes(n, digits) {
  n = Number(n) || 0;
  if (n < 1024) return `${Math.round(n)} B`;
  const units = ['KB', 'MB', 'GB', 'TB', 'PB'];
  let value = n / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  const places = digits ?? (value < 10 ? 1 : 0);
  return `${value.toFixed(places)} ${units[unit]}`;
}

export const rate = (n) => `${bytes(n)}/s`;

export function pct(n, digits = 1) {
  const v = Number(n) || 0;
  return `${v.toFixed(digits)}%`;
}

export function num(n) {
  return (Number(n) || 0).toLocaleString();
}

/** Relative time from a unix seconds timestamp. */
export function ago(seconds) {
  if (!seconds) return 'never';
  const diff = Math.max(0, Date.now() / 1000 - seconds);
  if (diff < 45) return 'just now';
  const steps = [
    [60, 'second', 1],
    [3600, 'minute', 60],
    [86400, 'hour', 3600],
    [2592000, 'day', 86400],
    [31536000, 'month', 2592000],
    [Infinity, 'year', 31536000],
  ];
  for (const [limit, label, size] of steps) {
    if (diff < limit) {
      const value = Math.floor(diff / size);
      return `${value} ${label}${value === 1 ? '' : 's'} ago`;
    }
  }
  return 'a long time ago';
}

/** Compact duration from a number of seconds. */
export function dur(seconds) {
  seconds = Math.max(0, Math.floor(Number(seconds) || 0));
  const d = Math.floor(seconds / 86400);
  const hr = Math.floor((seconds % 86400) / 3600);
  const min = Math.floor((seconds % 3600) / 60);
  if (d) return `${d}d ${hr}h`;
  if (hr) return `${hr}h ${min}m`;
  if (min) return `${min}m`;
  return `${seconds}s`;
}

export function when(seconds) {
  if (!seconds) return '—';
  return new Date(seconds * 1000).toLocaleString();
}

export const shortID = (id = '') => String(id).replace(/^sha256:/, '').slice(0, 12);

/** State to a badge class. */
export function stateClass(state) {
  switch (state) {
    case 'running': return 'ok';
    case 'paused': return 'warn';
    case 'restarting': return 'warn';
    case 'created': return 'info';
    case 'exited':
    case 'dead': return 'bad';
    default: return 'plain';
  }
}

// ---------- toasts ----------

let toastHost = null;

export function toast(message, kind = 'info', ttl = 5200) {
  toastHost = toastHost || document.getElementById('toasts');
  if (!toastHost) return;
  const node = h('div', { class: `toast ${kind}` },
    icon(kind === 'bad' ? 'alert' : kind === 'ok' ? 'check' : 'info'),
    h('div', { class: 'grow', text: message }),
    h('button', { class: 'x', title: 'Dismiss', onClick: () => node.remove() }, '×'),
  );
  toastHost.append(node);
  if (ttl) setTimeout(() => node.remove(), ttl);
  return node;
}

export const toastError = (err) =>
  toast(err && err.message ? err.message : String(err), 'bad', 8000);

// ---------- modal ----------

/**
 * Show a modal. render(ctx) builds the body and may return an object with
 * { submit } used by the confirm button. Resolves with the submit result, or
 * null when dismissed.
 */
export function modal({ title, subtitle, render, confirmLabel = 'Save', cancelLabel = 'Cancel', danger = false, wide = false, hideConfirm = false, extraActions = null }) {
  return new Promise((resolve) => {
    let settled = false;
    // While locked (an upload in progress, say) nothing closes the dialog:
    // not Escape, not the ✕, not a click outside, and the close button hides.
    let locked = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      scrim.remove();
      document.removeEventListener('keydown', onKey);
      resolve(value);
    };
    const dismiss = () => { if (!locked) finish(null); };
    const onKey = (event) => {
      if (event.key === 'Escape') dismiss();
    };

    const body = h('div', { class: 'body' });
    const confirm = h('button', { class: `btn ${danger ? 'danger' : 'primary'}`, text: confirmLabel });
    const cancelButton = h('button', { class: 'btn ghost', text: cancelLabel, onClick: dismiss });
    const closeX = h('button', { class: 'btn ghost icon', title: 'Close', onClick: dismiss }, icon('x'));
    const footer = h('footer', {},
      cancelButton,
      typeof extraActions === 'function' ? extraActions((value) => finish(value)) : extraActions,
      hideConfirm ? null : confirm,
    );
    const box = h('div', { class: `modal${wide ? ' wide' : ''}`, role: 'dialog', 'aria-modal': 'true' },
      h('header', {}, h('div', { class: 'grow' },
        h('h2', { text: title }),
        subtitle ? h('div', { class: 'small faint', text: subtitle }) : null,
      ), closeX),
      body,
      footer,
    );
    const scrim = h('div', {
      class: 'scrim',
      onClick: (event) => { if (event.target === scrim) dismiss(); },
    }, box);

    const setLocked = (on) => {
      locked = !!on;
      closeX.disabled = locked;
      closeX.title = locked ? 'Unavailable while the upload is in progress' : 'Close';
      cancelButton.classList.toggle('hidden', locked);
      cancelButton.disabled = locked;
    };
    const ctx = { body, close: finish, confirmButton: confirm, setLocked };
    const api = render ? render(ctx) : null;
    confirm.addEventListener('click', async () => {
      if (!api || typeof api.submit !== 'function') return finish(true);
      confirm.disabled = true;
      try {
        const result = await api.submit();
        if (result !== undefined && result !== null) finish(result);
        else finish(true);
      } catch (err) {
        toastError(err);
        confirm.disabled = false;
      }
    });

    document.body.append(scrim);
    document.addEventListener('keydown', onKey);
    const first = body.querySelector('input, select, textarea, button');
    if (first) setTimeout(() => first.focus(), 40);
  });
}

export function confirmDialog({ title, message, confirmLabel = 'Confirm', danger = true, extra }) {
  return modal({
    title,
    confirmLabel,
    danger,
    render: ({ body }) => {
      add(body, [h('p', { class: 'muted', style: { margin: 0 }, text: message }), extra || null]);
      return null;
    },
  });
}

// ---------- misc ----------

export async function copyText(text, label = 'Copied') {
  try {
    await navigator.clipboard.writeText(text);
    toast(label, 'ok', 2200);
  } catch {
    toast('Your browser would not let DocMan use the clipboard', 'warn');
  }
}

export function field(label, control, help) {
  return h('div', { class: 'field' },
    label ? h('label', { text: label }) : null,
    control,
    help ? h('div', { class: 'help', text: help }) : null,
  );
}

export function emptyState(title, detail, action) {
  return h('div', { class: 'empty' }, h('h3', { text: title }), detail ? h('p', { text: detail, style: { margin: 0 } }) : null, action || null);
}

export function notice(text, kind = 'info', extra) {
  return h('div', { class: `notice ${kind}` },
    icon(kind === 'bad' || kind === 'warn' ? 'alert' : 'info'),
    h('div', { class: 'grow' }, h('div', { text }), extra || null),
  );
}

export function spinner() {
  return h('div', { class: 'empty' }, h('div', { class: 'boot-mark' }));
}

/** Debounce a function by ms milliseconds. */
export function debounce(fn, ms = 220) {
  let timer = 0;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), ms);
  };
}

/** Escape a string for use inside a regular expression. */
export const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
