// Settings: account, passkeys, users, API tokens, registries, preferences and
// the audit trail.

import {
  h, add, clear, icon, when, ago, toast, toastError, confirmDialog, modal,
  field, notice, spinner, copyText, emptyState,
} from '../ui.js';
import { Account, System, Users } from '../api.js';
import * as passkeys from '../passkeys.js';
import { registriesPanel } from './registries.js';

const TABS = [
  ['account', 'Account', 'user'],
  ['passkeys', 'Passkeys', 'key'],
  ['users', 'Users', 'users'],
  ['tokens', 'API tokens', 'shield'],
  ['registries', 'Registries', 'layers'],
  ['preferences', 'Preferences', 'settings'],
  ['audit', 'Activity', 'history'],
];

export function settingsView(ctx) {
  const tab = ctx.params.tab || 'account';
  ctx.setCrumbs('Settings');
  ctx.flush();

  const tabsBar = h('div', { class: 'tabs' },
    ...TABS.map(([key, label, iconName]) => h('a', {
      href: `#/settings/${key}`,
      class: key === tab ? 'on' : '',
    }, icon(iconName), h('span', { text: label }))));

  const host = h('div', { class: 'content' });
  add(ctx.host, [tabsBar, host]);

  switch (tab) {
    case 'passkeys': passkeysPanel(host); break;
    case 'users': usersPanel(host); break;
    case 'tokens': tokensPanel(host); break;
    case 'registries': registriesPanel(host, panel); break;
    case 'preferences': preferencesPanel(host, ctx); break;
    case 'audit': auditPanel(host); break;
    default: accountPanel(host);
  }
  return { dispose() {} };
}

function panel(title, iconName, children, subtitle) {
  return h('div', { class: 'card', style: { marginBottom: '14px' } },
    h('div', { class: 'card-head' }, icon(iconName), h('div', { class: 'grow' },
      h('h2', { text: title }),
      subtitle ? h('div', { class: 'small faint', text: subtitle }) : null)),
    h('div', { class: 'card-body' }, children),
  );
}

// ---------- account ----------

function accountPanel(host) {
  const load = () => {
    clear(host);
    add(host, [spinner()]);
    Promise.all([Account.me(), Account.sessions()])
      .then(([me, sessions]) => draw(me, sessions))
      .catch((err) => {
        clear(host);
        add(host, [notice(err.message, 'bad')]);
      });
  };

  const draw = (me, sessions) => {
    clear(host);

    const usernameInput = h('input', { type: 'text', value: me.username, spellcheck: false });
    const saveUsername = h('button', { class: 'btn', onClick: async () => {
      saveUsername.disabled = true;
      try {
        await Account.setUsername(usernameInput.value.trim());
        toast('Username changed', 'ok');
      } catch (err) { toastError(err); }
      saveUsername.disabled = false;
    } }, icon('check'), 'Save');

    const current = h('input', { type: 'password', autocomplete: 'current-password' });
    const next = h('input', { type: 'password', autocomplete: 'new-password' });
    const confirmField = h('input', { type: 'password', autocomplete: 'new-password' });
    const savePassword = h('button', { class: 'btn primary', onClick: async () => {
      if (next.value !== confirmField.value) {
        toast('The two new passwords do not match', 'bad');
        return;
      }
      savePassword.disabled = true;
      try {
        await Account.setPassword(current.value, next.value);
        toast('Password changed', 'ok');
        current.value = ''; next.value = ''; confirmField.value = '';
      } catch (err) { toastError(err); }
      savePassword.disabled = false;
    } }, icon('check'), 'Change password');

    add(host, [
      panel('Who you are', 'user', h('div', { class: 'col' },
        h('dl', { class: 'kv' },
          h('dt', { text: 'Signed in with' }), h('dd', { text: me.auth_kind === 'token' ? 'an API token' : 'a browser session' }),
          h('dt', { text: 'Account created' }), h('dd', { text: when(me.created_at) }),
          h('dt', { text: 'Passkeys' }), h('dd', {}, h('a', { href: '#/settings/passkeys', text: `${me.passkey_count} registered` })),
        ),
        h('div', { class: 'row', style: { maxWidth: '420px' } },
          h('div', { class: 'grow' }, field('Username', usernameInput)),
          h('div', { style: { alignSelf: 'flex-end' } }, saveUsername)),
      )),

      panel('Password', 'shield', h('div', { class: 'col', style: { maxWidth: '460px' } },
        me.password_enabled
          ? notice(me.totp_enabled
            ? 'Password sign-in is on and protected by your authenticator app, so it stays available even with two passkeys registered.'
            : 'Password sign-in is on. Register two passkeys and DocMan turns it off automatically — unless you enrol an authenticator app below.', 'info')
          : notice('Password sign-in is off because two passkeys are registered. Enrol an authenticator app below to bring it back as a two-step alternative.', 'ok'),
        field('Current password', current),
        field('New password', next, 'At least 12 characters, mixing letters with digits or symbols.'),
        field('Confirm new password', confirmField),
        h('div', {}, savePassword),
      ), me.has_password ? null : 'No password is set on this account'),

      totpPanel(me, load),
      sessionsPanel(sessions.sessions || []),
    ]);
  };

  load();
}

