// A compact VT100/xterm-subset terminal emulator.
//
// It covers what an interactive container shell actually uses: SGR colours
// (16, 256 and truecolor), cursor movement, erase and insert/delete, scroll
// regions, the alternate screen, autowrap and a scrollback buffer. Rendering is
// plain DOM: one element per line, rebuilt only for lines that changed.

const DEFAULT_FG = -1;
const DEFAULT_BG = -2;
const SCROLLBACK = 2000;

const BASE16 = [
  '#0c1017', '#e05561', '#3fbf7f', '#d9a03a', '#4a9df0', '#b48ce8', '#39b8c2', '#c3ccd8',
  '#4a5769', '#ff7b86', '#5fe0a0', '#f5c65e', '#74bcff', '#cfb0ff', '#5fdce8', '#eef3fa',
];

const PALETTE = buildPalette();

function buildPalette() {
  const colors = BASE16.slice();
  const levels = [0, 95, 135, 175, 215, 255];
  for (let r = 0; r < 6; r++) {
    for (let g = 0; g < 6; g++) {
      for (let b = 0; b < 6; b++) {
        colors.push(`rgb(${levels[r]},${levels[g]},${levels[b]})`);
      }
    }
  }
  for (let i = 0; i < 24; i++) {
    const v = 8 + i * 10;
    colors.push(`rgb(${v},${v},${v})`);
  }
  return colors;
}

const FLAG_BOLD = 1;
const FLAG_DIM = 2;
const FLAG_ITALIC = 4;
const FLAG_UNDERLINE = 8;
const FLAG_INVERSE = 16;
const FLAG_HIDDEN = 32;
const FLAG_STRIKE = 64;

const blankCell = () => ({ ch: ' ', fg: DEFAULT_FG, bg: DEFAULT_BG, flags: 0 });

function makeRow(cols) {
  const cells = new Array(cols);
  for (let i = 0; i < cols; i++) cells[i] = blankCell();
  return { cells, dirty: true };
}

function colorCSS(value, isBackground) {
  if (value === DEFAULT_FG) return null;
  if (value === DEFAULT_BG) return null;
  if (typeof value === 'string') return value;
  if (value >= 0 && value < PALETTE.length) return PALETTE[value];
  return isBackground ? null : null;
}

/**
 * Create a terminal inside host.
 * @param {HTMLElement} host
 * @param {{onData?: (text:string)=>void, onResize?: (cols:number, rows:number)=>void}} handlers
 */
