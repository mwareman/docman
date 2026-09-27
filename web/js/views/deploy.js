// Deploy wizard: pick or bring an image, review what DocMan worked out from
// it, then save the container stopped or start it straight away.

import {
  h, add, clear, icon, bytes, modal, toast, toastError, field, notice, spinner, debounce,
} from '../ui.js';
import { Images, Volumes, Containers, Registries, stream } from '../api.js';
import { registryDialog } from './registries.js';
import { specForm, volumeDatalist } from './specform.js';
import { uploadPanel } from './uploadpanel.js';

/**
 * Open the wizard.
 * @param {{image?: string, onDone?: Function}} options start on the plan step
 *   when image is given.
 */
export function openDeployWizard({ image = '', onDone } = {}) {
  return modal({
    title: 'New container',
    subtitle: 'DocMan reads the image and fills in what it needs',
    wide: true,
    hideConfirm: true,
    cancelLabel: 'Close',
    extraActions: (finish) => {
      const host = h('div', { class: 'row', style: { gap: '8px' } });
      wizardActions = { host, finish };
      return host;
    },
    render: ({ body, setLocked }) => {
      runWizard(body, image, onDone, setLocked);
      return null;
    },
  });
}

/**
 * Import an image archive without going on to create a container: the usual
 * way to deliver a new version of an image, which containers using its tag
 * then pick up when they are recreated.
 * @param {{onDone?: Function}} options onDone runs after a successful import.
 */
export function openUploadDialog({ onDone } = {}) {
  return modal({
    title: 'Upload an image',
    subtitle: 'Imports the image only; no container is created',
    wide: true,
    hideConfirm: true,
    cancelLabel: 'Close',
    render: ({ body, setLocked }) => {
      add(body, [uploadPanel((first, loaded) => { if (onDone) onDone(loaded); }, {
        setLocked,
        doneText: (tags) => (tags.length
          ? `Containers using ${tags.length === 1 ? 'this tag' : 'these tags'} switch to the new version when you recreate them.`
          : 'The archive carried no tag. Add one from the image list before a container can use it.'),
      })]);
      return null;
    },
  });
}

let wizardActions = null;

