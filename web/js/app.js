// DocMan shell: boot, authentication gate, sidebar, hash router.

import { h, add, clear, icon, logoMark, toast, toastError, confirmDialog } from './ui.js';
import { State, System, Containers, Images, Volumes } from './api.js';
import { renderGate } from './views/gate.js';
import { dashboardView } from './views/dashboard.js';
import { containersView } from './views/containers.js';
import { containerView } from './views/container.js';
import { imagesView } from './views/images.js';
import { volumesView } from './views/volumes.js';
import { explorerView } from './views/explorer.js';
import { resumeJobs } from './views/jobs.js';
import { networkView } from './views/network.js';
import { settingsView } from './views/settings.js';

const app = document.getElementById('app');

const shellState = {
  status: null,
  system: null,
  settings: null,
  counts: { containers: 0, running: 0, images: 0, volumes: 0 },
};

let activeView = null;
let contentHost = null;
let crumbHost = null;
let actionHost = null;
let navHost = null;

// ---------- theme ----------

function applyTheme(theme) {
  const root = document.documentElement;
  if (theme === 'light' || theme === 'dark') root.setAttribute('data-theme', theme);
  else root.removeAttribute('data-theme');
  // Remembered in this browser so the help pages, which load no settings,
  // open in the same theme.
  try { localStorage.setItem('docman.theme', theme || 'system'); } catch { /* ignore */ }
}

// ---------- boot ----------

export async function boot() {
  try {
    const status = await State.fetch();
    shellState.status = status;
    if (!status.setup_complete || !status.authenticated) {
      teardown();
      renderGate(app, status, boot);
      return;
    }
    await renderShell(status);
  } catch (err) {
    clear(app);
    app.className = '';
    add(app, [
      h('div', { class: 'gate' },
        h('div', { class: 'gate-card' },
          h('div', { class: 'gate-brand' }, logoMark(), h('div', { class: 'gate-title', text: 'DocMan' })),
          h('p', { class: 'gate-sub', text: 'DocMan could not be reached.' }),
          h('div', { class: 'notice bad' }, icon('alert'), h('div', { text: err.message })),
          h('button', { class: 'btn primary', text: 'Try again', onClick: () => boot() }),
        )),
    ]);
  }
}

function teardown() {
  if (activeView && typeof activeView.dispose === 'function') {
    try { activeView.dispose(); } catch { /* ignore */ }
  }
  activeView = null;
  window.removeEventListener('hashchange', route);
}

const NAV = [
  { href: '#/', label: 'Overview', icon: 'dashboard' },
  { href: '#/containers', label: 'Containers', icon: 'box', count: 'containers' },
  { href: '#/images', label: 'Images', icon: 'layers', count: 'images' },
  { href: '#/volumes', label: 'Volumes', icon: 'database', count: 'volumes' },
  { href: '#/network', label: 'Addresses', icon: 'network' },
  { href: '#/settings', label: 'Settings', icon: 'settings' },
  // Opens in a new tab so live logs, consoles and half-finished forms survive.
  { href: '/help', label: 'Help', icon: 'help', external: true },
];

async function renderShell(status) {
  clear(app);
  app.className = 'shell';

  try {
    shellState.settings = await System.settings();
    applyTheme(shellState.settings.settings.theme);
  } catch { /* settings are optional for rendering */ }
  try {
    shellState.system = await System.info();
  } catch { /* the overview page will report it */ }

  const initial = (status.username || '?').slice(0, 1).toUpperCase();
  navHost = h('nav', { class: 'nav' });

  const sidebar = h('aside', { class: 'sidebar' },
    h('a', { class: 'brand', href: '#/', style: { textDecoration: 'none', color: 'inherit' } },
      logoMark(),
      h('div', { class: 'grow', style: { minWidth: 0 } },
        h('div', { class: 'brand-name', text: 'DocMan' }),
        h('div', { class: 'brand-host', text: hostLabel() }),
      ),
    ),
    navHost,
    h('div', { class: 'sidebar-foot' },
      h('div', { class: 'who' },
        h('div', { class: 'avatar', text: initial }),
        h('div', { class: 'grow', style: { minWidth: 0 } },
          h('div', { class: 'who-name truncate', text: status.username || 'signed in' }),
          h('div', { class: 'who-kind', text: status.auth_kind === 'token' ? 'API token' : 'signed in' }),
        ),
        h('button', {
          class: 'btn ghost icon', title: 'Sign out',
          onClick: async () => {
            const ok = await confirmDialog({
              title: 'Sign out of DocMan?',
              message: 'You will need your password or a passkey to get back in.',
              confirmLabel: 'Sign out',
              danger: false,
            });
            if (!ok) return;
            try { await State.logout(); } catch { /* ignore */ }
            boot();
          },
        }, icon('logout')),
      ),
    ),
  );

  crumbHost = h('div', { class: 'crumbs grow' });
  actionHost = h('div', { class: 'row' });
  contentHost = h('div', { class: 'content' });

  const main = h('div', { class: 'main' },
    h('header', { class: 'topbar' }, crumbHost, h('div', { class: 'spacer' }), actionHost),
    contentHost,
  );

  add(app, [sidebar, main]);
  renderNav();
  refreshCounts();

  window.addEventListener('hashchange', route);
  route();
  // Report on upgrades a previous page load was following, which carry on
  // on the server whatever happens to the page.
  resumeJobs();
}