// ---------- authenticator app ----------

function totpPanel(me, reload) {
  if (me.totp_enabled) {
    return panel('Authenticator app', 'clock', h('div', { class: 'col' },
      notice('An authenticator app is enrolled. Password sign-in now asks for a six digit code as well, '
        + 'and stays available even once two passkeys are registered.', 'ok'),
      h('div', {}, h('button', {
        class: 'btn danger',
        onClick: async () => {
          const chosen = await modal({
            title: 'Remove the authenticator app?',
            confirmLabel: 'Remove authenticator',
            danger: true,
            render: ({ body }) => {
              const password = h('input', { type: 'password', autocomplete: 'current-password' });
              add(body, [
                h('p', { class: 'muted', style: { margin: 0 } },
                  'Sign-in will stop asking for a code. If two passkeys are registered, '
                  + 'password sign-in switches off again and passkeys become the only way in.'),
                field('Confirm with your password', password),
              ]);
              return { submit: () => ({ password: password.value }) };
            },
          });
          if (!chosen) return;
          try {
            const result = await Account.totpDisable(chosen.password, '');
            toast(result.password_enabled
              ? 'Authenticator removed. Password sign-in is still available.'
              : 'Authenticator removed. Password sign-in is off; use a passkey.', 'ok', 8000);
            reload();
          } catch (err) { toastError(err); }
        },
      }, icon('trash'), 'Remove authenticator')),
    ), 'Password sign-in requires a one-time code');
  }

  return panel('Authenticator app', 'clock', h('div', { class: 'col' },
    h('p', { class: 'muted', style: { margin: 0 } },
      'Enrol an authenticator app and password sign-in becomes two-step: password plus a '
      + 'six digit code. It also keeps password sign-in available after you register two '
      + 'passkeys, so you have a way in that does not depend on a passkey-capable device.'),
    h('div', {}, h('button', {
      class: 'btn primary',
      onClick: () => enrolTOTP(reload),
    }, icon('clock'), 'Set up authenticator app')),
  ), 'Optional second factor for password sign-in');
}

async function enrolTOTP(reload) {
  await modal({
    title: 'Set up an authenticator app',
    subtitle: 'Scan the code, then confirm with the six digits it shows',
    wide: true,
    confirmLabel: 'Enable',
    render: (ctx) => {
      const { body } = ctx;
      add(body, [spinner()]);
      const code = h('input', {
        type: 'text', inputmode: 'numeric', maxLength: 7, class: 'mono',
        placeholder: '123456', style: { letterSpacing: '0.2em', textAlign: 'center', fontSize: '17px' },
      });
      let ready = false;

      Account.totpBegin().then((data) => {
        clear(body);
        ready = true;
        add(body, [
          h('div', { class: 'row', style: { alignItems: 'flex-start', gap: '20px', flexWrap: 'wrap' } },
            data.qr
              ? h('img', {
                src: data.qr, alt: 'Authenticator QR code', width: 190, height: 190,
                style: {
                  width: '190px', height: '190px', imageRendering: 'pixelated',
                  background: '#fff', padding: '8px', borderRadius: '10px', flex: 'none',
                },
              })
              : null,
            h('div', { class: 'col grow', style: { minWidth: '240px' } },
              h('div', { class: 'section-title', text: 'Scan with your app' }),
              h('p', { class: 'small muted', style: { margin: 0 } },
                'Google Authenticator, 1Password, Bitwarden, Aegis, Authy — any of them.'),
              h('div', { class: 'row' },
                h('a', { class: 'btn sm', href: data.uri }, icon('link'), 'Open in an installed app')),
              h('div', { class: 'section-title', style: { marginTop: '8px' }, text: 'Or type the secret' }),
              h('div', { class: 'row' },
                h('code', { class: 'pill', style: { fontSize: '13px', padding: '6px 9px' }, text: data.secret_formatted }),
                h('button', {
                  class: 'btn sm ghost icon', title: 'Copy secret',
                  onClick: () => copyText(data.secret, 'Secret copied'),
                }, icon('copy'))),
              h('div', { class: 'small faint' },
                `Account ${data.issuer}:${data.account} · ${data.algorithm} · ${data.digits} digits · ${data.period}s`),
            ),
          ),
          h('div', { style: { maxWidth: '260px' } }, field('Code from your app', code)),
          notice('The secret is only stored once you confirm a working code, so an abandoned '
            + 'setup can never lock you out.', 'info'),
        ]);
        setTimeout(() => code.focus(), 60);
      }).catch((err) => {
        clear(body);
        add(body, [notice(err.message, 'bad')]);
      });

      return {
        submit: async () => {
          if (!ready) throw new Error('still loading, try again in a moment');
          const result = await Account.totpEnable(code.value);
          toast('Authenticator enrolled. Password sign-in now asks for a code.', 'ok', 8000);
          if (reload) reload();
          return result;
        },
      };
    },
  });
}