function runWizard(body, initialImage, onDone, setLocked) {
  const actions = wizardActions;
  wizardActions = null;
  let volumes = [];
  Volumes.list().then((data) => { volumes = data.volumes || []; }).catch(() => {});

  const setActions = (...nodes) => {
    if (!actions) return;
    clear(actions.host);
    add(actions.host, nodes);
  };

  const showSource = () => {
    clear(body);
    setActions();

    const chosen = h('div', {});
    const tabs = h('div', { class: 'switchbar' });
    const panels = {};

    const select = (key) => {
      [...tabs.children].forEach((b) => b.classList.toggle('on', b.dataset.key === key));
      clear(chosen);
      add(chosen, [panels[key]()]);
    };

    panels.existing = () => existingPanel(showPlan);
    panels.pull = () => pullPanel(showPlan);
    // After a successful upload the wizard moves on to the plan for the image.
    panels.upload = () => uploadPanel(showPlan, { setLocked, moveOn: true });

    add(tabs, [
      h('button', { dataset: { key: 'existing' }, text: 'From an image on this host', onClick: () => select('existing') }),
      h('button', { dataset: { key: 'pull' }, text: 'Pull from a registry', onClick: () => select('pull') }),
      h('button', { dataset: { key: 'upload' }, text: 'Upload an image archive', onClick: () => select('upload') }),
    ]);
    add(body, [tabs, chosen]);
    select('existing');
  };

  async function showPlan(imageRef) {
    clear(body);
    setActions();
    add(body, [spinner()]);
    let plan;
    try {
      plan = await Images.deployPlan(imageRef);
    } catch (err) {
      clear(body);
      add(body, [
        notice(err.message, 'bad'),
        h('button', { class: 'btn', onClick: showSource }, 'Choose a different image'),
      ]);
      return;
    }

    clear(body);
    const secrets = new Set(plan.env.filter((e) => e.secret).map((e) => e.key));
    const required = new Set(plan.env.filter((e) => e.required).map((e) => e.key));
    const envNotes = {};
    for (const entry of plan.env) if (entry.note) envNotes[entry.key] = entry.note;

    const spec = plan.spec;
    // Carry the planner's notes onto the rows the form builds.
    spec.mounts = (spec.mounts || []).map((mount) => {
      const suggestion = plan.mounts.find((m) => m.target === mount.target);
      return suggestion ? { ...mount, note: suggestion.note } : mount;
    });
    spec.ports = (spec.ports || []).map((port) => {
      const suggestion = plan.ports.find((p) => p.container_port === port.container_port);
      return suggestion && suggestion.note ? { ...port, note: suggestion.note } : port;
    });

    const listID = 'docman-volume-list';
    const form = specForm(spec, { secrets, required, envNotes, volumeListID: listID });

    const pinCheck = h('input', { type: 'checkbox' });
    const pinIP = h('input', { type: 'text', class: 'mono', placeholder: 'next free address', disabled: true, style: { maxWidth: '180px' } });
    pinCheck.addEventListener('change', () => { pinIP.disabled = !pinCheck.checked; });

    const problems = h('div', {});
    const summaryChips = h('div', { class: 'chips' },
      h('span', { class: 'badge plain' }, icon('layers'), plan.defaults.image),
      plan.defaults.architecture ? h('span', { class: 'badge plain', text: `${plan.defaults.os}/${plan.defaults.architecture}` }) : null,
      plan.defaults.size ? h('span', { class: 'badge plain', text: bytes(plan.defaults.size) }) : null,
      plan.mounts.length ? h('span', { class: 'badge info', text: `${plan.mounts.length} volume${plan.mounts.length === 1 ? '' : 's'} detected` }) : null,
      required.size ? h('span', { class: 'badge warn', text: `${required.size} value${required.size === 1 ? '' : 's'} to fill in` }) : null,
    );

    add(body, [
      h('div', { class: 'spread' },
        summaryChips,
        h('button', { class: 'btn sm ghost', onClick: showSource }, 'Change image'),
      ),
      plan.notes.length
        ? h('div', { class: 'col' }, plan.notes.map((text) => notice(text, 'warn')))
        : null,
      volumeDatalist(listID, volumes),
      form.node,
      h('div', {},
        h('div', { class: 'section-title', text: 'Address' }),
        h('div', { class: 'card' }, h('div', { class: 'card-body' },
          h('label', { class: 'check' }, pinCheck, 'Give this container a fixed internal address'),
          h('div', { class: 'small faint', style: { margin: '6px 0 10px 24px' } },
            'DocMan puts it on its own bridge network so the address never changes when containers restart.'),
          h('div', { style: { marginLeft: '24px' } }, field('Address', pinIP, 'Leave empty and DocMan picks the next free one.')),
        )),
      ),
      problems,
    ]);

    const submit = async (start) => {
      clear(problems);
      const { spec: value, problems: found } = form.validate();
      if (found.length) {
        add(problems, [notice(found.join(' '), 'bad')]);
        return;
      }
      saveButton.disabled = true;
      startButton.disabled = true;
      try {
        const result = await Containers.create({
          spec: value,
          start,
          pin_ip: pinCheck.checked,
          ip: pinIP.value.trim(),
        });
        if (result.pin_error) toast(`Container created, but the address could not be fixed: ${result.pin_error}`, 'warn', 9000);
        if (result.start_error) toast(`Container created, but it would not start: ${result.start_error}`, 'bad', 12000);
        else toast(start ? `${value.name} is starting` : `${value.name} saved (not started)`, 'ok');
        if (onDone) onDone(result);
        if (actions) actions.finish(result);
        location.hash = `#/containers/${result.id}`;
      } catch (err) {
        toastError(err);
        saveButton.disabled = false;
        startButton.disabled = false;
      }
    };

    const saveButton = h('button', { class: 'btn', onClick: () => submit(false) }, 'Save without starting');
    const startButton = h('button', { class: 'btn primary', onClick: () => submit(true) }, icon('play'), 'Save and start');
    setActions(saveButton, startButton);
  }

  if (initialImage) showPlan(initialImage);
  else showSource();
}

// ---------- source panels ----------