function hostLabel() {
  const info = shellState.system && shellState.system.docker;
  if (!info) return location.host;
  return `${info.Name || location.hostname} · docker ${info.ServerVersion || '?'}`;
}

function renderNav() {
  if (!navHost) return;
  clear(navHost);
  const current = location.hash || '#/';
  add(navHost, NAV.map((item) => {
    if (item.external) {
      return h('a', { href: item.href, target: '_blank', rel: 'noopener', title: `${item.label} (opens in a new tab)` },
        icon(item.icon),
        h('span', { class: 'grow', text: item.label }),
        icon('external', 'ico faint'),
      );
    }
    const on = item.href === '#/'
      ? current === '#/' || current === ''
      : current.startsWith(item.href);
    const count = item.count ? shellState.counts[item.count] : null;
    return h('a', { href: item.href, class: on ? 'on' : '' },
      icon(item.icon),
      h('span', { class: 'grow', text: item.label }),
      count ? h('span', { class: 'count tabular', text: String(count) }) : null,
    );
  }));
}

async function refreshCounts() {
  try {
    const [containers, images, volumes] = await Promise.all([
      Containers.list(),
      Images.list(),
      Volumes.list(),
    ]);
    shellState.counts = {
      containers: (containers.containers || []).length,
      running: (containers.containers || []).filter((c) => c.state === 'running').length,
      images: (images.images || []).length,
      volumes: (volumes.volumes || []).length,
    };
    renderNav();
  } catch { /* counts are cosmetic */ }
}

// ---------- routing ----------

const ROUTES = [
  { pattern: /^#?\/?$/, view: dashboardView },
  { pattern: /^#\/containers$/, view: containersView },
  { pattern: /^#\/containers\/([^/]+)(?:\/([^/]+))?$/, view: containerView, keys: ['id', 'tab'] },
  { pattern: /^#\/images$/, view: imagesView },
  { pattern: /^#\/volumes$/, view: volumesView },
  { pattern: /^#\/volumes\/([^/?]+)(?:\?path=([^&]*))?$/, view: explorerView, keys: ['name', 'path'] },
  { pattern: /^#\/network$/, view: networkView },
  { pattern: /^#\/settings(?:\/([^/]+))?$/, view: settingsView, keys: ['tab'] },
];

function route() {
  const hash = location.hash || '#/';
  if (activeView && typeof activeView.dispose === 'function') {
    try { activeView.dispose(); } catch { /* ignore */ }
  }
  activeView = null;
  clear(contentHost);
  clear(crumbHost);
  clear(actionHost);
  contentHost.className = 'content';
  contentHost.scrollTop = 0;
  renderNav();

  for (const entry of ROUTES) {
    const match = hash.match(entry.pattern);
    if (!match) continue;
    const params = {};
    (entry.keys || []).forEach((key, index) => {
      const value = match[index + 1];
      if (value !== undefined) params[key] = decodeURIComponent(value);
    });
    try {
      activeView = entry.view(makeContext(params)) || null;
    } catch (err) {
      toastError(err);
      add(contentHost, [h('div', { class: 'notice bad' }, icon('alert'), h('div', { text: err.message }))]);
    }
    return;
  }
  add(contentHost, [
    h('div', { class: 'empty' },
      h('h3', { text: 'Nothing here' }),
      h('a', { href: '#/', text: 'Back to the overview' }),
    ),
  ]);
}

function makeContext(params) {
  return {
    host: contentHost,
    params,
    state: shellState,
    navigate: (hash) => { location.hash = hash; },
    setCrumbs: (...items) => {
      clear(crumbHost);
      const nodes = [];
      items.flat().forEach((item, index) => {
        if (index) nodes.push(h('span', { class: 'sep' }, icon('chevron', 'ico')));
        nodes.push(typeof item === 'string' ? h('h1', { text: item }) : item);
      });
      add(crumbHost, nodes);
    },
    setActions: (...nodes) => {
      clear(actionHost);
      add(actionHost, nodes);
    },
    flush: () => { contentHost.className = 'content flush'; },
    refreshCounts,
    toast,
    reload: () => route(),
  };
}

// ---------- go ----------

boot();