function sessionsPanel(sessions) {
  const body = h('tbody');
  add(body, sessions.map((sess) => h('tr', {},
    h('td', {}, h('div', { class: 'row' },
      h('span', { class: `dot ${sess.current ? 'ok' : ''}` }),
      h('span', { text: sess.current ? 'This browser' : 'Another browser' }),
    )),
    h('td', {}, h('span', { class: 'badge plain', text: sess.method })),
    h('td', { class: 'mono small', text: sess.ip || '—' }),
    h('td', { class: 'small faint truncate', style: { maxWidth: '260px' }, text: sess.ua || '—' }),
    h('td', { class: 'small faint', text: ago(sess.seen_at) }),
  )));
  return panel('Active sessions', 'clock', h('div', { class: 'col' },
    h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
      h('thead', {}, h('tr', {},
        h('th', { text: 'Session' }), h('th', { text: 'Signed in with' }),
        h('th', { text: 'Address' }), h('th', { text: 'Browser' }), h('th', { text: 'Last seen' }),
      )),
      body,
    )),
    h('div', {}, h('button', {
      class: 'btn', onClick: async () => {
        const ok = await confirmDialog({
          title: 'Sign out everywhere else?',
          message: 'Every other browser session is ended. This one stays signed in.',
          confirmLabel: 'Sign out other sessions',
          danger: false,
        });
        if (!ok) return;
        try {
          await Account.revokeSessions();
          toast('Other sessions signed out', 'ok');
          location.hash = '#/settings/account';
        } catch (err) { toastError(err); }
      },
    }, icon('logout'), 'Sign out other sessions')),
  ));
}

// ---------- passkeys ----------