function existingPanel(next) {
  const host = h('div', { class: 'col' }, spinner());
  Images.list().then((data) => {
    clear(host);
    const images = (data.images || []).filter((img) => img.reference && img.reference !== '<none>');
    if (!images.length) {
      add(host, [notice('There are no tagged images on this host yet. Pull one, or upload an archive.', 'warn')]);
      return;
    }
    const search = h('input', { type: 'search', placeholder: 'Filter images' });
    const list = h('div', { class: 'col', style: { gap: '6px', maxHeight: '46vh', overflow: 'auto' } });
    const draw = () => {
      const query = search.value.trim().toLowerCase();
      clear(list);
      add(list, images
        .filter((img) => !query || img.reference.toLowerCase().includes(query))
        .map((img) => h('button', {
          class: 'btn',
          style: { justifyContent: 'flex-start', width: '100%' },
          onClick: () => next(img.reference),
        },
          icon('layers'),
          h('span', { class: 'grow truncate', style: { textAlign: 'left' } }, img.reference),
          h('span', { class: 'small faint nowrap', text: bytes(img.Size) }),
        )));
    };
    search.addEventListener('input', draw);
    add(host, [h('div', { class: 'search' }, icon('search'), search), list]);
    draw();
  }).catch((err) => {
    clear(host);
    add(host, [notice(err.message, 'bad')]);
  });
  return host;
}

/**
 * The host part of a typed reference, when it names one ("ghcr.io/a/b",
 * "localhost:5000/x"); Docker's rule is a first segment with a dot, a colon,
 * or the word localhost.
 */
function typedHost(name) {
  const slash = name.indexOf('/');
  if (slash < 0) return '';
  const first = name.slice(0, slash);
  return first.includes('.') || first.includes(':') || first === 'localhost' ? first.toLowerCase() : '';
}

