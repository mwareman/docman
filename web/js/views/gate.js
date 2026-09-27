// First-run setup and sign-in.

import { h, add, clear, icon, logoMark, toast, field } from '../ui.js';
import { State, post } from '../api.js';
import * as passkeys from '../passkeys.js';

export function renderGate(app, status, onDone) {
  clear(app);
  app.className = '';
  const card = h('div', { class: 'gate-card' });
  add(app, [h('div', { class: 'gate' }, card)]);
  if (!status.setup_complete) renderSetup(card, status, onDone);
  else renderSignIn(card, status, onDone);
}

function brand(title, subtitle) {
  return [
    h('div', { class: 'gate-brand' }, logoMark(), h('div', {},
      h('div', { class: 'gate-title', text: title }),
      subtitle ? h('div', { class: 'small faint', text: subtitle }) : null,
    )),
  ];
}

// ---------- first run ----------

function renderSetup(card, status, onDone) {
  let step = 1;
  let setupKey = '';

  const draw = () => {
    clear(card);
    card.className = 'gate-card wide';
    add(card, [
      brand('Set up DocMan', 'One-time configuration'),
      h('div', { class: 'gate-steps' },
        h('i', { class: step >= 1 ? 'on' : '' }),
        h('i', { class: step >= 2 ? 'on' : '' }),
      ),
      step === 1 ? stepKey() : stepAdmin(),
    ]);
  };

  const stepKey = () => {
    const input = h('input', {
      type: 'text', class: 'key-input', placeholder: 'XXXXX-XXXXX-XXXXX-XXXXX',
      autocomplete: 'off', spellcheck: false, maxLength: 40, 'aria-label': 'Setup key',
    });
    const err = h('div', { class: 'notice bad hidden' });
    const submit = h('button', { class: 'btn primary', type: 'submit', text: 'Continue' });

    const form = h('form', {
      onSubmit: async (event) => {
        event.preventDefault();
        err.className = 'notice bad hidden';
        submit.disabled = true;
        try {
          await State.bootstrapVerify(input.value);
          setupKey = input.value;
          step = 2;
          draw();
        } catch (error) {
          clear(err);
          add(err, [icon('alert'), h('div', { text: error.message })]);
          err.className = 'notice bad';
          input.select();
        } finally {
          submit.disabled = false;
        }
      },
    },
      h('p', { class: 'gate-sub' },
        'DocMan printed a setup key to its container log when it started. Find it with ',
        h('code', { class: 'pill', text: 'docker logs docman' }),
        ' and enter it here.'),
      field('Setup key', input, 'Case and dashes do not matter.'),
      err,
      submit,
    );
    setTimeout(() => input.focus(), 60);
    return form;
  };

  const stepAdmin = () => {
    const username = h('input', { type: 'text', autocomplete: 'username', placeholder: 'admin', spellcheck: false });
    const password = h('input', { type: 'password', autocomplete: 'new-password', placeholder: 'At least 12 characters' });
    const confirm = h('input', { type: 'password', autocomplete: 'new-password' });
    const meter = h('div', { class: 'pwmeter' }, h('i'), h('i'), h('i'));
    const err = h('div', { class: 'notice bad hidden' });
    const submit = h('button', { class: 'btn primary', type: 'submit', text: 'Create administrator' });

    password.addEventListener('input', () => {
      const score = strength(password.value);
      [...meter.children].forEach((bar, index) => {
        bar.className = index < score ? `on${score}` : '';
      });
    });

    const form = h('form', {
      onSubmit: async (event) => {
        event.preventDefault();
        err.className = 'notice bad hidden';
        if (password.value !== confirm.value) {
          clear(err);
          add(err, [icon('alert'), h('div', { text: 'The two passwords do not match.' })]);
          err.className = 'notice bad';
          return;
        }
        submit.disabled = true;
        try {
          await State.bootstrapAdmin(setupKey, username.value.trim(), password.value);
          toast('DocMan is ready. Add two passkeys to switch password sign-in off.', 'ok', 9000);
          location.hash = '#/settings/passkeys';
          onDone();
        } catch (error) {
          clear(err);
          add(err, [icon('alert'), h('div', { text: error.message })]);
          err.className = 'notice bad';
          submit.disabled = false;
        }
      },
    },
      h('p', { class: 'gate-sub', text: 'This first account manages every container on this host, and can add more users later. Once an account registers two passkeys, its password sign-in switches off automatically.' }),
      field('Username', username),
      field('Password', password),
      meter,
      field('Confirm password', confirm),
      err,
      submit,
    );
    setTimeout(() => username.focus(), 60);
    return form;
  };

  draw();
}