function passkeysPanel(host) {
  const load = () => {
    clear(host);
    add(host, [spinner()]);
    Account.passkeys().then(render).catch((err) => {
      clear(host);
      add(host, [notice(err.message, 'bad')]);
    });
  };

  const addPasskey = async () => {
    try {
      const begin = await Account.registerBegin();
      const credential = await passkeys.register(begin.options);
      const result = await Account.registerFinish({
        challenge_id: begin.challenge_id,
        name: '',
        response: credential,
      });
      if (!result.password_enabled) {
        toast('Passkey added. Password sign-in is now switched off.', 'ok', 9000);
      } else {
        toast(`Passkey added. ${Math.max(0, 2 - result.passkey_count)} more and password sign-in switches off.`, 'ok', 8000);
      }
      load();
    } catch (err) {
      if (err.name === 'ApiError') toastError(err);
      else toast(passkeys.describeError(err), 'bad', 9000);
    }
  };

  function render(data) {
    clear(host);
    const list = data.passkeys || [];
    const remaining = Math.max(0, data.target - list.length);

    const progress = h('div', { class: 'row', style: { gap: '6px' } },
      ...Array.from({ length: data.target }, (_, index) => h('i', {
        style: {
          height: '4px', flex: '1', borderRadius: '999px',
          background: index < list.length ? 'var(--ok)' : 'var(--surface-3)',
        },
      })));

    add(host, [
      panel('Passkeys', 'key', h('div', { class: 'col' },
        h('p', { class: 'muted', style: { margin: 0 } },
          'A passkey is held by this device or your password manager and cannot be phished or reused. '
          + 'DocMan switches password sign-in off for your account once you have registered two, so losing one device never locks you out.'),
        progress,
        data.totp_enabled
          ? notice(`${list.length} of ${data.target} registered. Password sign-in stays available because an `
            + 'authenticator app is enrolled, so it is password plus a one-time code.', 'info')
          : data.password_enabled
            ? notice(remaining
              ? `${list.length} of ${data.target} registered. Add ${remaining} more and password sign-in switches off automatically.`
              : 'Password sign-in is still on.', remaining ? 'warn' : 'info')
            : notice('Password sign-in is switched off for your account. Only your passkeys can sign in as you.', 'ok'),
        passkeys.supported()
          ? h('div', { class: 'row' },
            h('button', { class: 'btn primary', onClick: addPasskey }, icon('plus'), 'Add a passkey'),
            h('span', { class: 'small faint', text: 'Use this device, a phone, or a hardware security key.' }))
          : notice('This browser cannot create passkeys. Try a current version of Chrome, Edge, Safari or Firefox over HTTPS.', 'bad'),
      )),
      list.length
        ? h('div', { class: 'card' },
          h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
            h('thead', {}, h('tr', {},
              h('th', { text: 'Name' }), h('th', { text: 'Type' }), h('th', { text: 'Added' }),
              h('th', { text: 'Last used' }), h('th', { class: 'actions' }),
            )),
            h('tbody', {}, list.map((key) => h('tr', {},
              h('td', {}, h('div', { class: 'row' }, icon('key'),
                h('span', { style: { fontWeight: '550' }, text: key.name || 'Passkey' }),
                key.backed_up ? h('span', { class: 'badge info', title: 'Synced by your password manager', text: 'synced' }) : null)),
              h('td', { class: 'small faint', text: `${key.algorithm}${key.transports ? ` · ${key.transports}` : ''}` }),
              h('td', { class: 'small faint', text: ago(key.created_at) }),
              h('td', { class: 'small faint', text: key.last_used_at ? ago(key.last_used_at) : 'never' }),
              h('td', { class: 'actions' }, h('div', { class: 'btn-group' },
                h('button', {
                  class: 'btn sm icon', title: 'Rename',
                  onClick: async () => {
                    const chosen = await modal({
                      title: 'Rename passkey',
                      confirmLabel: 'Rename',
                      render: ({ body }) => {
                        const input = h('input', { type: 'text', value: key.name });
                        add(body, [field('Name', input)]);
                        return { submit: () => ({ name: input.value.trim() }) };
                      },
                    });
                    if (!chosen || !chosen.name) return;
                    try {
                      await Account.renamePasskey(key.id, chosen.name);
                      load();
                    } catch (err) { toastError(err); }
                  },
                }, icon('edit')),
                h('button', {
                  class: 'btn sm icon danger', title: 'Remove',
                  onClick: async () => {
                    const ok = await confirmDialog({
                      title: `Remove “${key.name || 'this passkey'}”?`,
                      message: list.length <= data.target
                        ? 'With fewer than two passkeys, password sign-in comes back on as a fallback.'
                        : 'The passkey will no longer be able to sign in.',
                      confirmLabel: 'Remove passkey',
                    });
                    if (!ok) return;
                    try {
                      const result = await Account.deletePasskey(key.id);
                      toast(result.password_enabled
                        ? 'Passkey removed. Password sign-in is available again.'
                        : 'Passkey removed.', 'ok');
                      load();
                    } catch (err) { toastError(err); }
                  },
                }, icon('trash')),
              )),
            ))),
          )))
        : emptyState('No passkeys yet', 'Add one from the device you use most, then a second as a backup.'),
    ]);
  }

  load();
}

// ---------- users ----------

