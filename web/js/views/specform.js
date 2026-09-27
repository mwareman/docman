// A reusable editor for a container spec, shared by the deploy wizard and the
// per-container configuration tab.

import { h, add, clear, icon, field } from '../ui.js';
import { Networks, Containers, Volumes } from '../api.js';
import {
  CAPABILITIES, effectiveCaps, capsToLists, sameSet, unknownCaps, isDefaultCap, DOCKER_DEFAULTS,
} from './capabilities.js';

/** Split a shell-style command into tokens, honouring quotes. */
export function tokenize(input) {
  const tokens = [];
  let current = '';
  let quote = '';
  let has = false;
  for (const ch of input) {
    if (quote) {
      if (ch === quote) quote = '';
      else current += ch;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      has = true;
      continue;
    }
    if (ch === ' ' || ch === '\t' || ch === '\n') {
      if (current || has) tokens.push(current);
      current = '';
      has = false;
      continue;
    }
    current += ch;
  }
  if (current || has) tokens.push(current);
  return tokens;
}

/** Render tokens back to a shell-style string, quoting where needed. */
export function untokenize(tokens) {
  return (tokens || []).map((token) => {
    if (token === '') return "''";
    return /[\s"'$`\\*?[\]{}();&|<>#]/.test(token) ? `'${token.replace(/'/g, "'\\''")}'` : token;
  }).join(' ');
}

const RESTART_POLICIES = [
  ['unless-stopped', 'Unless stopped — start with docker, respect a manual stop'],
  ['always', 'Always — restart even after a manual stop'],
  ['on-failure', 'On failure — restart only after a non-zero exit'],
  ['no', 'Never'],
];

/**
 * Build the spec editor.
 * @param {object} spec the spec to edit (mutated only via read())
 * @param {{lockName?: boolean, lockImage?: boolean, envNotes?: Record<string,string>, secrets?: Set<string>, volumeSuggestions?: string[]}} opts
 */
export function specForm(spec, opts = {}) {
  const state = JSON.parse(JSON.stringify(spec || {}));
  state.env = state.env || [];
  state.ports = state.ports || [];
  state.mounts = state.mounts || [];
  state.labels = state.labels || {};

  const nameInput = h('input', { type: 'text', value: state.name || '', disabled: !!opts.lockName, spellcheck: false });
  const imageInput = h('input', { type: 'text', class: 'mono', value: state.image || '', disabled: !!opts.lockImage, spellcheck: false });
  const restartSelect = h('select', {},
    ...RESTART_POLICIES.map(([value, label]) =>
      h('option', { value, text: label, selected: (state.restart_policy || 'unless-stopped') === value })));

  const entrypointInput = h('input', { type: 'text', class: 'mono', value: untokenize(state.entrypoint), spellcheck: false, placeholder: 'leave empty to use the image default' });
  const cmdInput = h('input', { type: 'text', class: 'mono', value: untokenize(state.cmd), spellcheck: false, placeholder: 'leave empty to use the image default' });

  const userInput = h('input', { type: 'text', value: state.user || '', placeholder: 'root, 1000:1000, appuser', spellcheck: false });
  const workdirInput = h('input', { type: 'text', class: 'mono', value: state.working_dir || '', spellcheck: false });
  const hostnameInput = h('input', { type: 'text', value: state.hostname || '', spellcheck: false });
  const network = networkPicker(state.network_mode || 'bridge', opts);

  const ttyCheck = check('Allocate a TTY', state.tty);
  const stdinCheck = check('Keep stdin open', state.open_stdin);
  const privilegedCheck = check('Privileged (full access to the host)', state.privileged);
  const readonlyCheck = check('Read-only root filesystem', state.read_only_rootfs);
  const autoRemoveCheck = check('Remove automatically when it exits', state.auto_remove);

  const memoryInput = h('input', { type: 'number', min: '0', step: '16', value: state.memory ? Math.round(state.memory / (1024 * 1024)) : '', placeholder: 'unlimited' });
  const cpusInput = h('input', { type: 'number', min: '0', step: '0.1', value: state.nano_cpus ? (state.nano_cpus / 1e9).toString() : '', placeholder: 'unlimited' });
  const pidsInput = h('input', { type: 'number', min: '0', step: '1', value: state.pids_limit || '', placeholder: 'unlimited' });

  const caps = capabilityPicker(state.cap_add, state.cap_drop, privilegedCheck.input);
  const dnsInput = h('input', { type: 'text', class: 'mono', value: (state.dns || []).join(', '), placeholder: '1.1.1.1, 9.9.9.9' });
  const hostsInput = h('input', { type: 'text', class: 'mono', value: (state.extra_hosts || []).join(', '), placeholder: 'db:10.0.0.5' });
  const labelsInput = h('textarea', {
    value: Object.entries(state.labels).map(([k, v]) => `${k}=${v}`).join('\n'),
    placeholder: 'one key=value per line',
    rows: 3,
  });

  // ---------- environment ----------

  const envHost = h('div', {});
  const envRows = [];

  function addEnvRow(key = '', value = '', meta = {}) {
    const keyInput = h('input', { type: 'text', class: 'mono', value: key, placeholder: 'KEY', spellcheck: false });
    const valueInput = h('input', {
      type: meta.secret ? 'password' : 'text',
      class: 'mono', value, placeholder: meta.required ? 'required' : 'value', spellcheck: false,
    });
    const reveal = meta.secret
      ? h('button', {
        class: 'btn sm ghost icon', type: 'button', title: 'Show value',
        onClick: () => { valueInput.type = valueInput.type === 'password' ? 'text' : 'password'; },
      }, icon('key'))
      : null;
    const row = h('div', { class: 'editrow env' },
      keyInput,
      h('div', { class: 'row' }, valueInput, reveal),
      h('button', {
        class: 'btn sm ghost icon danger', type: 'button', title: 'Remove',
        onClick: () => { row.remove(); envRows.splice(envRows.indexOf(entry), 1); },
      }, icon('x')),
      meta.note ? h('div', { class: 'hint', text: meta.note }) : null,
    );
    const entry = { keyInput, valueInput, row };
    envRows.push(entry);
    envHost.append(row);
    return entry;
  }

  for (const line of state.env) {
    const eq = line.indexOf('=');
    const key = eq >= 0 ? line.slice(0, eq) : line;
    const value = eq >= 0 ? line.slice(eq + 1) : '';
    const meta = {
      note: (opts.envNotes || {})[key],
      secret: opts.secrets ? opts.secrets.has(key) : /PASS|SECRET|TOKEN|KEY/i.test(key),
      required: (opts.required || new Set()).has(key),
    };
    addEnvRow(key, value, meta);
  }

  const envBulk = h('textarea', { placeholder: 'Paste a .env block here, then press Import', rows: 3, class: 'hidden' });
  const envSection = section('Environment', [
    envHost,
    h('div', { class: 'row wrap' },
      h('button', { class: 'btn sm', type: 'button', onClick: () => addEnvRow() }, icon('plus'), 'Add variable'),
      h('button', {
        class: 'btn sm ghost', type: 'button',
        onClick: () => {
          envBulk.classList.toggle('hidden');
          if (!envBulk.classList.contains('hidden')) envBulk.focus();
        },
      }, 'Paste .env'),
      h('button', {
        class: 'btn sm', type: 'button',
        onClick: () => {
          for (const raw of envBulk.value.split('\n')) {
            const line = raw.trim();
            if (!line || line.startsWith('#')) continue;
            const eq = line.indexOf('=');
            if (eq <= 0) continue;
            addEnvRow(line.slice(0, eq).trim(), line.slice(eq + 1).trim().replace(/^["']|["']$/g, ''));
          }
          envBulk.value = '';
          envBulk.classList.add('hidden');
        },
      }, 'Import'),
    ),
    envBulk,
  ]);

  // ---------- ports ----------

  const portHost = h('div', {});
  const portRows = [];

  function addPortRow(port = {}) {
    const container = h('input', { type: 'number', min: '1', max: '65535', value: port.container_port || '', placeholder: '80' });
    const host = h('input', { type: 'text', value: port.host_port || '', placeholder: 'auto' });
    const proto = h('select', {},
      ...['tcp', 'udp', 'sctp'].map((p) => h('option', { value: p, text: p, selected: (port.protocol || 'tcp') === p })));
    const bindIP = h('input', { type: 'text', class: 'mono', value: port.host_ip || '', placeholder: 'all interfaces' });
    const row = h('div', { class: 'editrow port' },
      container, host, proto, bindIP,
      h('button', {
        class: 'btn sm ghost icon danger', type: 'button', title: 'Remove',
        onClick: () => { row.remove(); portRows.splice(portRows.indexOf(entry), 1); },
      }, icon('x')),
      port.note ? h('div', { class: 'hint', text: port.note }) : null,
    );
    const entry = { container, host, proto, bindIP, row };
    portRows.push(entry);
    portHost.append(row);
    return entry;
  }

  for (const port of state.ports) addPortRow(port);

  const portSection = section('Published ports', [
    h('div', { class: 'editrow port small faint' },
      h('span', { text: 'Container' }), h('span', { text: 'Host' }),
      h('span', { text: 'Protocol' }), h('span', { text: 'Bind address' }), h('span')),
    portHost,
    h('button', { class: 'btn sm', type: 'button', onClick: () => addPortRow() }, icon('plus'), 'Publish a port'),
  ]);

  // ---------- mounts ----------

  const mountHost = h('div', {});
  const mountRows = [];
  // The host's volumes, fetched once for every storage row.
  const volumesReady = Volumes.list().then((data) => data.volumes || []).catch(() => []);

  function addMountRow(mount = {}) {
    const type = h('select', {},
      ...[['volume', 'Volume'], ['bind', 'Host path'], ['tmpfs', 'tmpfs']].map(([value, label]) =>
        h('option', { value, text: label, selected: (mount.type || 'volume') === value })));
    const source = sourcePicker(mount.type || 'volume', mount.source || '', volumesReady);
    const target = h('input', { type: 'text', class: 'mono', value: mount.target || '', placeholder: '/data', spellcheck: false });
    const readonly = h('label', { class: 'check', title: 'Read only' },
      h('input', { type: 'checkbox', checked: !!mount.read_only }), 'ro');
    type.addEventListener('change', () => source.setKind(type.value));
    const row = h('div', { class: 'editrow mount' },
      type, source.node, target, readonly,
      h('button', {
        class: 'btn sm ghost icon danger', type: 'button', title: 'Remove',
        onClick: () => { row.remove(); mountRows.splice(mountRows.indexOf(entry), 1); },
      }, icon('x')),
      mount.note ? h('div', { class: 'hint', text: mount.note }) : null,
    );
    const entry = { type, source, target, readonly, row };
    mountRows.push(entry);
    mountHost.append(row);
    return entry;
  }

  for (const mount of state.mounts) addMountRow(mount);

  const mountSection = section('Storage', [
    h('div', { class: 'editrow mount small faint' },
      h('span', { text: 'Kind' }), h('span', { text: 'Source' }),
      h('span', { text: 'Container path' }), h('span'), h('span')),
    mountHost,
    h('button', { class: 'btn sm', type: 'button', onClick: () => addMountRow() }, icon('plus'), 'Add storage'),
  ]);

  // ---------- assembly ----------

  const advanced = h('details', { class: 'card', style: { padding: '0' } },
    h('summary', {
      style: { padding: '12px 16px', cursor: 'pointer', fontWeight: '580', fontSize: '13px' },
    }, 'Advanced options'),
    h('div', { class: 'card-body', style: { borderTop: '1px solid var(--border)' } },
      h('div', { class: 'grid c2' },
        field('Entrypoint', entrypointInput, 'Overrides the image entrypoint.'),
        field('Command', cmdInput, 'Arguments passed to the entrypoint.'),
        field('User', userInput),
        field('Working directory', workdirInput),
        field('Hostname', hostnameInput),
        field('Network', network.node, network.hint),
        field('Memory limit (MB)', memoryInput),
        field('CPU limit (cores)', cpusInput),
        field('Process limit', pidsInput),
        field('DNS servers', dnsInput),
        field('Extra hosts', hostsInput),
        field('Labels', labelsInput),
      ),
      h('div', { class: 'col', style: { marginTop: '12px' } },
        ttyCheck.node, stdinCheck.node, autoRemoveCheck.node, readonlyCheck.node, privilegedCheck.node),
      caps.node,
    ),
  );

  const node = h('div', { class: 'col', style: { gap: '16px' } },
    h('div', { class: 'grid c2' },
      field('Name', nameInput, opts.lockName ? 'Renaming happens on the container’s own page.' : 'Letters, digits, dot, dash and underscore.'),
      field('Image', imageInput),
      field('Restart policy', restartSelect),
    ),
    envSection,
    portSection,
    mountSection,
    advanced,
  );

  function read() {
    const out = { ...state };
    out.name = nameInput.value.trim();
    out.image = imageInput.value.trim();
    out.restart_policy = restartSelect.value;
    out.restart_retries = restartSelect.value === 'on-failure' ? (state.restart_retries || 5) : 0;
    out.entrypoint = tokenize(entrypointInput.value);
    out.cmd = tokenize(cmdInput.value);
    out.user = userInput.value.trim();
    out.working_dir = workdirInput.value.trim();
    out.hostname = hostnameInput.value.trim();
    out.network_mode = network.value() || 'bridge';
    out.tty = ttyCheck.input.checked;
    out.open_stdin = stdinCheck.input.checked;
    out.privileged = privilegedCheck.input.checked;
    out.read_only_rootfs = readonlyCheck.input.checked;
    out.auto_remove = autoRemoveCheck.input.checked;
    out.memory = memoryInput.value ? Math.round(Number(memoryInput.value) * 1024 * 1024) : 0;
    out.memory_swap = 0;
    out.nano_cpus = cpusInput.value ? Math.round(Number(cpusInput.value) * 1e9) : 0;
    out.pids_limit = pidsInput.value ? Number(pidsInput.value) : 0;
    Object.assign(out, caps.lists());
    out.dns = splitList(dnsInput.value);
    out.extra_hosts = splitList(hostsInput.value);

    out.labels = {};
    for (const raw of labelsInput.value.split('\n')) {
      const line = raw.trim();
      if (!line) continue;
      const eq = line.indexOf('=');
      if (eq <= 0) continue;
      out.labels[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    }

    out.env = [];
    for (const entry of envRows) {
      const key = entry.keyInput.value.trim();
      if (!key) continue;
      out.env.push(`${key}=${entry.valueInput.value}`);
    }

    out.ports = [];
    for (const entry of portRows) {
      const containerPort = Number(entry.container.value);
      if (!containerPort) continue;
      out.ports.push({
        container_port: containerPort,
        protocol: entry.proto.value,
        host_port: entry.host.value.trim(),
        host_ip: entry.bindIP.value.trim(),
      });
    }

    out.mounts = [];
    for (const entry of mountRows) {
      const target = entry.target.value.trim();
      if (!target) continue;
      out.mounts.push({
        type: entry.type.value,
        source: entry.type.value === 'tmpfs' ? '' : entry.source.value(),
        target,
        read_only: entry.readonly.querySelector('input').checked,
      });
    }
    return out;
  }

  /** Validate locally so obvious mistakes never reach the API. */
  function validate() {
    const problems = [];
    const spec = read();
    for (const entry of mountRows) {
      const problem = entry.source.problem();
      if (problem) problems.push(problem);
    }
    if (!spec.name) problems.push('A container name is required.');
    if (!spec.image) problems.push('An image is required.');
    const targets = new Set();
    for (const mount of spec.mounts) {
      if (!mount.target.startsWith('/')) problems.push(`Mount path ${mount.target} must be absolute.`);
      if (targets.has(mount.target)) problems.push(`Two mounts both target ${mount.target}.`);
      targets.add(mount.target);
      if (mount.type === 'bind' && !mount.source.startsWith('/')) {
        problems.push(`Host path for ${mount.target} must be absolute.`);
      }
      if (mount.type === 'volume' && !mount.source) {
        // An anonymous volume is legitimate, just worth knowing about.
      }
    }
    const hostPorts = new Set();
    for (const port of spec.ports) {
      if (port.host_port) {
        const key = `${port.host_port}/${port.protocol}`;
        if (hostPorts.has(key)) problems.push(`Host port ${port.host_port} is used twice.`);
        hostPorts.add(key);
      }
    }
    return { spec, problems };
  }

  return { node, read, validate, addEnvRow, addPortRow, addMountRow };
}

function check(label, checked) {
  const input = h('input', { type: 'checkbox', checked: !!checked });
  return { node: h('label', { class: 'check' }, input, label), input };
}

function section(title, children) {
  return h('div', {},
    h('div', { class: 'section-title', text: title }),
    h('div', { class: 'card' }, h('div', { class: 'card-body' }, h('div', { class: 'col' }, children))),
  );
}

function splitList(value) {
  return value.split(',').map((part) => part.trim()).filter(Boolean);
}

// ---------- storage source ----------

const NEW_VOLUME = '\u0000new';

/**
 * The Source of one storage row. For a volume it is a choice of the volumes
 * on this host, plus an anonymous volume and a new one by name; for a host
 * path it is a path; tmpfs has none. A volume named in the settings that does
 * not exist yet, as the deploy wizard suggests, is kept and created on save.
 */
function sourcePicker(kind, initial, volumesReady) {
  const select = h('select', { class: 'mono' });
  const text = h('input', { type: 'text', class: 'mono', spellcheck: false });
  const node = h('div', { class: 'source-pick' }, select, text);
  let volumeNames = [];
  let current = kind;

  const fillVolumes = (chosen) => {
    clear(select);
    const known = volumeNames.includes(chosen);
    add(select, [
      h('option', { value: '', text: 'Anonymous volume — made just for this container', selected: chosen === '' }),
      volumeNames.length
        ? h('optgroup', { label: 'Volumes on this host' },
          volumeNames.map((v) => h('option', { value: v, text: v, selected: v === chosen })))
        : null,
      chosen && !known
        ? h('optgroup', { label: 'Not created yet' },
          h('option', { value: chosen, text: `${chosen} — created when saved`, selected: true }))
        : null,
      h('option', { value: NEW_VOLUME, text: 'New volume…' }),
    ]);
  };

  const show = () => {
    const isVolume = current === 'volume';
    const naming = isVolume && select.value === NEW_VOLUME;
    select.classList.toggle('hidden', !isVolume);
    text.classList.toggle('hidden', !(current === 'bind' || naming));
    text.placeholder = naming ? 'new volume name' : '/srv/data';
    node.classList.toggle('muted', current === 'tmpfs');
  };

  select.addEventListener('change', () => {
    if (select.value === NEW_VOLUME) {
      text.value = '';
      show();
      text.focus();
    } else {
      show();
    }
  });

  const setKind = (next) => {
    const was = current;
    current = next;
    if (next === 'bind' && was !== 'bind') text.value = '';
    if (next === 'volume' && select.value === NEW_VOLUME) select.value = '';
    show();
  };

  if (kind === 'bind') text.value = initial;
  fillVolumes(kind === 'volume' ? initial : '');
  show();
  volumesReady.then((volumes) => {
    volumeNames = volumes.map((v) => v.Name || v.name).filter(Boolean).sort();
    const chosen = current === 'volume' && select.value !== NEW_VOLUME ? select.value : (kind === 'volume' ? initial : '');
    const naming = select.value === NEW_VOLUME ? text.value : null;
    fillVolumes(chosen);
    if (naming !== null) { select.value = NEW_VOLUME; text.value = naming; }
    show();
  });

  return {
    node,
    setKind,
    problem: () => {
      if (current !== 'volume' || select.value !== NEW_VOLUME) return '';
      const name = text.value.trim();
      if (!name) return 'Enter a name for the new volume, or choose an existing one.';
      if (!/^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(name)) {
        return `Volume name ${name} may contain only letters, digits, dot, dash and underscore.`;
      }
      return '';
    },
    value: () => {
      if (current === 'tmpfs') return '';
      if (current === 'bind') return text.value.trim();
      return select.value === NEW_VOLUME ? text.value.trim() : select.value;
    },
  };
}

// ---------- network ----------

/**
 * A choice of the networks that exist on this host, instead of free text.
 * Whatever the container uses today is always offered, even when it is not
 * in the list any more, so opening and saving the form never changes it.
 */
function networkPicker(current, opts) {
  const select = h('select', { disabled: !!opts.pinnedIP },
    h('option', { value: current, text: current, selected: true }));
  const hint = opts.pinnedIP
    ? 'This container has a fixed address, so DocMan keeps it on its own network. Release the address to choose another.'
    : 'Only networks that exist on this host can be chosen.';

  Promise.all([Networks.list(), Containers.list()]).then(([netData, ctrData]) => {
    const networks = netData.networks || [];
    const containers = (ctrData.containers || []).filter((c) => c.name !== opts.selfName);
    let matched = false;
    const option = (value, text, matches) => {
      const isCurrent = matches || value === current;
      if (isCurrent) matched = true;
      return h('option', { value: isCurrent ? current : value, text, selected: isCurrent });
    };

    const bridge = networks.find((n) => n.Name === 'bridge');
    const docker = [option('bridge', 'bridge — Docker’s default network',
      current === 'default' || (bridge && idMatches(bridge.Id, current)))];
    for (const n of networks) {
      if (['bridge', 'host', 'none'].includes(n.Name)) continue;
      const details = [n.Driver, n.subnet, n.managed ? 'DocMan fixed addresses' : '', n.Internal ? 'internal' : '']
        .filter(Boolean).join(', ');
      docker.push(option(n.Name, details ? `${n.Name} — ${details}` : n.Name, idMatches(n.Id, current)));
    }
    const special = [
      option('host', 'host — share the host’s network directly'),
      option('none', 'none — no networking'),
    ];
    const shared = containers.map((c) => option(`container:${c.name}`, `${c.name}`,
      current === `container:${c.name}` || idMatches(c.id, current.replace(/^container:/, ''))));

    clear(select);
    add(select, [
      h('optgroup', { label: 'Docker networks' }, docker),
      h('optgroup', { label: 'Special' }, special),
      shared.length ? h('optgroup', { label: 'Share another container’s network' }, shared) : null,
    ]);
    if (!matched) {
      const label = current.startsWith('container:')
        ? `${current.slice(10, 22)}… — a container that no longer exists`
        : `${current} — no longer exists on this host`;
      select.prepend(h('optgroup', { label: 'Current setting' },
        h('option', { value: current, text: label, selected: true })));
    }
  }).catch(() => { /* keep the current value as the only choice */ });

  return { node: select, hint, value: () => select.value };
}

function idMatches(id, value) {
  return !!id && !!value && value.length >= 12 && id.startsWith(value);
}

// ---------- capabilities ----------

/**
 * A checkbox per Linux capability: ticked means the container has it. It
 * starts from what the container has today, worked out from cap_add and
 * cap_drop, and gives back the lists unchanged unless a box was changed.
 */
function capabilityPicker(capAdd, capDrop, privilegedInput) {
  const original = effectiveCaps(capAdd, capDrop);
  const extras = unknownCaps(capAdd, capDrop).map((name) => ({
    name, description: 'DocMan has no description for this capability. It is kept as it was.', risk: false,
  }));
  const all = [...CAPABILITIES, ...extras];
  const boxes = new Map();

  const item = (cap) => {
    const input = h('input', { type: 'checkbox', checked: original.has(cap.name) });
    input.addEventListener('change', update);
    boxes.set(cap.name, input);
    const tip = `CAP_${cap.name}: ${cap.description}${cap.risk ? ' Weakens the isolation between the container and the host.' : ''}`
      + (isDefaultCap(cap.name) ? ' Docker grants it by default.' : ' Off unless added.');
    return h('label', { class: `cap${cap.risk ? ' risk' : ''}`, title: tip },
      input, h('span', { class: 'mono', text: cap.name }),
      cap.risk ? h('span', { class: 'cap-risk', 'aria-label': 'weakens isolation', text: '!' }) : null);
  };

  const summary = h('span', { class: 'small faint' });
  const privilegedNote = h('div', { class: 'small', style: { color: 'var(--warn)' },
    text: 'Privileged is on, so the container gets every capability and these choices have no effect.' });
  const reset = h('button', {
    class: 'btn sm ghost', type: 'button',
    onClick: () => {
      for (const [name, input] of boxes) input.checked = isDefaultCap(name);
      update();
    },
  }, icon('refresh'), 'Docker defaults');

  const node = h('div', { class: 'col', style: { marginTop: '16px', gap: '8px' } },
    h('div', { class: 'row wrap' },
      h('div', { class: 'section-title', style: { margin: 0 }, text: 'Capabilities' }),
      summary, h('div', { class: 'grow' }), reset),
    h('div', { class: 'small faint', text: 'Ticked capabilities are granted to the container. Hover over one to see what it allows. Items marked ! weaken the isolation between the container and the host.' }),
    privilegedNote,
    h('div', { class: 'small faint', style: { marginTop: '4px' }, text: 'Granted by Docker by default' }),
    h('div', { class: 'caps' }, all.filter((c) => isDefaultCap(c.name)).map(item)),
    h('div', { class: 'small faint', style: { marginTop: '4px' }, text: 'Off unless added' }),
    h('div', { class: 'caps' }, all.filter((c) => !isDefaultCap(c.name)).map(item)),
  );

  function chosen() {
    return new Set([...boxes].filter(([, input]) => input.checked).map(([name]) => name));
  }

  function update() {
    const set = chosen();
    const dropped = DOCKER_DEFAULTS.filter((name) => !set.has(name)).length;
    const added = [...set].filter((name) => !isDefaultCap(name)).length;
    summary.textContent = added || dropped
      ? [added ? `${added} added` : '', dropped ? `${dropped} of Docker’s defaults removed` : ''].filter(Boolean).join(' · ')
      : 'Docker’s defaults';
    const privileged = privilegedInput.checked;
    privilegedNote.classList.toggle('hidden', !privileged);
    node.classList.toggle('caps-off', privileged);
  }
  privilegedInput.addEventListener('change', update);
  update();

  return {
    node,
    lists: () => {
      const set = chosen();
      if (sameSet(set, original)) {
        // Untouched: give back exactly what the container had, ALL included.
        return { cap_add: capAdd || [], cap_drop: capDrop || [] };
      }
      return capsToLists(set);
    },
  };
}

/** Build a <datalist> of volume names so mount sources autocomplete. */
export function volumeDatalist(id, volumes) {
  const list = h('datalist', { id });
  add(list, (volumes || []).map((v) => h('option', { value: v.Name || v.name || v })));
  return list;
}