function strength(value) {
  if (value.length < 12) return 1;
  let classes = 0;
  if (/[a-z]/.test(value)) classes++;
  if (/[A-Z]/.test(value)) classes++;
  if (/[0-9]/.test(value)) classes++;
  if (/[^A-Za-z0-9]/.test(value)) classes++;
  if (value.length >= 16 && classes >= 3) return 3;
  return classes >= 2 ? 2 : 1;
}

// ---------- sign in ----------

function renderSignIn(card, status, onDone) {
  clear(card);
  const err = h('div', { class: 'notice bad hidden' });
  const showError = (message) => {
    clear(err);
    add(err, [icon('alert'), h('div', { text: message })]);
    err.className = 'notice bad';
  };

  const passkeyButton = h('button', {
    class: `btn ${status.password_enabled ? '' : 'primary'}`,
    type: 'button',
    onClick: async () => {
      passkeyButton.disabled = true;
      try {
        const begin = await post('/api/auth/passkey/login/begin');
        const assertion = await passkeys.authenticate(begin.options);
        await post('/api/auth/passkey/login/finish', {
          challenge_id: begin.challenge_id,
          response: assertion,
        });
        onDone();
      } catch (error) {
        showError(error.name === 'ApiError' ? error.message : passkeys.describeError(error));
        passkeyButton.disabled = false;
      }
    },
  }, icon('key'), 'Sign in with a passkey');

  const nodes = [brand('DocMan', location.host)];

  if (status.rp_problem && !status.password_enabled) {
    nodes.push(h('div', { class: 'notice bad' }, icon('alert'), h('div', {},
      h('div', { text: 'Passkey sign-in is unavailable here.' }),
      h('div', { class: 'small faint', text: status.rp_problem }),
    )));
  }

  if (status.password_enabled) {
    const username = h('input', { type: 'text', autocomplete: 'username', placeholder: 'Username', spellcheck: false });
    const password = h('input', { type: 'password', autocomplete: 'current-password', placeholder: 'Password' });
    const code = h('input', {
      type: 'text', inputmode: 'numeric', autocomplete: 'one-time-code', maxLength: 7,
      placeholder: '123456', class: 'mono', style: { letterSpacing: '0.18em', textAlign: 'center' },
    });
    const codeField = field('Authenticator code', code, 'If your account uses an authenticator app, the six digit code it shows.');
    if (!status.totp_enabled) codeField.classList.add('hidden');
    const submit = h('button', { class: 'btn primary', type: 'submit', text: 'Sign in' });

    const revealCode = () => {
      codeField.classList.remove('hidden');
      code.focus();
    };

    const form = h('form', {
      onSubmit: async (event) => {
        event.preventDefault();
        err.className = 'notice bad hidden';
        submit.disabled = true;
        try {
          await State.login(username.value.trim(), password.value, code.value);
          onDone();
        } catch (error) {
          if (error.payload && error.payload.totp_required) revealCode();
          showError(error.message + (error.hint ? ` — ${error.hint}` : ''));
          code.value = '';
          if (!(error.payload && error.payload.totp_required)) password.value = '';
          submit.disabled = false;
        }
      },
    }, field('Username', username), field('Password', password), codeField, submit);
    nodes.push(form);
    if (status.passkey_ready && passkeys.supported()) {
      nodes.push(h('div', { class: 'or', text: 'or' }), passkeyButton);
    }
  } else {
    nodes.push(h('p', { class: 'gate-sub', text: 'Every account here signs in with passkeys, so password sign-in is switched off.' }));
    if (passkeys.supported()) nodes.push(passkeyButton);
    else nodes.push(h('div', { class: 'notice bad' }, icon('alert'), h('div', { text: 'This browser does not support passkeys, and password sign-in is disabled.' })));
  }

  nodes.push(err);
  if (!status.secure_context) {
    nodes.push(h('div', { class: 'notice warn' }, icon('alert'), h('div', {},
      h('div', { text: 'This page is not a secure context.' }),
      h('div', { class: 'small faint', text: 'Browsers only allow passkeys over HTTPS or on localhost.' }),
    )));
  }
  add(card, nodes);

  if (!status.password_enabled && passkeys.supported()) {
    setTimeout(() => passkeyButton.focus(), 80);
  }
}