function usersPanel(host) {
  const load = () => {
    clear(host);
    add(host, [spinner()]);
    Users.list().then(render).catch((err) => {
      clear(host);
      add(host, [notice(err.message, 'bad')]);
    });
  };

  // A starting password is set by whoever adds the account or resets it. The
  // generator makes a strong one to hand over; the user changes it later.
  const passwordFields = (body, intro) => {
    const password = h('input', { type: 'text', class: 'mono', autocomplete: 'off', spellcheck: false });
    const generate = () => { password.value = strongPassword(); };
    add(body, [
      intro,
      h('div', { class: 'row' },
        h('div', { class: 'grow' }, field('Starting password', password,
          'At least 12 characters, mixing letters with digits or symbols. Share it securely; they can change it after signing in.')),
        h('div', { style: { alignSelf: 'flex-end', marginBottom: '22px' } },
          h('button', { class: 'btn sm', type: 'button', onClick: generate }, icon('refresh'), 'Generate'),
          h('button', {
            class: 'btn sm ghost icon', type: 'button', title: 'Copy',
            onClick: () => password.value && copyText(password.value, 'Password copied'),
          }, icon('copy'))),
      ),
    ]);
    generate();
    return password;
  };

  const create = async () => {
    const chosen = await modal({
      title: 'Add a user',
      confirmLabel: 'Add user',
      render: ({ body }) => {
        const username = h('input', { type: 'text', spellcheck: false, autocomplete: 'off', placeholder: 'jsmith' });
        add(body, [field('Username', username, '3 to 32 characters: letters, digits, dot, dash and underscore.')]);
        const password = passwordFields(body, h('p', { class: 'muted small', style: { margin: 0 } },
          'Every user is a full administrator of this host. They sign in with this password, then add their own passkeys or authenticator app.'));
        setTimeout(() => username.focus(), 50);
        return {
          submit: async () => {
            const result = await Users.create(username.value.trim(), password.value);
            return { username: result.user.username, password: password.value };
          },
        };
      },
    });
    if (!chosen) return;
    toast(`${chosen.username} added`, 'ok');
    load();
  };

  const reset = async (user) => {
    const chosen = await modal({
      title: `Reset sign-in for ${user.username}?`,
      confirmLabel: 'Reset sign-in',
      danger: true,
      render: ({ body }) => {
        const password = passwordFields(body, notice(
          `${user.username} gets this new password. Their ${user.passkey_count} passkey${user.passkey_count === 1 ? '' : 's'}`
          + `${user.totp_enabled ? ' and authenticator app are' : ' are'} removed and every session they have is signed out, `
          + 'so nothing else can still sign in as them.', 'warn'));
        return { submit: async () => { await Users.reset(user.id, password.value); return true; } };
      },
    });
    if (!chosen) return;
    toast(`Sign-in reset for ${user.username}`, 'ok');
    load();
  };

  const remove = async (user) => {
    const ok = await confirmDialog({
      title: `Remove ${user.username}?`,
      message: 'The account, its passkeys and its sessions are deleted. Its entries in the activity log, and any API tokens it created, are kept.',
      confirmLabel: 'Remove user',
    });
    if (!ok) return;
    try {
      await Users.remove(user.id);
      toast(`${user.username} removed`, 'ok');
      load();
    } catch (err) { toastError(err); }
  };

  function signIn(user) {
    const parts = [];
    if (user.passkey_count) parts.push(h('span', { class: 'badge ok', text: `${user.passkey_count} passkey${user.passkey_count === 1 ? '' : 's'}` }));
    if (user.password_enabled) {
      parts.push(h('span', { class: 'badge plain', text: user.totp_enabled ? 'password + code' : 'password' }));
    }
    if (!parts.length) parts.push(h('span', { class: 'badge bad', text: 'no way in' }));
    return h('div', { class: 'chips' }, parts);
  }

  function render(data) {
    clear(host);
    const users = data.users || [];
    add(host, [
      panel('Users', 'users', h('div', { class: 'col' },
        h('p', { class: 'muted', style: { margin: 0 } },
          'Each user has their own password, passkeys and authenticator app, and every user can manage this host fully. '
          + 'Their actions are recorded under their own name in the activity log.'),
        h('div', {}, h('button', { class: 'btn primary', onClick: create }, icon('plus'), 'Add a user')),
      )),
      h('div', { class: 'card' }, h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
        h('thead', {}, h('tr', {},
          h('th', { text: 'User' }), h('th', { text: 'Signs in with' }), h('th', { text: 'Added' }),
          h('th', { text: 'Last signed in' }), h('th', { class: 'actions' }),
        )),
        h('tbody', {}, users.map((user) => h('tr', {},
          h('td', {}, h('div', { class: 'row' },
            h('div', { class: 'avatar', text: user.username.slice(0, 1).toUpperCase() }),
            h('span', { style: { fontWeight: '550' }, text: user.username }),
            user.current ? h('span', { class: 'badge info', text: 'you' }) : null)),
          h('td', {}, signIn(user)),
          h('td', { class: 'small faint', title: when(user.created_at), text: ago(user.created_at) }),
          h('td', { class: 'small faint', title: user.last_login_at ? when(user.last_login_at) : '', text: user.last_login_at ? ago(user.last_login_at) : 'never' }),
          h('td', { class: 'actions' }, user.current
            ? h('a', { class: 'btn sm ghost', href: '#/settings/account', text: 'Your account' })
            : h('div', { class: 'btn-group' },
              h('button', { class: 'btn sm', title: 'New password; removes their passkeys and authenticator app', onClick: () => reset(user) },
                icon('key'), 'Reset sign-in'),
              h('button', {
                class: 'btn sm icon danger', title: users.length <= 1 ? 'DocMan needs at least one user' : 'Remove',
                disabled: users.length <= 1, onClick: () => remove(user),
              }, icon('trash')))),
        ))),
      ))),
    ]);
  }

  load();
}

