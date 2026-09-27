// Registries: the list in Settings, and the add/edit dialog the deploy wizard
// also opens.

import {
  h, add, clear, icon, when, ago, toast, toastError, confirmDialog, modal,
  field, notice, spinner,
} from '../ui.js';
import { Registries } from '../api.js';

const AUTH_LABELS = {
  none: 'No sign-in (public images only)',
  basic: 'Username and password or access token',
  token: 'Identity token',
  aws: 'AWS access key',
  gcp: 'Service-account JSON key',
};

const OPTION_FIELDS = {
  region: ['AWS region', 'eu-west-1', 'Taken from the address when left empty.'],
  repo: ['Repository', 'owner/name, or paste https://github.com/owner/name', 'The repository that holds the pre-built image archives.'],
  ref: ['Branch or tag', 'the default branch', 'Where to look in the repository’s files. Releases are always looked at.'],
  path: ['Folder', 'dist', 'Only look in this folder of the repository. Empty looks everywhere.'],
  api_url: ['API address', 'https://github.example.com/api/v3', 'Only for GitHub Enterprise. Defaults to https://<your GitHub host>/api/v3.'],
};

/** One line saying how a registry signs in. */
function authSummary(reg) {
  switch (reg.auth_type) {
    case 'none': return h('span', { class: 'faint', text: 'anonymous' });
    case 'basic': return h('span', { class: 'mono small', text: reg.username });
    case 'aws': return h('span', {}, h('span', { class: 'badge plain', text: 'AWS key' }), ' ', h('span', { class: 'mono small', text: reg.username }));
    case 'gcp': return h('span', { class: 'badge plain', text: 'JSON key' });
    case 'token': return h('span', { class: 'badge plain', text: 'identity token' });
    default: return h('span', { text: reg.auth_type });
  }
}

/** Add or edit a registry. Resolves with the saved registry, or null. */
export function registryDialog(kinds, existing = null) {
  return modal({
    title: existing ? `Edit ${existing.name}` : 'Add a registry',
    subtitle: existing ? existing.host : 'Where DocMan searches for and pulls images',
    confirmLabel: existing ? 'Save' : 'Add registry',
    render: ({ body }) => {
      const kindSelect = h('select', { disabled: !!existing },
        kinds.map((k) => h('option', { value: k.kind, text: k.name })));
      const name = h('input', { type: 'text', maxLength: 60 });
      const hostInput = h('input', { type: 'text', class: 'mono', spellcheck: false });
      const authSelect = h('select');
      const username = h('input', { type: 'text', autocomplete: 'off', spellcheck: false });
      const secret = h('input', { type: 'password', autocomplete: 'new-password' });
      const keyArea = h('textarea', { class: 'mono', rows: 6, spellcheck: false });
      const options = {};
      for (const [key, [, placeholder]] of Object.entries(OPTION_FIELDS)) {
        options[key] = h('input', { type: 'text', class: 'mono', placeholder, spellcheck: false });
      }
      const hostHelp = h('div', { class: 'help' });
      const authHelp = h('div', { class: 'help' });
      const fields = h('div', { class: 'col' });
      let kind = null;
      // The name follows the kind until the user types their own.
      let nameTouched = !!existing;
      name.addEventListener('input', () => { nameTouched = name.value.trim() !== ''; });

      const hasSaved = (type) => !!(existing && existing.has_secret && existing.auth_type === type);

      const drawAuth = () => {
        clear(fields);
        const type = authSelect.value;
        const kept = hasSaved(type);
        const keepNote = kept ? 'Leave empty to keep the one saved.' : '';
        secret.placeholder = kept ? '•••••••• (saved)' : '';
        keyArea.placeholder = kept ? 'Saved. Paste a new key to replace it.' : '{ "type": "service_account", ... }';
        if (type === 'basic') {
          add(fields, [h('div', { class: 'grid c2' },
            field('Username', username),
            field('Password or access token', secret, keepNote))]);
        } else if (type === 'token') {
          add(fields, [field('Identity token', secret, keepNote || 'An OAuth refresh token the registry issued.')]);
        } else if (type === 'aws') {
          add(fields, [h('div', { class: 'grid c2' },
            field('Access key ID', username),
            field('Secret access key', secret, keepNote))]);
        } else if (type === 'gcp') {
          add(fields, [field('Service-account key (JSON)', keyArea, keepNote)]);
        }
        for (const key of kind.options || []) {
          const [label, , help] = OPTION_FIELDS[key];
          add(fields, [field(label, options[key], help)]);
        }
      };

      const drawKind = (keepHost) => {
        kind = kinds.find((k) => k.kind === kindSelect.value) || kinds[0];
        if (!nameTouched) name.value = kind.name;
        name.placeholder = kind.name;
        if (!keepHost) hostInput.value = kind.host || '';
        hostInput.placeholder = kind.host || kind.host_hint || '';
        hostHelp.textContent = kind.host
          ? 'The usual address is filled in; change it for a self-hosted install.'
          : `For example ${kind.host_hint}.`;
        clear(authSelect);
        add(authSelect, kind.auth.map((a) => h('option', { value: a, text: AUTH_LABELS[a] || a })));
        authHelp.textContent = [kind.auth_help, kind.search_note || (kind.search ? '' : 'Search is not available.')]
          .filter(Boolean).join(' ');
        drawAuth();
      };

      if (existing) {
        kindSelect.value = existing.kind;
        name.value = existing.name;
        hostInput.value = existing.host;
        username.value = existing.auth_type === 'gcp' ? '' : existing.username || '';
        for (const [key, input] of Object.entries(options)) input.value = (existing.options || {})[key] || '';
      }
      drawKind(!!existing);
      if (existing) { authSelect.value = existing.auth_type; drawAuth(); }
      kindSelect.addEventListener('change', () => drawKind(false));
      authSelect.addEventListener('change', drawAuth);

      add(body, [h('div', { class: 'col' },
        h('div', { class: 'grid c2' },
          field('Kind', kindSelect),
          field('Name', name, 'How the registry is shown in DocMan.')),
        h('div', { class: 'field' }, h('label', { text: 'Address' }), hostInput, hostHelp),
        h('div', { class: 'field' }, h('label', { text: 'Sign-in' }), authSelect, authHelp),
        fields,
      )]);

      return {
        submit: async () => {
          const type = authSelect.value;
          const typed = type === 'gcp' ? keyArea.value.trim() : secret.value;
          if (type === 'gcp' && typed) {
            try { JSON.parse(typed); } catch { throw new Error('The service-account key is not valid JSON.'); }
          }
          const payload = {
            kind: kind.kind,
            name: name.value.trim(),
            host: hostInput.value.trim(),
            auth_type: type,
            username: username.value.trim(),
            // null keeps the saved secret; anything typed replaces it.
            secret: typed || (hasSaved(type) ? null : ''),
            options: Object.fromEntries(Object.entries(options).map(([key, input]) => [key, input.value.trim()])),
          };
          return existing ? Registries.update(existing.id, payload) : Registries.create(payload);
        },
      };
    },
  });
}

