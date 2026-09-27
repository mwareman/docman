// Help pages: instant search, keyboard shortcuts, the "on this page" highlight
// and copy buttons on code blocks. The pages are complete without it; this
// only makes them quicker to get around.

const input = document.getElementById('help-q');
const results = document.getElementById('help-results');
let index = null;
let loading = null;
let active = -1;

// ---------- search ----------

function loadIndex() {
  if (index) return Promise.resolve(index);
  if (!loading) {
    loading = fetch('/help/search.json', { credentials: 'omit' })
      .then((res) => (res.ok ? res.json() : []))
      .then((entries) => {
        index = entries.map((e) => ({
          ...e,
          hay: `${e.p} ${e.h || ''} ${e.t}`.toLowerCase(),
          title: `${e.h || ''} ${e.p}`.toLowerCase(),
        }));
        return index;
      })
      .catch(() => { index = []; return index; });
  }
  return loading;
}

function score(entry, terms) {
  let total = 0;
  for (const term of terms) {
    if (!entry.hay.includes(term)) return 0;
    if ((entry.h || '').toLowerCase().includes(term)) total += 8;
    if (entry.p.toLowerCase().includes(term)) total += 5;
    const hits = entry.hay.split(term).length - 1;
    total += Math.min(hits, 6);
  }
  // Whole-phrase matches rank above scattered words.
  const phrase = terms.join(' ');
  if (terms.length > 1 && entry.hay.includes(phrase)) total += 10;
  // A page whose own title matches beats the sections inside it.
  if (!entry.h && terms.every((term) => entry.p.toLowerCase().includes(term))) total += 12;
  // The API reference sorts after the guide for the same strength of match.
  if (entry.s === 'API reference') total -= 1;
  return total;
}

function snippet(text, terms) {
  const lower = text.toLowerCase();
  let at = -1;
  for (const term of terms) {
    at = lower.indexOf(term);
    if (at >= 0) break;
  }
  const start = Math.max(0, at - 50);
  const piece = (start > 0 ? '…' : '') + text.slice(start, start + 170) + (start + 170 < text.length ? '…' : '');
  return piece;
}