/** Who created a token: a user, another token, a removed user, or unknown. */
function creatorCell(token) {
  if (!token.created_by) {
    return h('span', { class: 'faint', title: 'Created before DocMan recorded who creates tokens', text: 'not recorded' });
  }
  if (token.created_by.startsWith('token:')) return actorLabel(token.created_by);
  return h('span', {}, token.created_by,
    token.created_by_removed ? h('span', { class: 'badge plain', style: { marginLeft: '6px' }, text: 'removed' }) : null);
}

/** Show an activity-log actor, naming tokens as tokens. */
function actorLabel(actor) {
  if (actor && actor.startsWith('token:')) {
    return h('span', { class: 'row', style: { gap: '6px', display: 'inline-flex' }, title: 'An API token' },
      icon('shield'), h('span', { text: actor.slice(6) }), h('span', { class: 'badge plain', text: 'API token' }));
  }
  return h('span', { text: actor || '—' });
}

/** A random 20-character password drawn from an unambiguous alphabet. */
function strongPassword() {
  const letters = 'abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ';
  const all = `${letters}23456789-_!@#%+=`;
  const bytes = crypto.getRandomValues(new Uint32Array(20));
  let out = '';
  for (const n of bytes) out += all[n % all.length];
  // Guarantee the mix the password rules ask for.
  return `${out.slice(0, 18)}${letters[bytes[0] % letters.length]}${'23456789'[bytes[1] % 8]}`;
}

// ---------- api tokens ----------