/** The Settings → Registries tab. */
export function registriesPanel(host, panel) {
  let kinds = [];
  const load = () => {
    clear(host);
    add(host, [spinner()]);
    Registries.list().then((data) => { kinds = data.kinds || []; render(data.registries || []); }).catch((err) => {
      clear(host);
      add(host, [notice(err.message, 'bad')]);
    });
  };

  const edit = async (reg) => {
    const saved = await registryDialog(kinds, reg);
    if (!saved) return;
    toast(reg ? `${saved.name} saved` : `${saved.name} added`, 'ok');
    load();
  };

  const test = async (reg, button) => {
    button.disabled = true;
    try {
      const result = await Registries.test(reg.id);
      toast(`${reg.name}: ${result.message}`, result.ok ? 'ok' : 'bad');
    } catch (err) { toastError(err); } finally { button.disabled = false; }
  };

  const makeDefault = async (reg) => {
    try {
      await Registries.makeDefault(reg.id);
      toast(`${reg.name} is now the default`, 'ok');
      load();
    } catch (err) { toastError(err); }
  };

  const remove = async (reg) => {
    const ok = await confirmDialog({
      title: `Remove ${reg.name}?`,
      message: 'Its saved sign-in is deleted. Images already pulled from it stay on the host, but later pulls and update checks for them go without a sign-in.',
      confirmLabel: 'Remove registry',
    });
    if (!ok) return;
    try {
      await Registries.remove(reg.id);
      toast(`${reg.name} removed`, 'ok');
      load();
    } catch (err) { toastError(err); }
  };

  function render(list) {
    clear(host);
    add(host, [
      panel('Registries', 'layers', h('div', { class: 'col' },
        h('p', { class: 'muted', style: { margin: 0 } },
          'The registries DocMan searches and pulls from when you create a container; the default is selected first. '
          + 'A registry\'s sign-in is also used by Images → Pull, recreates and the daily update check for any image whose name points at it. '
          + 'Passwords, tokens and keys are stored encrypted and are never shown again.'),
        h('div', {}, h('button', { class: 'btn primary', onClick: () => edit(null) }, icon('plus'), 'Add a registry')),
      )),
      h('div', { class: 'card' }, h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
        h('thead', {}, h('tr', {},
          h('th', { text: 'Name' }), h('th', { text: 'Address' }), h('th', { text: 'Signs in as' }),
          h('th', { text: 'Updated' }), h('th', { class: 'actions' }),
        )),
        h('tbody', {}, list.map((reg) => {
          const kind = kinds.find((k) => k.kind === reg.kind);
          const testButton = h('button', { class: 'btn sm', title: 'Check the address and sign-in' }, icon('check'), 'Test');
          testButton.addEventListener('click', () => test(reg, testButton));
          return h('tr', {},
            h('td', {},
              h('div', { class: 'row' },
                h('span', { style: { fontWeight: '550', whiteSpace: 'nowrap' }, text: reg.name }),
                reg.is_default ? h('span', { class: 'badge info', title: 'Selected first when creating a container', text: 'default' }) : null),
              kind && kind.name !== reg.name ? h('div', { class: 'small faint', text: kind.name }) : null),
            h('td', { class: 'mono small', text: reg.kind === 'github-repo' && reg.options && reg.options.repo ? `${reg.host}/${reg.options.repo}` : reg.host }),
            h('td', {}, authSummary(reg)),
            h('td', { class: 'small faint nowrap', title: when(reg.updated_at), text: ago(reg.updated_at) }),
            h('td', { class: 'actions' }, h('div', { class: 'btn-group' },
              testButton,
              reg.is_default ? null : h('button', {
                class: 'btn sm', title: 'Select this registry first when creating a container', onClick: () => makeDefault(reg),
              }, 'Make default'),
              h('button', { class: 'btn sm icon', title: 'Edit', onClick: () => edit(reg) }, icon('edit')),
              h('button', {
                class: 'btn sm icon danger',
                title: reg.is_default ? 'Make another registry the default first' : 'Remove',
                disabled: reg.is_default, onClick: () => remove(reg),
              }, icon('trash')))),
          );
        })),
      ))),
    ]);
  }

  load();
}