export function createTerminal(host, handlers = {}) {
  const root = document.createElement('div');
  root.className = 'term';
  root.tabIndex = 0;
  root.setAttribute('role', 'application');
  root.setAttribute('aria-label', 'Container console');

  const scrollLayer = document.createElement('div');
  const screenLayer = document.createElement('div');
  root.append(scrollLayer, screenLayer);
  host.append(root);

  let cols = 80;
  let rows = 24;
  let cellWidth = 8;
  let lineHeight = 17;

  let rowsBuffer = [];
  let altBuffer = null;
  let scrollback = [];
  let lineNodes = [];

  let cx = 0;
  let cy = 0;
  let savedCursor = null;
  let wrapPending = false;
  let scrollTop = 0;
  let scrollBottom = 0;
  let cursorVisible = true;
  let appCursorKeys = false;
  let bracketedPaste = false;
  let autoScroll = true;

  let attrs = { fg: DEFAULT_FG, bg: DEFAULT_BG, flags: 0 };

  // Parser state
  let state = 'ground';
  let paramBuffer = '';
  let intermediate = '';
  let stringBuffer = '';
  const decoder = new TextDecoder('utf-8', { fatal: false });

  let frame = 0;

  // ---------- geometry ----------

  function measure() {
    const probe = document.createElement('span');
    probe.textContent = 'M'.repeat(20);
    probe.style.visibility = 'hidden';
    probe.style.position = 'absolute';
    probe.style.whiteSpace = 'pre';
    root.append(probe);
    const rect = probe.getBoundingClientRect();
    if (rect.width > 0) cellWidth = rect.width / 20;
    if (rect.height > 0) lineHeight = rect.height;
    probe.remove();
  }

  function resizeBuffers(nextCols, nextRows) {
    const grow = (buffer) => {
      while (buffer.length < nextRows) buffer.push(makeRow(nextCols));
      while (buffer.length > nextRows) {
        const removed = buffer.shift();
        pushScrollback(removed, nextCols);
      }
      for (const row of buffer) {
        while (row.cells.length < nextCols) row.cells.push(blankCell());
        if (row.cells.length > nextCols) row.cells.length = nextCols;
        row.dirty = true;
      }
    };
    grow(rowsBuffer);
    if (altBuffer) grow(altBuffer);
    cols = nextCols;
    rows = nextRows;
    scrollTop = 0;
    scrollBottom = rows - 1;
    cx = Math.min(cx, cols - 1);
    cy = Math.min(cy, rows - 1);
    syncLineNodes();
  }

  function syncLineNodes() {
    while (lineNodes.length < rows) {
      const node = document.createElement('div');
      screenLayer.append(node);
      lineNodes.push(node);
    }
    while (lineNodes.length > rows) {
      lineNodes.pop().remove();
    }
  }

  function fit() {
    measure();
    const width = host.clientWidth - 4;
    const height = host.clientHeight - 4;
    const nextCols = Math.max(20, Math.min(400, Math.floor(width / cellWidth)));
    const nextRows = Math.max(5, Math.min(150, Math.floor(height / lineHeight)));
    if (nextCols === cols && nextRows === rows) return;
    resizeBuffers(nextCols, nextRows);
    schedule();
    if (handlers.onResize) handlers.onResize(cols, rows);
  }

  // ---------- rendering ----------

  function renderRow(row, isCursorRow, cursorX) {
    const parts = [];
    let run = null;
    const flush = () => {
      if (!run) return;
      parts.push(styleSpan(run));
      run = null;
    };
    for (let x = 0; x < row.cells.length; x++) {
      const cell = row.cells[x];
      const isCursor = isCursorRow && x === cursorX && cursorVisible;
      const key = `${cell.fg}|${cell.bg}|${cell.flags}|${isCursor}`;
      if (!run || run.key !== key) {
        flush();
        run = { key, cell, isCursor, text: '' };
      }
      run.text += cell.ch;
    }
    flush();
    // Trailing blanks add nothing; drop them so selection stays tidy.
    let html = parts.join('');
    if (html === '') html = '&nbsp;';
    row.html = html;
    return html;
  }

  function styleSpan(run) {
    const { cell, isCursor, text } = run;
    let fg = cell.fg;
    let bg = cell.bg;
    if (cell.flags & FLAG_INVERSE) {
      const tmp = fg;
      fg = bg === DEFAULT_BG ? 0 : bg;
      bg = tmp === DEFAULT_FG ? 15 : tmp;
    }
    const styles = [];
    const fgCSS = colorCSS(fg, false);
    const bgCSS = colorCSS(bg, true);
    if (fgCSS) styles.push(`color:${fgCSS}`);
    if (bgCSS) styles.push(`background:${bgCSS}`);
    if (cell.flags & FLAG_BOLD) styles.push('font-weight:600');
    if (cell.flags & FLAG_DIM) styles.push('opacity:.62');
    if (cell.flags & FLAG_ITALIC) styles.push('font-style:italic');
    const decorations = [];
    if (cell.flags & FLAG_UNDERLINE) decorations.push('underline');
    if (cell.flags & FLAG_STRIKE) decorations.push('line-through');
    if (decorations.length) styles.push(`text-decoration:${decorations.join(' ')}`);
    if (cell.flags & FLAG_HIDDEN) styles.push('visibility:hidden');

    const cls = isCursor ? ' class="cursor"' : '';
    const style = styles.length ? ` style="${styles.join(';')}"` : '';
    return `<span${cls}${style}>${escapeHTML(text)}</span>`;
  }

  function escapeHTML(text) {
    return text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
  }

  function schedule() {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      paint();
    });
  }

  function paint() {
    for (let y = 0; y < rows; y++) {
      const row = rowsBuffer[y];
      if (!row) continue;
      const hasCursor = y === cy;
      const hadCursor = row.renderedCursor === true;
      // Repaint a line when its contents changed, or when the cursor moved
      // onto or off it.
      if (!row.dirty && !hasCursor && !hadCursor) continue;
      const html = renderRow(row, hasCursor, cx);
      const node = lineNodes[y];
      if (node && node.innerHTML !== html) node.innerHTML = html;
      row.dirty = false;
      row.renderedCursor = hasCursor;
    }
    if (autoScroll) root.scrollTop = root.scrollHeight;
  }

  function pushScrollback(row, width) {
    const html = renderRow(row, false, -1);
    const node = document.createElement('div');
    node.innerHTML = html;
    scrollLayer.append(node);
    scrollback.push(node);
    while (scrollback.length > SCROLLBACK) {
      scrollback.shift().remove();
    }
    void width;
  }

  // ---------- buffer operations ----------

  function currentRow() {
    return rowsBuffer[cy];
  }

  function markDirty(y) {
    if (rowsBuffer[y]) rowsBuffer[y].dirty = true;
  }

  function clearCell(cell) {
    cell.ch = ' ';
    cell.fg = DEFAULT_FG;
    cell.bg = attrs.bg;
    cell.flags = 0;
  }

  function scrollUp(count = 1) {
    for (let i = 0; i < count; i++) {
      const removed = rowsBuffer.splice(scrollTop, 1)[0];
      if (scrollTop === 0 && !altBuffer) pushScrollback(removed, cols);
      rowsBuffer.splice(scrollBottom, 0, makeRow(cols));
    }
    for (let y = scrollTop; y <= scrollBottom; y++) markDirty(y);
  }

  function scrollDown(count = 1) {
    for (let i = 0; i < count; i++) {
      rowsBuffer.splice(scrollBottom, 1);
      rowsBuffer.splice(scrollTop, 0, makeRow(cols));
    }
    for (let y = scrollTop; y <= scrollBottom; y++) markDirty(y);
  }

  function lineFeed() {
    if (cy === scrollBottom) scrollUp(1);
    else if (cy < rows - 1) cy++;
    markDirty(cy);
  }

  function putChar(ch) {
    if (wrapPending) {
      wrapPending = false;
      cx = 0;
      lineFeed();
    }
    const row = currentRow();
    if (!row) return;
    const cell = row.cells[cx];
    if (!cell) return;
    cell.ch = ch;
    cell.fg = attrs.fg;
    cell.bg = attrs.bg;
    cell.flags = attrs.flags;
    row.dirty = true;
    if (cx + 1 >= cols) wrapPending = true;
    else cx++;
  }

  // ---------- parser ----------

  function write(chunk) {
    const text = typeof chunk === 'string'
      ? chunk
      : decoder.decode(chunk instanceof ArrayBuffer ? new Uint8Array(chunk) : chunk, { stream: true });
    for (const ch of text) consume(ch);
    schedule();
  }

  function consume(ch) {
    const code = ch.codePointAt(0);
    switch (state) {
      case 'ground':
        return ground(ch, code);
      case 'esc':
        return escState(ch);
      case 'csi':
        return csiState(ch, code);
      case 'osc':
        return oscState(ch, code);
      case 'ignore':
        // DCS / APC / PM: swallow until ST.
        if (code === 0x1b) state = 'ignore-esc';
        else if (code === 0x07) state = 'ground';
        return;
      case 'ignore-esc':
        state = ch === '\\' ? 'ground' : 'ignore';
        return;
      case 'charset':
        state = 'ground';
        return;
      default:
        state = 'ground';
    }
  }

  function ground(ch, code) {
    switch (code) {
      case 0x00: return;
      case 0x07: return; // bell
      case 0x08:
        wrapPending = false;
        if (cx > 0) cx--;
        markDirty(cy);
        return;
      case 0x09: {
        wrapPending = false;
        const next = Math.min(cols - 1, (Math.floor(cx / 8) + 1) * 8);
        cx = next;
        return;
      }
      case 0x0a:
      case 0x0b:
      case 0x0c:
        wrapPending = false;
        lineFeed();
        return;
      case 0x0d:
        wrapPending = false;
        cx = 0;
        markDirty(cy);
        return;
      case 0x0e:
      case 0x0f:
        return; // charset shifts
      case 0x1b:
        state = 'esc';
        paramBuffer = '';
        intermediate = '';
        return;
      default:
        if (code < 0x20) return;
        putChar(ch);
    }
  }

  function escState(ch) {
    switch (ch) {
      case '[':
        state = 'csi';
        paramBuffer = '';
        intermediate = '';
        return;
      case ']':
        state = 'osc';
        stringBuffer = '';
        return;
      case 'P':
      case '^':
      case '_':
        state = 'ignore';
        return;
      case '(':
      case ')':
      case '*':
      case '+':
        state = 'charset';
        return;
      case '7':
        saveCursor();
        state = 'ground';
        return;
      case '8':
        restoreCursor();
        state = 'ground';
        return;
      case 'D':
        lineFeed();
        state = 'ground';
        return;
      case 'E':
        cx = 0;
        lineFeed();
        state = 'ground';
        return;
      case 'M':
        if (cy === scrollTop) scrollDown(1);
        else if (cy > 0) cy--;
        markDirty(cy);
        state = 'ground';
        return;
      case 'c':
        reset();
        state = 'ground';
        return;
      default:
        state = 'ground';
    }
  }

  function csiState(ch, code) {
    if (code >= 0x30 && code <= 0x3f) {
      paramBuffer += ch;
      return;
    }
    if (code >= 0x20 && code <= 0x2f) {
      intermediate += ch;
      return;
    }
    dispatchCSI(ch);
    state = 'ground';
  }

  function oscState(ch, code) {
    if (code === 0x07) {
      state = 'ground';
      stringBuffer = '';
      return;
    }
    if (code === 0x1b) {
      state = 'ignore-esc';
      return;
    }
    if (stringBuffer.length < 2048) stringBuffer += ch;
  }

  function params() {
    if (paramBuffer === '' || paramBuffer === '?' || paramBuffer === '>') return [];
    return paramBuffer
      .replace(/^[?><!]/, '')
      .split(';')
      .map((part) => {
        const first = part.split(':')[0];
        const n = parseInt(first, 10);
        return Number.isNaN(n) ? 0 : n;
      });
  }

  function rawParams() {
    return paramBuffer.replace(/^[?><!]/, '').split(';');
  }

  function param(index, fallback) {
    const list = params();
    const value = list[index];
    return value === undefined || value === 0 ? fallback : value;
  }

  function dispatchCSI(final) {
    const isPrivate = paramBuffer.startsWith('?');
    const list = params();
    switch (final) {
      case 'A':
        cy = Math.max(scrollTop, cy - param(0, 1));
        wrapPending = false;
        markDirty(cy);
        return;
      case 'B':
        cy = Math.min(scrollBottom, cy + param(0, 1));
        wrapPending = false;
        markDirty(cy);
        return;
      case 'C':
        cx = Math.min(cols - 1, cx + param(0, 1));
        wrapPending = false;
        markDirty(cy);
        return;
      case 'D':
        cx = Math.max(0, cx - param(0, 1));
        wrapPending = false;
        markDirty(cy);
        return;
      case 'E':
        cy = Math.min(scrollBottom, cy + param(0, 1));
        cx = 0;
        return;
      case 'F':
        cy = Math.max(scrollTop, cy - param(0, 1));
        cx = 0;
        return;
      case 'G':
      case '`':
        cx = clamp(param(0, 1) - 1, 0, cols - 1);
        wrapPending = false;
        return;
      case 'd':
        cy = clamp(param(0, 1) - 1, 0, rows - 1);
        return;
      case 'H':
      case 'f':
        cy = clamp(param(0, 1) - 1, 0, rows - 1);
        cx = clamp(param(1, 1) - 1, 0, cols - 1);
        wrapPending = false;
        return;
      case 'J':
        eraseInDisplay(list[0] || 0);
        return;
      case 'K':
        eraseInLine(list[0] || 0);
        return;
      case 'L':
        insertLines(param(0, 1));
        return;
      case 'M':
        deleteLines(param(0, 1));
        return;
      case 'P':
        deleteChars(param(0, 1));
        return;
      case '@':
        insertChars(param(0, 1));
        return;
      case 'X':
        eraseChars(param(0, 1));
        return;
      case 'S':
        scrollUp(param(0, 1));
        return;
      case 'T':
        scrollDown(param(0, 1));
        return;
      case 'm':
        applySGR(rawParams());
        return;
      case 'r':
        scrollTop = clamp(param(0, 1) - 1, 0, rows - 1);
        scrollBottom = clamp(param(1, rows) - 1, scrollTop, rows - 1);
        cx = 0;
        cy = scrollTop;
        return;
      case 'h':
        setMode(list, isPrivate, true);
        return;
      case 'l':
        setMode(list, isPrivate, false);
        return;
      case 's':
        saveCursor();
        return;
      case 'u':
        restoreCursor();
        return;
      case 'n':
        if (list[0] === 6 && handlers.onData) {
          handlers.onData(`\u001b[${cy + 1};${cx + 1}R`);
        }
        return;
      case 'c':
        if (handlers.onData) handlers.onData('\u001b[?1;2c');
        return;
      default:
        return;
    }
  }

  function setMode(list, isPrivate, on) {
    for (const mode of list) {
      if (!isPrivate) continue;
      switch (mode) {
        case 1:
          appCursorKeys = on;
          break;
        case 25:
          cursorVisible = on;
          markDirty(cy);
          break;
        case 1049:
        case 1047:
        case 47:
          toggleAltScreen(on);
          break;
        case 2004:
          bracketedPaste = on;
          break;
        default:
          break;
      }
    }
  }

  function toggleAltScreen(on) {
    if (on && !altBuffer) {
      altBuffer = rowsBuffer;
      rowsBuffer = [];
      for (let y = 0; y < rows; y++) rowsBuffer.push(makeRow(cols));
      savedCursor = { cx, cy, attrs: { ...attrs } };
      cx = 0;
      cy = 0;
    } else if (!on && altBuffer) {
      rowsBuffer = altBuffer;
      altBuffer = null;
      if (savedCursor) {
        cx = Math.min(savedCursor.cx, cols - 1);
        cy = Math.min(savedCursor.cy, rows - 1);
        attrs = savedCursor.attrs;
        savedCursor = null;
      }
      for (let y = 0; y < rows; y++) markDirty(y);
    }
  }

  function saveCursor() {
    savedCursor = { cx, cy, attrs: { ...attrs } };
  }

  function restoreCursor() {
    if (!savedCursor) return;
    cx = Math.min(savedCursor.cx, cols - 1);
    cy = Math.min(savedCursor.cy, rows - 1);
    attrs = { ...savedCursor.attrs };
    markDirty(cy);
  }

  function eraseInDisplay(mode) {
    if (mode === 2 || mode === 3) {
      for (let y = 0; y < rows; y++) {
        for (const cell of rowsBuffer[y].cells) clearCell(cell);
        markDirty(y);
      }
      return;
    }
    if (mode === 0) {
      eraseInLine(0);
      for (let y = cy + 1; y < rows; y++) {
        for (const cell of rowsBuffer[y].cells) clearCell(cell);
        markDirty(y);
      }
      return;
    }
    eraseInLine(1);
    for (let y = 0; y < cy; y++) {
      for (const cell of rowsBuffer[y].cells) clearCell(cell);
      markDirty(y);
    }
  }

  function eraseInLine(mode) {
    const row = currentRow();
    if (!row) return;
    const from = mode === 0 ? cx : 0;
    const to = mode === 1 ? cx : cols - 1;
    for (let x = from; x <= to && x < cols; x++) clearCell(row.cells[x]);
    row.dirty = true;
  }

  function eraseChars(count) {
    const row = currentRow();
    if (!row) return;
    for (let x = cx; x < Math.min(cols, cx + count); x++) clearCell(row.cells[x]);
    row.dirty = true;
  }

  function insertChars(count) {
    const row = currentRow();
    if (!row) return;
    for (let i = 0; i < count; i++) {
      row.cells.splice(cx, 0, blankCell());
      row.cells.length = cols;
    }
    row.dirty = true;
  }

  function deleteChars(count) {
    const row = currentRow();
    if (!row) return;
    for (let i = 0; i < count; i++) {
      row.cells.splice(cx, 1);
      row.cells.push(blankCell());
    }
    row.dirty = true;
  }

  function insertLines(count) {
    if (cy < scrollTop || cy > scrollBottom) return;
    for (let i = 0; i < count; i++) {
      rowsBuffer.splice(scrollBottom, 1);
      rowsBuffer.splice(cy, 0, makeRow(cols));
    }
    for (let y = cy; y <= scrollBottom; y++) markDirty(y);
  }

  function deleteLines(count) {
    if (cy < scrollTop || cy > scrollBottom) return;
    for (let i = 0; i < count; i++) {
      rowsBuffer.splice(cy, 1);
      rowsBuffer.splice(scrollBottom, 0, makeRow(cols));
    }
    for (let y = cy; y <= scrollBottom; y++) markDirty(y);
  }

  function applySGR(raw) {
    if (raw.length === 0 || (raw.length === 1 && raw[0] === '')) {
      attrs = { fg: DEFAULT_FG, bg: DEFAULT_BG, flags: 0 };
      return;
    }
    // Flatten colon sub-parameters so 38:2::r:g:b behaves like 38;2;r;g;b.
    const flat = [];
    for (const part of raw) {
      for (const piece of part.split(':')) flat.push(piece === '' ? 0 : parseInt(piece, 10) || 0);
    }
    for (let i = 0; i < flat.length; i++) {
      const code = flat[i];
      if (code === 0) {
        attrs = { fg: DEFAULT_FG, bg: DEFAULT_BG, flags: 0 };
      } else if (code === 1) attrs.flags |= FLAG_BOLD;
      else if (code === 2) attrs.flags |= FLAG_DIM;
      else if (code === 3) attrs.flags |= FLAG_ITALIC;
      else if (code === 4) attrs.flags |= FLAG_UNDERLINE;
      else if (code === 7) attrs.flags |= FLAG_INVERSE;
      else if (code === 8) attrs.flags |= FLAG_HIDDEN;
      else if (code === 9) attrs.flags |= FLAG_STRIKE;
      else if (code === 21 || code === 22) attrs.flags &= ~(FLAG_BOLD | FLAG_DIM);
      else if (code === 23) attrs.flags &= ~FLAG_ITALIC;
      else if (code === 24) attrs.flags &= ~FLAG_UNDERLINE;
      else if (code === 27) attrs.flags &= ~FLAG_INVERSE;
      else if (code === 28) attrs.flags &= ~FLAG_HIDDEN;
      else if (code === 29) attrs.flags &= ~FLAG_STRIKE;
      else if (code >= 30 && code <= 37) attrs.fg = code - 30;
      else if (code === 39) attrs.fg = DEFAULT_FG;
      else if (code >= 40 && code <= 47) attrs.bg = code - 40;
      else if (code === 49) attrs.bg = DEFAULT_BG;
      else if (code >= 90 && code <= 97) attrs.fg = code - 90 + 8;
      else if (code >= 100 && code <= 107) attrs.bg = code - 100 + 8;
      else if (code === 38 || code === 48) {
        const target = code === 38 ? 'fg' : 'bg';
        const kind = flat[i + 1];
        if (kind === 5) {
          attrs[target] = clamp(flat[i + 2] || 0, 0, 255);
          i += 2;
        } else if (kind === 2) {
          const r = clamp(flat[i + 2] || 0, 0, 255);
          const g = clamp(flat[i + 3] || 0, 0, 255);
          const b = clamp(flat[i + 4] || 0, 0, 255);
          attrs[target] = `rgb(${r},${g},${b})`;
          i += 4;
        } else {
          i += 1;
        }
      }
    }
  }

  function reset() {
    rowsBuffer = [];
    for (let y = 0; y < rows; y++) rowsBuffer.push(makeRow(cols));
    altBuffer = null;
    cx = 0;
    cy = 0;
    scrollTop = 0;
    scrollBottom = rows - 1;
    attrs = { fg: DEFAULT_FG, bg: DEFAULT_BG, flags: 0 };
    cursorVisible = true;
    wrapPending = false;
    schedule();
  }

  // ---------- input ----------

  function keySequence(event) {
    const key = event.key;
    const ctrl = event.ctrlKey;
    const alt = event.altKey;
    const cursor = (letter) => (appCursorKeys ? `\u001bO${letter}` : `\u001b[${letter}`);

    switch (key) {
      case 'Enter': return '\r';
      case 'Tab': return event.shiftKey ? '\u001b[Z' : '\t';
      case 'Backspace': return ctrl ? '\u0008' : '\u007f';
      case 'Escape': return '\u001b';
      case 'ArrowUp': return cursor('A');
      case 'ArrowDown': return cursor('B');
      case 'ArrowRight': return cursor('C');
      case 'ArrowLeft': return cursor('D');
      case 'Home': return appCursorKeys ? '\u001bOH' : '\u001b[H';
      case 'End': return appCursorKeys ? '\u001bOF' : '\u001b[F';
      case 'PageUp': return '\u001b[5~';
      case 'PageDown': return '\u001b[6~';
      case 'Insert': return '\u001b[2~';
      case 'Delete': return '\u001b[3~';
      case 'F1': return '\u001bOP';
      case 'F2': return '\u001bOQ';
      case 'F3': return '\u001bOR';
      case 'F4': return '\u001bOS';
      case 'F5': return '\u001b[15~';
      case 'F6': return '\u001b[17~';
      case 'F7': return '\u001b[18~';
      case 'F8': return '\u001b[19~';
      case 'F9': return '\u001b[20~';
      case 'F10': return '\u001b[21~';
      case 'F11': return '\u001b[23~';
      case 'F12': return '\u001b[24~';
      default: break;
    }
    if (key.length !== 1) return null;

    if (ctrl && !alt) {
      const upper = key.toUpperCase();
      if (upper >= 'A' && upper <= 'Z') return String.fromCharCode(upper.charCodeAt(0) - 64);
      const specials = { ' ': '\u0000', '[': '\u001b', '\\': '\u001c', ']': '\u001d', '^': '\u001e', '_': '\u001f', '?': '\u007f' };
      if (specials[key] !== undefined) return specials[key];
      return null;
    }
    if (alt) return `\u001b${key}`;
    return key;
  }

  root.addEventListener('keydown', (event) => {
    // Let the browser handle copy so selections stay usable.
    if ((event.ctrlKey || event.metaKey) && event.shiftKey && ['C', 'V'].includes(event.key.toUpperCase())) return;
    if (event.metaKey && !event.ctrlKey && event.key.length === 1) return;
    const sequence = keySequence(event);
    if (sequence === null) return;
    event.preventDefault();
    autoScroll = true;
    if (handlers.onData) handlers.onData(sequence);
  });

  root.addEventListener('paste', (event) => {
    event.preventDefault();
    const text = (event.clipboardData || window.clipboardData).getData('text');
    if (!text || !handlers.onData) return;
    handlers.onData(bracketedPaste ? `\u001b[200~${text}\u001b[201~` : text);
  });

  root.addEventListener('focus', () => {
    markDirty(cy);
    schedule();
  });
  root.addEventListener('blur', () => {
    markDirty(cy);
    schedule();
  });
  root.addEventListener('scroll', () => {
    autoScroll = root.scrollHeight - root.scrollTop - root.clientHeight < 24;
  });

  const observer = new ResizeObserver(() => fit());
  observer.observe(host);

  // ---------- boot ----------

  resizeBuffers(cols, rows);
  fit();
  paint();

  return {
    write,
    reset,
    fit,
    focus: () => root.focus(),
    element: root,
    get cols() { return cols; },
    get rows() { return rows; },
    writeText: (text) => write(text),
    /** Print a DocMan message in the terminal, dimmed so it reads as ours. */
    notice: (text) => write(`\u001b[2m${text}\u001b[0m\r\n`),
    dispose() {
      observer.disconnect();
      if (frame) cancelAnimationFrame(frame);
      root.remove();
    },
    selection: () => String(window.getSelection() || ''),
  };
}

function clamp(value, low, high) {
  return Math.min(Math.max(value, low), high);
}