/** The full reference for a repository in a registry, as Docker writes it. */
function fullReference(reg, name, tag) {
  let repo = name.trim().replace(/^\/+|\/+$/g, '');
  let registry = reg;
  const named = typedHost(repo);
  if (named) {
    if (reg && named === reg.host) repo = repo.slice(named.length + 1);
    else registry = null; // a host of its own: pulled as typed
  }
  const suffix = tag ? `:${tag}` : '';
  if (!registry) return { image: repo, reference: `${repo}${suffix || ':latest'}`, registry: null };
  const image = registry.kind === 'dockerhub' ? repo.replace(/^library\//, '') : `${registry.host}/${repo}`;
  return { image, reference: `${image}${suffix || ':latest'}`, registry, repo };
}

function pullPanel(next) {
  const registrySelect = h('select');
  const registryInfo = h('div', { class: 'help' });
  const addRegistry = h('button', { class: 'btn', title: 'Add a registry, with its sign-in' }, icon('plus'), 'Add');
  const manage = h('div', { class: 'help', text: 'Registries and their sign-ins are managed in Settings → Registries.' });
  const name = h('input', { type: 'text', class: 'mono', placeholder: 'nginx', spellcheck: false, autocomplete: 'off' });
  const nameHelp = h('div', { class: 'help' });
  const hits = h('div', { class: 'col hidden', style: { gap: '4px', maxHeight: '30vh', overflow: 'auto' } });
  const tagInput = h('input', { type: 'text', class: 'mono', placeholder: 'latest', spellcheck: false, autocomplete: 'off', list: 'deploy-tags' });
  const tagList = h('datalist', { id: 'deploy-tags' });
  const tagHelp = h('div', { class: 'help', text: 'Pick a tag or type one; latest when left empty.' });
  const preview = h('div', { class: 'mono small', style: { wordBreak: 'break-all' } });
  const username = h('input', { type: 'text', placeholder: 'instead of the registry\'s saved sign-in', autocomplete: 'off' });
  const password = h('input', { type: 'password', autocomplete: 'off' });
  const log = h('div', { class: 'progress-log hidden' });
  const bar = h('div', { class: 'bar hidden' }, h('i'));
  const button = h('button', { class: 'btn primary' }, icon('download'), 'Pull image');

  let registries = [];
  let kinds = [];
  let current = null;
  let searchSeq = 0;
  let tagSeq = 0;

  const kindOf = (reg) => kinds.find((k) => k.kind === (reg && reg.kind)) || {};

  const updatePreview = () => {
    const typed = name.value.trim();
    if (!typed) { preview.textContent = ''; return; }
    const { reference, registry } = fullReference(current, typed, tagInput.value.trim());
    preview.textContent = `Pulls ${reference}${registry ? '' : ' (the address in the name is used, not the registry above)'}`;
  };

  const loadRegistries = async (selectId) => {
    const data = await Registries.list();
    registries = data.registries || [];
    kinds = data.kinds || [];
    clear(registrySelect);
    add(registrySelect, registries.map((reg) => h('option', {
      value: String(reg.id),
      text: `${reg.name}${reg.is_default ? ' (default)' : ''} — ${reg.host}`,
    })));
    const pick = registries.find((reg) => reg.id === selectId) || registries.find((reg) => reg.is_default) || registries[0];
    if (pick) registrySelect.value = String(pick.id);
    chooseRegistry();
  };

  const chooseRegistry = () => {
    current = registries.find((reg) => String(reg.id) === registrySelect.value) || null;
    const kind = kindOf(current);
    // A GitHub repository offers pre-built archives to install, not images to pull.
    const archives = !!(current && kind.archives);
    pullBox.classList.toggle('hidden', archives);
    archiveBox.classList.toggle('hidden', !archives);
    clear(archiveBox);
    if (archives) add(archiveBox, [archivePanel(current, next)]);
    if (current) {
      const signIn = current.auth_type === 'none'
        ? `no sign-in, so public ${archives ? 'repositories' : 'images'} only`
        : `signs in as ${current.username || 'a saved key'}`;
      const where = archives && current.options && current.options.repo ? `${current.host}/${current.options.repo}` : current.host;
      registryInfo.textContent = `${where} · ${signIn}`;
    }
    if (archives) return;
    name.placeholder = { dockerhub: 'nginx', ghcr: 'owner/image', gitlab: 'group/project/image', gar: 'project/repository/image' }[kind.kind] || 'path/to/image';
    nameHelp.textContent = kind.search
      ? `Type to search ${current ? current.name : 'the registry'}${kind.search_note ? `. ${kind.search_note}` : ''}.`
      : kind.search_note || 'Enter the image\'s path in this registry.';
    hits.classList.add('hidden');
    clear(tagList);
    updatePreview();
    if (name.value.trim()) loadTags();
  };

  const loadTags = debounce(async () => {
    const typed = name.value.trim();
    const target = fullReference(current, typed, '');
    clear(tagList);
    if (!typed || !target.registry) return;
    const seq = ++tagSeq;
    tagHelp.textContent = 'Fetching tags…';
    try {
      const data = await Registries.tags(current.id, target.repo);
      if (seq !== tagSeq) return;
      const tags = data.tags || [];
      add(tagList, tags.map((t) => h('option', { value: t })));
      tagHelp.textContent = tags.length
        ? `${tags.length} tag${tags.length === 1 ? '' : 's'} available; pick one or type it. latest when left empty.`
        : 'The registry lists no tags for this image.';
    } catch (err) {
      if (seq !== tagSeq) return;
      tagHelp.textContent = `Tags could not be listed: ${err.message}`;
    }
  }, 400);

  const runSearch = debounce(async () => {
    const q = name.value.trim();
    const kind = kindOf(current);
    const seq = ++searchSeq;
    clear(hits);
    if (!q || q.length < 2 || !kind.search || typedHost(q)) { hits.classList.add('hidden'); return; }
    hits.classList.remove('hidden');
    add(hits, [h('div', { class: 'small faint', text: 'Searching…' })]);
    try {
      const data = await Registries.search(current.id, q);
      if (seq !== searchSeq) return;
      clear(hits);
      const results = data.results || [];
      if (!results.length) { add(hits, [h('div', { class: 'small faint', text: 'Nothing found. You can still pull a path you know.' })]); return; }
      add(hits, results.slice(0, 25).map((hit) => h('button', {
        class: 'btn',
        style: { justifyContent: 'flex-start', width: '100%', textAlign: 'left' },
        onClick: () => {
          name.value = hit.name;
          hits.classList.add('hidden');
          updatePreview();
          loadTags();
        },
      },
        icon('layers'),
        h('span', { class: 'grow', style: { minWidth: 0 } },
          h('div', { class: 'mono truncate' }, hit.name,
            hit.official ? h('span', { class: 'badge ok', style: { marginLeft: '6px' }, text: 'official' }) : null),
          hit.description ? h('div', { class: 'small faint truncate', text: hit.description }) : null),
        hit.stars ? h('span', { class: 'small faint nowrap', text: `★ ${hit.stars}` }) : null,
      )));
    } catch (err) {
      if (seq !== searchSeq) return;
      clear(hits);
      add(hits, [h('div', { class: 'small', style: { color: 'var(--bad)' }, text: `Search failed: ${err.message}` })]);
    }
  }, 350);

  registrySelect.addEventListener('change', chooseRegistry);
  name.addEventListener('input', () => { updatePreview(); runSearch(); loadTags(); });
  tagInput.addEventListener('input', updatePreview);
  addRegistry.addEventListener('click', async () => {
    const saved = await registryDialog(kinds);
    if (saved) { toast(`${saved.name} added`, 'ok'); loadRegistries(saved.id).catch(toastError); }
  });

  button.addEventListener('click', async () => {
    const typed = name.value.trim();
    if (!typed) { name.focus(); return; }
    const { image, reference, registry } = fullReference(current, typed, tagInput.value.trim());
    button.disabled = true;
    log.className = 'progress-log';
    bar.className = 'bar';
    log.textContent = '';
    const layers = new Map();
    try {
      await stream('/api/images/pull', {
        body: {
          image,
          tag: tagInput.value.trim() || 'latest',
          registry_id: registry ? registry.id : 0,
          username: username.value,
          password: password.value,
        },
        onEvent: (event) => {
          if (event.error || event.errorDetail) {
            log.textContent += `\n${event.error || event.errorDetail.message}`;
            return;
          }
          if (event.id && event.progressDetail && event.progressDetail.total) {
            layers.set(event.id, event.progressDetail);
            let done = 0;
            let total = 0;
            for (const detail of layers.values()) {
              done += detail.current || 0;
              total += detail.total || 0;
            }
            if (total) bar.firstChild.style.width = `${Math.min(100, (done / total) * 100)}%`;
          }
          const line = [event.id, event.status, event.progress].filter(Boolean).join(' ');
          if (line) {
            log.textContent = `${log.textContent}${line}\n`.split('\n').slice(-200).join('\n');
            log.scrollTop = log.scrollHeight;
          }
        },
      });
      bar.firstChild.style.width = '100%';
      toast(`Pulled ${reference}`, 'ok');
      next(reference);
    } catch (err) {
      toastError(err);
      button.disabled = false;
    }
  });

  const pullBox = h('div', { class: 'col' },
    h('div', { class: 'grid c2' },
      h('div', { class: 'field' }, h('label', { text: 'Image' }), name, nameHelp),
      h('div', { class: 'field' }, h('label', { text: 'Tag' }), tagInput, tagList, tagHelp)),
    hits,
    preview,
    h('details', {},
      h('summary', { style: { cursor: 'pointer', fontSize: '13px', color: 'var(--text-dim)' } }, 'Use other credentials for this pull'),
      h('div', { class: 'grid c2', style: { marginTop: '10px' } },
        field('Username', username), field('Password or token', password)),
    ),
    button, bar, log,
  );
  const archiveBox = h('div', { class: 'col hidden' });
  const host = h('div', { class: 'col' },
    h('div', { class: 'field' }, h('label', { text: 'Registry' }),
      h('div', { class: 'row' }, h('div', { class: 'grow' }, registrySelect), addRegistry),
      registryInfo, manage),
    pullBox,
    archiveBox,
  );
  loadRegistries().catch((err) => { registryInfo.textContent = `Registries could not be loaded: ${err.message}`; });
  return host;
}

/**
 * The pre-built images a GitHub repository registry offers, grouped by name,
 * each with a version picker and Install. Only archives are offered: DocMan
 * never builds from a Dockerfile or compose file.
 */
function archivePanel(reg, next) {
  const host = h('div', { class: 'col' }, spinner());
  Registries.archives(reg.id).then((listing) => draw(listing)).catch((err) => {
    clear(host);
    add(host, [notice(err.message, 'bad')]);
  });

  const archLabel = (a) => (a.arch ? (a.arch === 'multiarch' ? 'all architectures' : a.arch) : 'architecture not named');
  const optionText = (a) => [a.version || 'unversioned', archLabel(a), a.source === 'release' ? `release ${a.release}` : a.file, bytes(a.size)]
    .join(' · ');

  function draw(listing) {
    clear(host);
    const artifacts = listing.artifacts || [];
    const where = `${listing.repo} · ${listing.ref}${listing.host_arch ? ` · this host is ${listing.host_arch}` : ''}`;
    add(host, [h('div', { class: 'small faint', text: where })]);
    if (!artifacts.length) {
      add(host, [notice(listing.build_files && listing.build_files.length
        ? `${listing.repo} has ${listing.build_files.join(', ')} but no pre-built image archives. DocMan installs only pre-built images (docker save archives, as .tar, .tar.gz or .tgz, in the repository or attached to a release).`
        : `No pre-built image archives were found in ${listing.repo}. DocMan looks for docker save archives (.tar, .tar.gz or .tgz) in the repository's files and its releases.`, 'warn')]);
      return;
    }
    if (listing.build_files && listing.build_files.length) {
      add(host, [h('div', { class: 'small faint', text: `The repository also has ${listing.build_files.join(', ')}. DocMan does not build images; only the pre-built ones below can be installed.` })]);
    }
    if (listing.truncated) {
      add(host, [h('div', { class: 'small faint', text: 'The repository is very large, so GitHub listed only part of it. Set a folder on the registry to look in.' })]);
    }
    const groups = new Map();
    for (const a of artifacts) {
      if (!groups.has(a.name)) groups.set(a.name, []);
      groups.get(a.name).push(a);
    }
    for (const [name, list] of groups) {
      const fits = list.filter((a) => a.fits);
      const select = h('select', {},
        list.map((a) => h('option', { value: a.key, disabled: !a.fits, text: `${optionText(a)}${a.fits ? '' : ' — does not run on this host'}` })));
      if (fits.length) select.value = fits[0].key;
      const button = h('button', { class: 'btn primary', disabled: !fits.length }, icon('download'), 'Install');
      const log = h('div', { class: 'progress-log hidden' });
      const bar = h('div', { class: 'bar hidden' }, h('i'));
      button.addEventListener('click', () => install(select.value, button, log, bar));
      add(host, [h('div', { class: 'card', style: { padding: '12px' } }, h('div', { class: 'col' },
        h('div', { class: 'row' }, icon('layers'), h('span', { class: 'mono', style: { fontWeight: '600' }, text: name }),
          fits.length ? h('span', { class: 'badge ok', text: `newest: ${fits[0].version || 'unversioned'}` }) : h('span', { class: 'badge bad', text: 'none for this host' })),
        h('div', { class: 'row' }, h('div', { class: 'grow' }, select), button),
        bar, log))]);
    }
  }

  async function install(key, button, log, bar) {
    button.disabled = true;
    log.className = 'progress-log';
    bar.className = 'bar';
    log.textContent = '';
    let loaded = null;
    let failed = '';
    try {
      await stream(`/api/registries/${reg.id}/install`, {
        body: { key },
        onEvent: (event) => {
          if (event.error) { failed = event.error; log.textContent += `\n${event.error}`; return; }
          if (event.loaded) { loaded = event.loaded; return; }
          if (event.status) {
            const pct = /(\d+)%/.exec(event.status);
            if (pct) bar.firstChild.style.width = `${pct[1]}%`;
            log.textContent = `${log.textContent}${event.status}\n`.split('\n').slice(-200).join('\n');
            log.scrollTop = log.scrollHeight;
          }
        },
      });
      if (failed || !loaded) throw new Error(failed || 'The install did not finish; see the log.');
      bar.firstChild.style.width = '100%';
      // A moving tag such as latest lets later versions reach the container.
      const refs = loaded.filter((r) => !r.startsWith('sha256:'));
      const pick = refs.find((r) => r.endsWith(':latest')) || refs[0] || loaded[0];
      toast(`Installed ${refs.join(', ') || pick}`, 'ok');
      next(pick);
    } catch (err) {
      toastError(err);
      button.disabled = false;
    }
  }

  return host;
}