function tokensPanel(host) {
  const load = () => {
    clear(host);
    add(host, [spinner()]);
    Account.tokens().then(render).catch((err) => {
      clear(host);
      add(host, [notice(err.message, 'bad')]);
    });
  };

  const create = async () => {
    const chosen = await modal({
      title: 'New API token',
      confirmLabel: 'Create token',
      render: ({ body }) => {
        const name = h('input', { type: 'text', placeholder: 'ci-deploy', spellcheck: false });
        const days = h('input', { type: 'number', min: '0', step: '1', placeholder: '0 for no expiry' });
        add(body, [
          h('p', { class: 'muted', style: { margin: 0 } },
            'The token has full rights over this host and belongs to DocMan, not to you: it keeps working even if your account is removed. '
            + 'It is shown once and stored only as a hash. You are recorded as the one who created it.'),
          field('Name', name, 'Something that says where it will be used.'),
          field('Expires in (days)', days, 'Leave empty or 0 for a token that never expires.'),
        ]);
        return { submit: () => ({ name: name.value.trim(), days: Number(days.value) || 0 }) };
      },
    });
    if (!chosen || !chosen.name) return;
    try {
      const result = await Account.createToken(chosen.name, chosen.days);
      await showToken(result.token, chosen.name);
      load();
    } catch (err) { toastError(err); }
  };

  async function showToken(token, name) {
    await modal({
      title: 'Copy this token now',
      subtitle: 'It cannot be shown again',
      wide: true,
      hideConfirm: true,
      cancelLabel: 'Done',
      render: ({ body }) => {
        const value = h('input', { type: 'text', class: 'mono', value: token, readOnly: true });
        const example = `curl -sS -H "Authorization: Bearer ${token}" \\\n  ${location.origin}/api/containers`;
        add(body, [
          notice('DocMan stores only a hash of this token. If you lose it, revoke it and make a new one.', 'warn'),
          h('div', { class: 'row' },
            h('div', { class: 'grow' }, field(name, value)),
            h('div', { style: { alignSelf: 'flex-end' } }, h('button', {
              class: 'btn primary', onClick: () => copyText(token, 'Token copied'),
            }, icon('copy'), 'Copy')),
          ),
          h('div', { class: 'section-title', text: 'Try it' }),
          h('div', { class: 'progress-log', text: example }),
          h('p', { class: 'small faint', style: { margin: 0 } },
            'Every endpoint the UI uses accepts this token. The ',
            h('a', { href: '/help/api', target: '_blank', rel: 'noopener', text: 'API reference' }),
            ' lists them all.'),
        ]);
        setTimeout(() => { value.select(); }, 60);
        return null;
      },
    });
  }

  function render(data) {
    clear(host);
    const tokens = data.tokens || [];
    add(host, [
      panel('API access', 'shield', h('div', { class: 'col' },
        h('p', { class: 'muted', style: { margin: 0 } },
          'Everything in this UI is available over the same REST API. Send the token as ',
          h('code', { class: 'pill', text: 'Authorization: Bearer …' }),
          '. WebSocket streams also accept it as an ',
          h('code', { class: 'pill', text: 'access_token' }),
          ' query parameter.'),
        h('p', { class: 'muted small', style: { margin: 0 } },
          'Tokens belong to DocMan rather than to a user, so every user sees and can revoke all of them, and they keep working '
          + 'if the user who created them is removed. Anything done with a token is recorded in the activity log under the token’s name.'),
        h('div', {}, h('button', { class: 'btn primary', onClick: create }, icon('plus'), 'New token')),
      )),
      tokens.length
        ? h('div', { class: 'card' }, h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
          h('thead', {}, h('tr', {},
            h('th', { text: 'Name' }), h('th', { text: 'Prefix' }), h('th', { text: 'Created by' }),
            h('th', { text: 'Created' }), h('th', { text: 'Last used' }), h('th', { text: 'Expires' }),
            h('th', { text: 'State' }), h('th', { class: 'actions' }),
          )),
          h('tbody', {}, tokens.map((token) => h('tr', {},
            h('td', { style: { fontWeight: '550' }, text: token.name }),
            h('td', {}, h('span', { class: 'pill', text: `dm_${token.prefix}…` })),
            h('td', { class: 'small' }, creatorCell(token)),
            h('td', { class: 'small faint nowrap', title: when(token.created_at), text: ago(token.created_at) }),
            h('td', { class: 'small faint nowrap', title: token.last_used_at ? when(token.last_used_at) : '', text: token.last_used_at ? ago(token.last_used_at) : 'never' }),
            h('td', { class: 'small faint', text: token.expires_at ? when(token.expires_at) : 'never' }),
            h('td', {}, token.revoked
              ? h('span', { class: 'badge bad', text: 'revoked' })
              : token.expires_at && token.expires_at * 1000 < Date.now()
                ? h('span', { class: 'badge warn', text: 'expired' })
                : h('span', { class: 'badge ok', text: 'active' })),
            h('td', { class: 'actions' }, h('button', {
              class: 'btn sm icon danger', title: token.revoked ? 'Delete record' : 'Revoke',
              onClick: async () => {
                const ok = await confirmDialog({
                  title: token.revoked ? `Delete ${token.name}?` : `Revoke ${token.name}?`,
                  message: token.revoked
                    ? 'The record is removed from the list.'
                    : 'Anything using this token stops working immediately.',
                  confirmLabel: token.revoked ? 'Delete' : 'Revoke',
                });
                if (!ok) return;
                try {
                  await Account.deleteToken(token.id, token.revoked);
                  toast(token.revoked ? 'Token deleted' : 'Token revoked', 'ok');
                  load();
                } catch (err) { toastError(err); }
              },
            }, icon(token.revoked ? 'trash' : 'x'))),
          ))),
        )))
        : emptyState('No API tokens', 'Create one to drive DocMan from a script or another agent.'),
    ]);
  }

  load();
}

// ---------- preferences ----------