function highlight(node, text, terms) {
  const pattern = new RegExp(`(${terms.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'gi');
  let last = 0;
  text.replace(pattern, (match, _group, offset) => {
    node.append(document.createTextNode(text.slice(last, offset)));
    const mark = document.createElement('mark');
    mark.textContent = match;
    node.append(mark);
    last = offset + match.length;
    return match;
  });
  node.append(document.createTextNode(text.slice(last)));
}

function render(query) {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  results.replaceChildren();
  active = -1;
  if (!terms.length) {
    close();
    return;
  }
  const found = index
    .map((entry) => ({ entry, s: score(entry, terms) }))
    .filter((r) => r.s > 0)
    .sort((a, b) => b.s - a.s)
    .slice(0, 12);

  if (!found.length) {
    const none = document.createElement('div');
    none.className = 'help-result-none';
    none.textContent = `Nothing matches “${query}”.`;
    results.append(none);
  }
  found.forEach(({ entry }, i) => {
    const link = document.createElement('a');
    link.href = entry.u;
    link.className = 'help-result';
    link.setAttribute('role', 'option');
    link.id = `help-r${i}`;
    const where = document.createElement('div');
    where.className = 'help-result-where';
    where.textContent = entry.h ? `${entry.s} › ${entry.p}` : entry.s;
    const title = document.createElement('div');
    title.className = 'help-result-title';
    highlight(title, entry.h || entry.p, terms);
    const text = document.createElement('div');
    text.className = 'help-result-text';
    highlight(text, snippet(entry.t, terms), terms);
    link.append(where, title, text);
    link.addEventListener('mousemove', () => select(i));
    results.append(link);
  });
  results.hidden = false;
  input.setAttribute('aria-expanded', 'true');
}

function select(i) {
  const items = [...results.querySelectorAll('.help-result')];
  if (!items.length) return;
  active = (i + items.length) % items.length;
  items.forEach((item, n) => item.classList.toggle('on', n === active));
  input.setAttribute('aria-activedescendant', items[active].id);
  items[active].scrollIntoView({ block: 'nearest' });
}

function close() {
  results.hidden = true;
  input.setAttribute('aria-expanded', 'false');
  input.removeAttribute('aria-activedescendant');
}

if (input && results) {
  input.addEventListener('focus', () => {
    loadIndex().then(() => { if (input.value.trim()) render(input.value.trim()); });
  });
  input.addEventListener('input', () => {
    loadIndex().then(() => render(input.value.trim()));
  });
  input.addEventListener('keydown', (event) => {
    if (event.key === 'ArrowDown') { event.preventDefault(); select(active + 1); }
    else if (event.key === 'ArrowUp') { event.preventDefault(); select(active - 1); }
    else if (event.key === 'Enter') {
      const items = results.querySelectorAll('.help-result');
      const target = items[active >= 0 ? active : 0];
      if (target) { event.preventDefault(); location.href = target.href; }
    } else if (event.key === 'Escape') {
      if (input.value) input.value = '';
      close();
      input.blur();
    }
  });
  document.addEventListener('click', (event) => {
    if (!event.target.closest('.help-search')) close();
  });
  // "/" jumps to the search box from anywhere on the page.
  document.addEventListener('keydown', (event) => {
    if (event.key !== '/' || event.ctrlKey || event.metaKey || event.altKey) return;
    const tag = (document.activeElement && document.activeElement.tagName) || '';
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
    event.preventDefault();
    input.focus();
    input.select();
  });
}

// ---------- menu on small screens ----------

const navWrap = document.querySelector('.help-nav-wrap');
if (navWrap && window.matchMedia('(max-width: 940px)').matches) navWrap.open = false;

// ---------- menu state across pages ----------
//
// Every help page is a fresh load, so without this the menu would scroll back
// to the top and re-collapse sections each time an entry is chosen. Its scroll
// position and which sections are open are kept for this tab's session.

const NAV_KEY = 'docman.help.nav';
const sections = [...document.querySelectorAll('.help-nav-section')];
const sectionName = (details) => details.querySelector('summary').textContent.trim();

function readNavState() {
  try { return JSON.parse(sessionStorage.getItem(NAV_KEY)) || {}; } catch { return {}; }
}
function saveNavState() {
  if (!navWrap) return;
  const state = {
    scroll: navWrap.scrollTop,
    open: Object.fromEntries(sections.map((d) => [sectionName(d), d.open])),
  };
  try { sessionStorage.setItem(NAV_KEY, JSON.stringify(state)); } catch { /* storage unavailable */ }
}

if (navWrap && sections.length) {
  const saved = readNavState();
  const current = navWrap.querySelector('a.on');

  // Restore the sections the reader opened or closed, but always show the
  // section holding the page being read.
  if (saved.open) {
    for (const details of sections) {
      const wanted = saved.open[sectionName(details)];
      if (typeof wanted === 'boolean') details.open = wanted;
    }
  }
  if (current) current.closest('.help-nav-section').open = true;

  // Put the menu back where it was, then make sure the current page is in
  // view, as it may not be after a search result or a link in the text.
  if (typeof saved.scroll === 'number') navWrap.scrollTop = saved.scroll;
  if (current) {
    const box = navWrap.getBoundingClientRect();
    const link = current.getBoundingClientRect();
    if (link.top < box.top + 8 || link.bottom > box.bottom - 8) {
      navWrap.scrollTop += link.top - box.top - box.height / 3;
    }
  }

  for (const details of sections) details.addEventListener('toggle', saveNavState);
  navWrap.addEventListener('scroll', () => {
    clearTimeout(saveNavState.timer);
    saveNavState.timer = setTimeout(saveNavState, 120);
  }, { passive: true });
  window.addEventListener('pagehide', saveNavState);
}

// ---------- "on this page" highlight ----------

// The current section is the last heading scrolled past the top of the page.
const tocLinks = [...document.querySelectorAll('.help-toc a')];
const tocTargets = tocLinks
  .map((link) => ({ link, heading: document.getElementById(decodeURIComponent(link.hash.slice(1))) }))
  .filter((t) => t.heading);
if (tocTargets.length) {
  let queued = false;
  const update = () => {
    queued = false;
    let current = tocTargets[0];
    for (const target of tocTargets) {
      if (target.heading.getBoundingClientRect().top <= 110) current = target;
      else break;
    }
    // At the very bottom, the last section is current even if it is short.
    if (window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 4) {
      current = tocTargets[tocTargets.length - 1];
    }
    tocTargets.forEach((t) => t.link.classList.toggle('on', t === current));
  };
  window.addEventListener('scroll', () => {
    if (!queued) { queued = true; requestAnimationFrame(update); }
  }, { passive: true });
  update();
}

// ---------- copy buttons ----------

if (navigator.clipboard && window.isSecureContext) {
  for (const block of document.querySelectorAll('.doc-code')) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'doc-copy';
    button.textContent = 'Copy';
    button.addEventListener('click', () => {
      navigator.clipboard.writeText(block.querySelector('code').textContent.replace(/\n$/, '')).then(() => {
        button.textContent = 'Copied';
        setTimeout(() => { button.textContent = 'Copy'; }, 1400);
      }).catch(() => { button.textContent = 'Copy failed'; });
    });
    block.append(button);
  }
}
