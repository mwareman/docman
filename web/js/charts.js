// Canvas sparklines and gauges. Small, theme-aware, no dependencies.

const cssVar = (name, fallback) => {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || fallback;
};

/**
 * A fixed-length ring of samples drawn as a filled area chart.
 * Usage: const s = sparkline(canvas, { max: 100 }); s.push(value);
 */
export function sparkline(canvas, { capacity = 90, max = null, color = null, min = 0 } = {}) {
  const values = [];
  let peak = 0;

  const draw = () => {
    const ratio = window.devicePixelRatio || 1;
    const width = canvas.clientWidth || 200;
    const height = canvas.clientHeight || 34;
    if (canvas.width !== Math.round(width * ratio) || canvas.height !== Math.round(height * ratio)) {
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
    }
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, width, height);
    if (values.length < 2) return;

    const stroke = color || cssVar('--accent', '#38bdf8');
    const ceiling = max !== null ? max : Math.max(peak, 1) * 1.15;
    const span = Math.max(ceiling - min, 1e-9);
    const stepX = width / (capacity - 1);
    const yOf = (v) => height - 1 - ((Math.min(Math.max(v, min), ceiling) - min) / span) * (height - 2);
    const startIndex = capacity - values.length;

    ctx.beginPath();
    ctx.moveTo(startIndex * stepX, yOf(values[0]));
    for (let i = 1; i < values.length; i++) {
      ctx.lineTo((startIndex + i) * stepX, yOf(values[i]));
    }

    const line = new Path2D();
    line.moveTo(startIndex * stepX, yOf(values[0]));
    for (let i = 1; i < values.length; i++) {
      line.lineTo((startIndex + i) * stepX, yOf(values[i]));
    }

    ctx.lineTo(width, height);
    ctx.lineTo(startIndex * stepX, height);
    ctx.closePath();
    const gradient = ctx.createLinearGradient(0, 0, 0, height);
    gradient.addColorStop(0, withAlpha(stroke, 0.34));
    gradient.addColorStop(1, withAlpha(stroke, 0.02));
    ctx.fillStyle = gradient;
    ctx.fill();

    ctx.strokeStyle = stroke;
    ctx.lineWidth = 1.6;
    ctx.lineJoin = 'round';
    ctx.stroke(line);
  };

  const api = {
    push(value) {
      const v = Number(value) || 0;
      values.push(v);
      while (values.length > capacity) values.shift();
      peak = values.reduce((a, b) => Math.max(a, b), 0);
      draw();
      return api;
    },
    reset() {
      values.length = 0;
      peak = 0;
      draw();
      return api;
    },
    redraw: draw,
    values: () => values.slice(),
  };
  window.addEventListener('resize', draw);
  return api;
}

/** Two-series sparkline, used for read/write and rx/tx pairs. */
export function dualSparkline(canvas, { capacity = 90, colors = null } = {}) {
  const a = [];
  const b = [];

  const draw = () => {
    const ratio = window.devicePixelRatio || 1;
    const width = canvas.clientWidth || 200;
    const height = canvas.clientHeight || 34;
    if (canvas.width !== Math.round(width * ratio)) {
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
    }
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, width, height);
    if (a.length < 2) return;

    const palette = colors || [cssVar('--accent', '#38bdf8'), cssVar('--violet', '#a78bfa')];
    const ceiling = Math.max(1, ...a, ...b) * 1.15;
    const stepX = width / (capacity - 1);

    [a, b].forEach((series, index) => {
      const startIndex = capacity - series.length;
      ctx.beginPath();
      ctx.moveTo(startIndex * stepX, height - 1 - (series[0] / ceiling) * (height - 2));
      for (let i = 1; i < series.length; i++) {
        ctx.lineTo((startIndex + i) * stepX, height - 1 - (series[i] / ceiling) * (height - 2));
      }
      ctx.strokeStyle = palette[index];
      ctx.lineWidth = 1.5;
      ctx.lineJoin = 'round';
      ctx.stroke();
    });
  };

  const api = {
    push(first, second) {
      a.push(Number(first) || 0);
      b.push(Number(second) || 0);
      while (a.length > capacity) a.shift();
      while (b.length > capacity) b.shift();
      draw();
      return api;
    },
    redraw: draw,
  };
  window.addEventListener('resize', draw);
  return api;
}

/** A donut gauge showing a single percentage. */
export function gauge(canvas, { thickness = 8 } = {}) {
  let value = 0;

  const draw = () => {
    const ratio = window.devicePixelRatio || 1;
    const size = Math.min(canvas.clientWidth || 96, canvas.clientHeight || 96);
    canvas.width = Math.round(size * ratio);
    canvas.height = Math.round(size * ratio);
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, size, size);

    const radius = size / 2 - thickness / 2 - 1;
    const centre = size / 2;
    ctx.lineWidth = thickness;
    ctx.lineCap = 'round';

    ctx.beginPath();
    ctx.arc(centre, centre, radius, 0, Math.PI * 2);
    ctx.strokeStyle = cssVar('--surface-3', '#1d2937');
    ctx.stroke();

    if (value > 0) {
      const start = -Math.PI / 2;
      ctx.beginPath();
      ctx.arc(centre, centre, radius, start, start + (Math.min(value, 100) / 100) * Math.PI * 2);
      ctx.strokeStyle = value > 90 ? cssVar('--bad', '#fb7185')
        : value > 70 ? cssVar('--warn', '#f5b544')
        : cssVar('--accent', '#38bdf8');
      ctx.stroke();
    }
  };

  const api = {
    set(next) {
      value = Number(next) || 0;
      draw();
      return api;
    },
    redraw: draw,
  };
  window.addEventListener('resize', draw);
  return api;
}

/** Horizontal stacked bar breaking a total down by contributor. */
export function stackBar(host, parts) {
  host.textContent = '';
  const total = parts.reduce((sum, p) => sum + Math.max(0, p.value), 0) || 1;
  const bar = document.createElement('div');
  bar.style.display = 'flex';
  bar.style.height = '8px';
  bar.style.borderRadius = '999px';
  bar.style.overflow = 'hidden';
  bar.style.background = cssVar('--surface-3', '#1d2937');
  for (const part of parts) {
    if (part.value <= 0) continue;
    const seg = document.createElement('i');
    seg.style.width = `${(part.value / total) * 100}%`;
    seg.style.background = part.color;
    seg.title = `${part.label}: ${part.text || part.value}`;
    bar.append(seg);
  }
  host.append(bar);
  return host;
}

/** Deterministic colour for a container name, so charts stay stable. */
export function seriesColor(key) {
  let hash = 0;
  for (let i = 0; i < key.length; i++) hash = (hash * 31 + key.charCodeAt(i)) | 0;
  const hue = Math.abs(hash) % 360;
  return `hsl(${hue} 68% 58%)`;
}

function withAlpha(color, alpha) {
  const value = color.trim();
  if (value.startsWith('#')) {
    const hex = value.length === 4
      ? value.slice(1).split('').map((c) => c + c).join('')
      : value.slice(1, 7);
    const int = parseInt(hex, 16);
    if (Number.isNaN(int)) return value;
    return `rgba(${(int >> 16) & 255}, ${(int >> 8) & 255}, ${int & 255}, ${alpha})`;
  }
  if (value.startsWith('rgb(')) return value.replace('rgb(', 'rgba(').replace(')', `, ${alpha})`);
  if (value.startsWith('hsl(')) return value.replace('hsl(', 'hsla(').replace(')', ` / ${alpha})`);
  return value;
}