function preferencesPanel(host, ctx) {
  add(host, [spinner()]);
  System.settings().then((data) => {
    clear(host);
    const settings = data.settings || {};

    const theme = h('select', {}, ...[['system', 'Match the system'], ['dark', 'Dark'], ['light', 'Light']]
      .map(([value, label]) => h('option', { value, text: label, selected: (settings.theme || 'system') === value })));
    const logTail = h('input', { type: 'number', min: '50', max: '10000', step: '50', value: settings.log_tail || '500' });
    const interval = h('input', { type: 'number', min: '500', max: '30000', step: '500', value: settings.stats_interval_ms || '2000' });
    const rpID = h('input', { type: 'text', class: 'mono', value: settings.rp_id || '', placeholder: location.hostname });

    theme.addEventListener('change', () => {
      const root = document.documentElement;
      if (theme.value === 'system') root.removeAttribute('data-theme');
      else root.setAttribute('data-theme', theme.value);
      try { localStorage.setItem('docman.theme', theme.value); } catch { /* ignore */ }
    });

    const save = h('button', { class: 'btn primary', onClick: async () => {
      save.disabled = true;
      try {
        await System.saveSettings({
          theme: theme.value,
          log_tail: logTail.value,
          stats_interval_ms: interval.value,
          rp_id: rpID.value.trim(),
        });
        toast('Preferences saved', 'ok');
        if (ctx.state.settings) ctx.state.settings.settings.theme = theme.value;
      } catch (err) { toastError(err); }
      save.disabled = false;
    } }, icon('check'), 'Save preferences');

    add(host, [
      panel('Appearance and streams', 'settings', h('div', { class: 'grid c2' },
        field('Theme', theme),
        field('Log lines to load', logTail, 'How much history the log view asks for when it opens.'),
        field('Statistics interval (ms)', interval, 'How often live figures refresh. Lower is smoother, higher is lighter.'),
      )),
      panel('Passkey identity', 'key', h('div', { class: 'col', style: { maxWidth: '520px' } },
        h('p', { class: 'muted', style: { margin: 0 } },
          'Passkeys are bound to a hostname, so DocMan must always be reached by the same name. '
          + 'Leave this empty to use whatever host the browser asked for, or set it explicitly when DocMan '
          + 'sits behind a reverse proxy.'),
        field('Relying party ID', rpID, 'A hostname, never an IP address. Changing it invalidates existing passkeys.'),
        settings.rp_id ? notice('Existing passkeys stop working if this value changes.', 'warn') : null,
      )),
      panel('Managed network', 'network', h('dl', { class: 'kv' },
        h('dt', { text: 'Name' }), h('dd', { class: 'mono', text: data.managed_network_name || '—' }),
        h('dt', { text: 'Subnet' }), h('dd', { class: 'mono', text: data.managed_network_subnet || 'not created yet' }),
      ), 'DocMan chooses this automatically the first time you fix an address'),
      h('div', {}, save),
    ]);
  }).catch((err) => {
    clear(host);
    add(host, [notice(err.message, 'bad')]);
  });
}

// ---------- audit ----------

function auditPanel(host) {
  add(host, [spinner()]);
  System.audit(300).then((data) => {
    clear(host);
    const entries = data.entries || [];
    if (!entries.length) {
      add(host, [emptyState('Nothing recorded yet', 'DocMan logs every change made through the UI or the API here.')]);
      return;
    }
    add(host, [h('div', { class: 'card' }, h('div', { class: 'table-wrap' }, h('table', { class: 'tbl' },
      h('thead', {}, h('tr', {},
        h('th', { text: 'When' }), h('th', { text: 'Who' }), h('th', { text: 'Action' }),
        h('th', { text: 'Target' }), h('th', { text: 'Detail' }), h('th', { text: '' }),
      )),
      h('tbody', {}, entries.map((entry) => h('tr', {},
        h('td', { class: 'small faint nowrap', title: when(entry.at), text: ago(entry.at) }),
        h('td', { class: 'small nowrap' }, actorLabel(entry.actor)),
        h('td', {}, h('span', { class: 'pill', text: entry.action })),
        h('td', { class: 'small mono truncate', style: { maxWidth: '200px' }, text: entry.target || '—' }),
        h('td', { class: 'small faint truncate', style: { maxWidth: '320px' }, title: entry.detail, text: entry.detail || '' }),
        h('td', {}, entry.ok
          ? h('span', { class: 'badge ok', text: 'ok' })
          : h('span', { class: 'badge bad', text: 'failed' })),
      ))),
    )))]);
  }).catch((err) => {
    clear(host);
    add(host, [notice(err.message, 'bad')]);
  });
}
