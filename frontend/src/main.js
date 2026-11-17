import '@fontsource/golos-text/400.css';
import '@fontsource/golos-text/500.css';
import '@fontsource/golos-text/600.css';
import '@fontsource/jetbrains-mono/400.css';
import './style.css';

import { Info, Check, CheckInBrowser, FinishBrowser, Report, SaveReport, CopyReport, CheckUpdate, Update,
  StartLive, FinishLive, CancelLive, MarkLag, ReadHAR, PasteHAR, ReadHARFile, PickHAR, CompareHAR,
  ListPrograms, RecordApp, FinishApp, CancelApp } from '../wailsjs/go/main/App';
import { EventsOn, OnFileDrop, WindowGetPosition, WindowGetSize, WindowSetPosition, WindowSetSize } from '../wailsjs/runtime/runtime';

const GLYPH = { ok: '✓', fail: '✗', error: '✗', warn: '!', skip: '—', wait: '', run: '' };
const TONE = { ok: '✓', warn: '!', bad: '✗' };
const esc = s => String(s ?? '').replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);
const fill = (s, vars) => String(s ?? '').replace(/\{(\w+)\}/g, (m, k) => (k in vars ? vars[k] : m));

const $ = id => document.getElementById(id);
const form = $('ask'), input = $('site'), checkBtn = $('check'), out = $('out'), toastEl = $('toast'), modeEl = $('mode');

const freshLive = () => ({
  phase: 'idle', // idle | starting | recording | done
  target: '', links: [], seconds: 0, lags: 0, hotkey: true,
  series: {}, now: {}, own: {}, view: null, error: '', chart: 'lanes',
});

const freshApp = () => ({
  phase: 'idle', // idle (the picker) | recording | checking | done
  list: [], selected: '', name: '', tick: null, steps: [], view: null, error: '',
  udp: (() => { try { return localStorage.getItem('app-udp') === '1'; } catch { return false; } })(),
});

const state = {
  ui: {}, L: {}, A: {}, version: '', blank: [], appBlank: [], steps: [], hosts: [], slow: [],
  own: null, // the person's own browser record, once compared
  ownModal: null, // { tab: chromium | firefox, status: '' | reading | warn, msg }
  browser: 'chromium', // the default browser: which instructions to show first
  mode: 'site', // site | live | app
  phase: 'idle', // idle | running | done
  host: '', verdict: null, error: '',
  details: false, modal: false, reveal: false,
  visible: false, progress: null, // a check with a visible browser window
  update: null, updating: false, // a newer release offered at the top
  live: freshLive(),
  app: freshApp(),
};

function updateHTML() {
  const u = state.update, ui = state.ui;
  if (!u) return '';
  if (state.updating) return `<div class="update" role="status">${esc(fill(ui.update_busy, u))}</div>`;
  return `<div class="update" role="status"><span>${esc(fill(ui.update_text, u))}</span>
    <span class="actions"><button class="btn primary" data-act="update-yes">${esc(ui.update_yes)}</button>
    <button class="btn" data-act="update-no">${esc(ui.update_no)}</button></span></div>`;
}

function stepsHTML(steps = state.steps) {
  return steps.map(s => `<li class="${s.status}">
      <span class="ico ${s.status}">${GLYPH[s.status] ?? ''}</span>
      <span>${esc(s.name)}</span>
      <span class="ms">${s.status === 'run' ? '…' : s.status === 'wait' || s.status === 'skip' ? ''
        : s.id === 'own' ? esc(fill(state.ui.own_requests, { n: state.own?.requests ?? '' })) : `${s.ms} мс`}</span>
      ${s.note ? `<span class="note">${esc(s.note)}</span>` : ''}
    </li>`).join('');
}

function rightHTML() {
  const ui = state.ui;
  if (state.phase === 'running' && state.visible && state.steps.find(s => s.status === 'run')?.id === 'browser') {
    const p = state.progress;
    return `<section class="verdict" aria-live="polite">
      <h2>${esc(ui.browser_title)}</h2><p>${esc(ui.browser_text)}</p>
      ${p ? `<p class="live">${esc(fill(ui.browser_live, p))}</p>` : ''}
      <div class="actions"><button class="btn primary" data-act="finish">${esc(ui.browser_done)}</button></div></section>`;
  }
  if (state.phase === 'running') {
    const n = state.steps.findIndex(s => s.status === 'run');
    return `<section class="verdict waiting" aria-live="polite"><strong>${esc(fill(ui.checking, { host: state.host }))}</strong>
      <span>${esc(fill(ui.step_of, { n: n < 0 ? state.steps.length : n + 1, total: state.steps.length }))}${n >= 0 ? `: ${esc(state.steps[n].name.toLowerCase())}` : ''}</span></section>`;
  }
  return verdictCard(state.verdict, `<div class="deeper"><div class="deeper-cap">${esc(ui.deeper)}</div><div class="actions">
        <button class="btn sm" data-act="own">${esc(ui.own_open)}</button>
        <button class="btn sm" data-act="in-browser">${esc(ui.in_browser)}</button>
      </div></div>`);
}

// The verdict with its advice and the AI report buttons; extra goes under them.
function verdictCard(v, extra = '') {
  const ui = state.ui;
  return `<section class="verdict tone-${esc(v.tone)}" aria-live="polite">
      <div class="top"><span class="big" aria-hidden="true">${TONE[v.tone] ?? '!'}</span><span class="pill">${esc(v.confidence)}</span></div>
      <h2>${esc(v.title)}</h2><p>${esc(v.explain)}</p>
      <div class="advice"><b>${esc(ui.advice)}</b>${esc(v.advice)}</div>
      <div class="actions"><span class="split">
        <button class="btn primary" data-act="open">${esc(ui.save_report)}</button><button class="btn primary" data-act="copy">⧉ ${esc(ui.copy)}</button>
      </span></div>${extra}</section>`;
}

const moreBarHTML = (hint = state.ui.more_hint) => `<button class="morebar" data-act="details" aria-expanded="${state.details}">${state.details
  ? `${esc(state.ui.hide_details)} ▴` : `${esc(state.ui.details)} <span>— ${esc(hint)}</span> ▾`}</button>`;

function hostsHTML() {
  const ui = state.ui;
  if (!state.hosts.length) return '';
  const rows = state.hosts.map(h => `<tr>
      <td class="host"><span class="dot ${esc(h.tone || 'none')}"></span>${esc(h.host)}</td>
      <td>${esc(h.kinds)}</td>
      <td class="num">${pingCell(h)}</td>
      <td class="num">${h.kbps ? `${h.kbps} ${esc(ui.kbps)}` : '—'}</td>
      <td class="num">${h.requests ? (h.failed ? `${h.requests} <span class="bad">(${h.failed} ✗)</span>` : h.requests) : '—'}</td>
      <td>${esc(h.result || ui.not_checked)}</td>
    </tr>`).join('');
  return `<div class="details-title">${esc(ui.hosts_title)}</div>
    <div class="table-wrap"><table class="hosts">
      <thead><tr><th>${esc(ui.col_host)}</th><th>${esc(ui.col_kinds)}</th><th class="num">${esc(ui.col_ping)}</th>
        <th class="num">${esc(ui.col_speed)}</th><th class="num">${esc(ui.col_requests)}</th><th>${esc(ui.col_result)}</th></tr></thead>
      <tbody>${rows}</tbody></table></div>`;
}

// A server's ping, with the tries that lost a packet.
const pingCell = h => h.ping_ms
  ? `${h.ping_ms} мс${h.lost ? ` · <span class="bad">${esc(fill(state.ui.lost, h))}</span>` : ''}` : '—';
// A file's address for one cell: no scheme, a long path cut in the middle (the full one on hover).
const shortFile = f => { f = f.replace(/^https?:\/\//, ''); return f.length > 70 ? `${f.slice(0, 42)}…${f.slice(-25)}` : f; };

function slowHTML() {
  const ui = state.ui;
  if (!state.slow.length) return '';
  const rows = state.slow.map(f => `<tr>
      <td class="host" title="${esc(f.file)}">${esc(shortFile(f.file))}</td>
      <td>${esc(f.kind)}</td>
      <td class="num">${num(f.ms / 1000)}${f.unfinished ? '+' : ''} ${esc(ui.sec)}</td>
      <td>${esc(f.where)}</td>
      <td class="num">${pingCell(f)}</td>
    </tr>`).join('');
  return `<div class="details-title">${esc(ui.slow_title)}</div>
    <div class="table-wrap"><table class="hosts">
      <thead><tr><th>${esc(ui.col_file)}</th><th>${esc(ui.col_kind)}</th><th class="num">${esc(ui.col_time)}</th>
        <th>${esc(ui.col_where)}</th><th class="num">${esc(ui.col_server_ping)}</th></tr></thead>
      <tbody>${rows}</tbody></table></div>`;
}

// The person's own browser record against whynet's browser.
function ownHTML() {
  const o = state.own, ui = state.ui;
  if (!o) return '';
  const sum = fill(ui.own_sum, o) + (o.shown_ms ? fill(ui.own_shown, { s: num(o.shown_ms / 1000) }) : '');
  const rows = (o.rows || []).map(r => `<tr>
      <td class="host" title="${esc(r.file)}">${esc(shortFile(r.file))}</td>
      <td>${esc(r.kind)}${r.site ? ` <span class="tag site">${esc(ui.own_tag_site)}</span>` : r.ad ? ` <span class="tag">${esc(ui.own_tag_ad)}</span>` : ''}</td>
      <td class="num">${r.ms ? `${num(r.ms / 1000)} ${esc(ui.sec)}` : '—'}</td>
      <td class="bad">${esc(r.what)}</td>
      <td class="${r.whynet_ok ? 'good' : 'dim'}">${esc(r.whynet)}</td>
    </tr>`).join('') + (o.more_ads ? `<tr><td class="dim" colspan="5">${esc(fill(ui.own_more_ads, { n: o.more_ads }))}</td></tr>` : '');
  return `<div class="details-title">${esc(ui.own_table)}</div><div class="own-sum">${esc(sum)}</div>${rows ? `
    <div class="table-wrap"><table class="hosts">
      <thead><tr><th>${esc(ui.col_file)}</th><th>${esc(ui.col_kind)}</th><th class="num">${esc(ui.col_time)}</th>
        <th>${esc(ui.own_col_what)}</th><th>${esc(ui.own_col_whynet)}</th></tr></thead>
      <tbody>${rows}</tbody></table></div>` : ''}`;
}

function detailsHTML() {
  return `<div class="details">${ownHTML()}${slowHTML()}${hostsHTML()}
    <div class="details-title">${esc(state.ui.facts_title)}</div><div class="details-grid">${factCards(state.steps)}</div></div>`;
}

const factCards = steps => steps.map(s =>
  `<div class="fact-card"><b>${GLYPH[s.status] ?? ''} ${esc(s.name)}</b>${(s.facts || []).map(([k, v]) => `<span>${esc(k)}:</span> ${esc(v)}`).join('<br>')}</div>`).join('');

function modalHTML() {
  const ui = state.ui;
  return `<div class="m-back" data-act="backdrop"><div class="m" role="dialog" aria-modal="true" aria-labelledby="m-title">
      <h2 id="m-title">${esc(ui.report_title)}</h2>
      <p>${esc(ui.report_hint)}</p>
      <label><input type="checkbox" id="reveal" ${state.reveal ? 'checked' : ''}> ${esc(ui.reveal)}</label>
      <pre id="preview" tabindex="0"></pre>
      <div class="actions">
        <button class="btn primary" data-act="save">${esc(ui.save)}</button>
        <button class="btn" data-act="copy">${esc(ui.copy)}</button>
        <button class="btn link" data-act="close">${esc(ui.close)}</button>
      </div></div></div>`;
}

// "[F12]" is a key, "**Network**" is bold: the instructions' only markup.
const rich = t => esc(t).replace(/\[([^\]]+)\]/g, '<kbd>$1</kbd>').replace(/\*\*(.+?)\*\*/g, '<b>$1</b>');

function ownModalHTML() {
  const ui = state.ui, m = state.ownModal, ff = m.tab === 'firefox';
  const steps = (ff ? ['own_f1', 'own_f2', 'own_f3'] : ['own_c1', 'own_c2', 'own_c3']).map((k, i) =>
    `<li><span>${rich(ui[k])}${i === 2 ? `<small>${esc(ui[ff ? 'own_f3_hint' : 'own_c3_hint'])}</small>` : ''}</span></li>`).join('');
  return `<div class="m-back" data-act="own-backdrop"><div class="m own" id="own-dialog" role="dialog" aria-modal="true" aria-labelledby="own-title">
      <h2 id="own-title">${esc(ui.own_title)}</h2>
      <p>${esc(ui.own_lead)}</p>
      <div class="seg small" role="tablist">
        <button type="button" role="tab" data-act="own-tab" data-tab="chromium" aria-selected="${!ff}">${esc(ui.own_tab_chromium)}</button>
        <button type="button" role="tab" data-act="own-tab" data-tab="firefox" aria-selected="${ff}">${esc(ui.own_tab_firefox)}</button>
      </div>
      <ol class="howto">${steps}</ol>
      <div class="drop"><b>${rich(ui.own_drop)}</b><span>${esc(ui.own_drop_hint)}</span></div>
      ${m.msg ? `<div class="own-msg ${esc(m.status)}" role="alert">${m.status === 'reading' ? '<span class="spin"></span>' : ''}${esc(m.msg)}</div>` : ''}
      <div class="private">${esc(ui.own_private)}</div>
      <div class="actions end">
        <button class="btn" data-act="own-pick" ${m.status === 'reading' ? 'disabled' : ''}>${esc(ui.own_pick_btn)}</button>
        <button class="btn primary" data-act="own-paste" ${m.status === 'reading' ? 'disabled' : ''}>${esc(ui.own_paste)}</button>
        <button class="btn link" data-act="own-close">${esc(ui.close)}</button>
      </div></div></div>`;
}

/* ---------- Stability check ---------- */

const LINK_COLOR = { router: '#f07468', beyond: '#e6ae52', ctl_ru: '#8fa3b3', ctl_world: '#c2cdd6', server: '#6fb6ff' };
const ENOUGH = 180, MIN_SECONDS = 30, LIVE_POINTS = 240; // 3 minutes; 2 minutes of 0.5 s ticks on the live chart
const CPU_BUSY = 90; // live.CPUBusy
const num = v => v < 10 ? v.toLocaleString('ru-RU', { maximumFractionDigits: 1 }) : String(Math.round(v));
const ms = v => (v == null || v < 0) ? '—' : `${num(v)} ${state.L.ms}`;
const clock = s => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
const linkName = id => state.live.links.find(l => l.id === id)?.name ?? id;

// Lines: one per link, ms on a fixed scale to 550 (game pings and the spikes above them).
function linesSVG(series, ids, n, w, marks = []) {
  const H = 180, max = 550, y = v => H - 8 - (Math.min(v, max) / max) * (H - 16);
  const x = i => n > 1 ? (i / (n - 1)) * w : 0;
  let svg = '';
  for (const g of [100, 250, 500]) svg += `<line x1="0" x2="${w}" y1="${y(g)}" y2="${y(g)}" stroke="#2a353f"/><text x="4" y="${y(g) - 3}" fill="#6d7a85" font-size="10" font-family="JetBrains Mono, monospace">${g}</text>`;
  for (const id of [...ids].reverse()) {
    const s = series[id] || [], off = n - s.length;
    let pts = [], segs = [];
    s.forEach((v, i) => { if (v < 0) { if (pts.length) segs.push(pts); pts = []; } else pts.push(`${x(i + off).toFixed(1)},${y(v).toFixed(1)}`); });
    if (pts.length) segs.push(pts);
    svg += segs.map(p => p.length === 1 ? `<circle cx="${p[0].split(',')[0]}" cy="${p[0].split(',')[1]}" r="1.5" fill="${LINK_COLOR[id]}"/>`
      : `<polyline fill="none" stroke="${LINK_COLOR[id]}" stroke-width="${id === 'router' ? 1.6 : 1.1}" stroke-linejoin="round" vector-effect="non-scaling-stroke" points="${p.join(' ')}"/>`).join('');
  }
  return svg + lagSVG(marks, n, w);
}

// Lanes: one row per link; red where the link caused trouble, pale red where it repeated a nearer link's.
function lanesSVG(chart, ids, n, w, marks = []) {
  const cpu = chart.cpu?.length ? chart.cpu : null;
  const H = 180, top = 6, lh = (H - top * 2) / (ids.length + (cpu ? 1 : 0)), cw = w / Math.max(n, 1);
  let svg = '';
  ids.forEach((id, li) => {
    const y = top + li * lh, s = chart.series[id];
    svg += `<rect x="0" y="${y + 3}" width="${w}" height="${lh - 6}" fill="#1f3529"/>`;
    for (let i = 0; i < n; i++) {
      const lost = s.ms[i] < 0, st = s.state[i];
      if (!st && !lost) continue;
      const own = st === 1;
      const color = lost ? (own || !st ? '#ff4d3d' : '#ff4d3d55') : (own ? '#f07468' : '#f0746855');
      svg += `<rect x="${(i * cw).toFixed(2)}" y="${y + 3}" width="${Math.max(cw, 1.2).toFixed(2)}" height="${lh - 6}" fill="${color}"/>`;
    }
    svg += laneLabel(y, lh, linkName(id));
  });
  // The computer's lane: a bar per point as high as the CPU load, orange from CPU_BUSY up.
  if (cpu) {
    const y = top + ids.length * lh + 3, h = lh - 6;
    svg += `<rect x="0" y="${y}" width="${w}" height="${h}" fill="#1f2a35"/>`;
    cpu.forEach((p, i) => {
      if (p <= 0) return;
      svg += `<rect x="${(i * cw).toFixed(2)}" y="${(y + h - h * p / 100).toFixed(2)}" width="${Math.max(cw, 1.2).toFixed(2)}" height="${(h * p / 100).toFixed(2)}" fill="${p >= CPU_BUSY ? '#f0a35e' : '#4f6a85'}"/>`;
    });
    svg += laneLabel(y - 3, lh, state.L.link_cpu);
  }
  return svg + lagSVG(marks, n, w);
}

function laneLabel(y, lh, text) {
  return `<text x="8" y="${y + lh / 2 + 4}" fill="#e2e8ec" font-size="11.5" font-family="Golos Text, Segoe UI, sans-serif" paint-order="stroke" stroke="#1c252e" stroke-width="3">${esc(text)}</text>`;
}

// A "лагнуло" mark: the line where the keys were pressed, and the 10 s before it shaded — where
// whynet looked for the jump the person meant (people press a second or three late).
function lagSVG(marks, n, w) {
  const x = i => (n > 1 ? (i / (n - 1)) * w : 0);
  return marks.map(({ i, back }) => {
    const x0 = x(Math.max(0, i - back)), x1 = x(i);
    return `<rect x="${x0}" y="0" width="${Math.max(0, x1 - x0)}" height="180" fill="#c792ea" opacity=".13"/>
      <line x1="${x1}" x2="${x1}" y1="0" y2="180" stroke="#c792ea" stroke-width="1.5" stroke-dasharray="4 3" vector-effect="non-scaling-stroke"/>
      <rect x="${x1 - 26}" y="2" width="52" height="15" rx="4" fill="#c792ea"/><text x="${x1}" y="13" text-anchor="middle" fill="#1b1124" font-size="10.5" font-weight="600" font-family="Golos Text, Segoe UI, sans-serif">${esc(state.L.mark?.toLowerCase())}</text>`;
  }).join('');
}

function legendHTML(view) {
  const L = state.L;
  if (view === 'lanes') return `<span><span class="lane-key" style="background:#1f3529"></span>${esc(L.legend_calm)}</span>
    <span><span class="lane-key" style="background:#f07468"></span>${esc(L.legend_own)}</span>
    <span><span class="lane-key" style="background:#f0746855"></span>${esc(L.legend_repeat)}</span>
    <span><span class="lane-key" style="background:#ff4d3d"></span>${esc(L.legend_lost)}</span>
    <span><span class="lane-key" style="background:#c792ea"></span>${esc(L.legend_lag)}</span>`
    + (state.live.view?.chart.cpu?.length ? `<span><span class="lane-key" style="background:#4f6a85"></span>${esc(L.legend_cpu)}</span>
    <span><span class="lane-key" style="background:#f0a35e"></span>${esc(L.legend_cpu_busy)}</span>` : '');
  return state.live.links.map(l => `<span><span class="key" style="background:${LINK_COLOR[l.id]}"></span>${esc(l.name)}</span>`).join('');
}

function liveChainRecHTML() {
  const lv = state.live;
  return lv.links.map(l => `<li id="link-${l.id}"><span class="ico run"></span>
      <span class="what"><span class="key" style="background:${LINK_COLOR[l.id]}"></span>${esc(l.name)}<small>${esc(l.addr)}</small></span>
      <span class="num"></span></li>`).join('');
}

// Numbers change in place: replacing the rows every second would restart the spinners and jerk them.
function patchLiveChain() {
  const lv = state.live, L = state.L;
  for (const l of lv.links) {
    const li = $(`link-${l.id}`);
    if (!li) continue;
    const own = lv.own[l.id] || 0, ico = li.querySelector('.ico');
    li.classList.toggle('bad', own > 0);
    if (own > 0 !== ico.classList.contains('warn')) {
      ico.className = `ico ${own > 0 ? 'warn' : 'run'}`;
      ico.textContent = own > 0 ? '!' : '';
    }
    const html = `${esc(L.now)} <b>${ms(lv.now[l.id])}</b><br>${esc(fill(L.own, { n: own }))}`;
    const num = li.querySelector('.num');
    if (num.dataset.v !== html) { num.innerHTML = html; num.dataset.v = html; }
  }
}

function progressKey(sec) { return `${sec < MIN_SECONDS}${sec >= ENOUGH}`; }

function progressHTML(sec) {
  const L = state.L, early = sec < MIN_SECONDS, full = sec >= ENOUGH;
  return `<div class="bar"><div class="fill ${early ? 'early' : ''}" style="width:${Math.min(100, sec / ENOUGH * 100)}%"></div>
      <div class="tick" style="left:${MIN_SECONDS / ENOUGH * 100}%"></div>${full ? '' : '<div class="tick" style="left:calc(100% - 2px)"></div>'}</div>
    <div class="labels"><span style="left:calc(${MIN_SECONDS / ENOUGH * 100}% - 26px)" class="${early ? '' : 'ok'}">${esc(L.progress_verdict)}</span>
      <span style="right:0" class="${full ? 'ok' : ''}">${esc(full ? L.progress_full : L.progress_enough)}</span></div>`;
}

function liveRecHTML() {
  const lv = state.live, L = state.L, sec = lv.seconds;
  return `<div class="grid"><ol class="chain" id="live-chain">${liveChainRecHTML()}</ol>
    <section class="verdict" aria-live="off">
      <div class="rec"><span class="dotrec"></span><span class="t" id="live-time">${clock(sec)}</span><span class="muted">${esc(L.recording)}</span>
        <span class="lags" id="live-lags">${lv.lags ? esc(fill(L.lag_count, { n: lv.lags })) : ''}</span></div>
      <div class="prog" id="live-prog">${progressHTML(sec)}</div>
      <div class="chartbox"><svg class="chart" id="live-chart" viewBox="0 0 600 180" preserveAspectRatio="none"></svg></div>
      <div class="legend">${legendHTML('lines')}<span>· ${esc(L.chart_live)}</span></div>
      <p class="muted" id="live-hint"></p>
      ${lv.hotkey ? '' : `<p class="muted">${esc(L.hotkey_busy)}</p>`}
      <div class="actions"><button class="btn primary" data-act="live-done" id="live-done"></button>
        ${lv.hotkey ? '' : `<button class="btn" data-act="live-mark">${esc(L.mark)}</button>`}
        <button class="btn link" data-act="live-cancel">${esc(L.cancel)}</button></div>
    </section></div>`;
}

// Patches the recording screen in place: a full render every second would pull the buttons from under the cursor.
function updateLiveRec() {
  const lv = state.live, L = state.L, sec = lv.seconds;
  if (lv.phase !== 'recording' || !$('live-time')) return;
  $('live-time').textContent = clock(sec);
  $('live-lags').textContent = lv.lags ? fill(L.lag_count, { n: lv.lags }) : '';
  const prog = $('live-prog'), key = progressKey(sec);
  if (prog.dataset.k !== key) { prog.innerHTML = progressHTML(sec); prog.dataset.k = key; }
  else prog.querySelector('.fill').style.width = `${Math.min(100, sec / ENOUGH * 100)}%`;
  patchLiveChain();
  const early = sec < MIN_SECONDS, done = $('live-done');
  done.disabled = early;
  done.textContent = early ? fill(L.done_in, { n: MIN_SECONDS - sec }) : L.done;
  $('live-hint').textContent = early ? fill(L.rec_hint_early, { n: MIN_SECONDS - sec }) : L.rec_hint;
  const n = Math.min(LIVE_POINTS, Math.max(0, ...Object.values(lv.series).map(s => s.length)));
  const recent = Object.fromEntries(Object.entries(lv.series).map(([id, s]) => [id, s.slice(-LIVE_POINTS)]));
  $('live-chart').innerHTML = linesSVG(recent, lv.links.map(l => l.id), n, 600);
  fitWindow(); // the hint changes length once the verdict is available: the window follows
}

function liveDoneChainHTML(v) {
  const L = state.L;
  return v.links.map(l => {
    const bad = l.state === 'unstable', silent = l.state === 'silent';
    const sub = l.stand_in ? fill(L.stand_in, { addr: l.stand_in }) : silent ? L.silent : '';
    return `<li class="${bad ? 'bad' : silent ? 'warn' : ''}"><span class="ico ${bad ? 'fail' : silent ? 'warn' : 'ok'}">${bad ? '!' : silent ? '—' : '✓'}</span>
      <span class="what">${esc(l.name)}<small>${esc(l.addr)}</small></span>
      <span class="num">${silent ? '' : `${esc(L.usual)} <b>${ms(l.median)}</b><br>${esc(L.up_to)} <b>${ms(l.worst)}</b><br>`}${esc(fill(L.own, { n: l.own }))}</span>
      ${sub ? `<span class="sub">${esc(sub)}</span>` : ''}</li>`;
  }).join('');
}

function compareHTML(c) {
  const L = state.L;
  return `<div class="table-wrap"><table class="hosts compare">
    <thead><tr><th>${esc(c.link)}</th><th class="num">${esc(L.compare_before)}</th><th class="num">${esc(L.compare_after)}</th></tr></thead>
    <tbody>${c.rows.map(r => `<tr><td>${esc(r.label)}</td><td class="num">${esc(r.before)}</td><td class="num ${r.better ? 'good' : r.worse ? 'bad' : ''}">${esc(r.after)}</td></tr>`).join('')}</tbody></table></div>`;
}

function liveDoneHTML() {
  const v = state.live.view, L = state.L, ui = state.ui, d = v.verdict;
  return `<div class="grid"><div><ol class="chain">${liveDoneChainHTML(v)}</ol>
      <div class="version">whynet ${esc(state.version)} · ${clock(v.seconds)}</div></div>
    <section class="verdict tone-${esc(d.tone)}" aria-live="polite">
      <div class="top"><span class="big" aria-hidden="true">${TONE[d.tone] ?? '!'}</span><span class="pill">${esc(d.confidence)}</span></div>
      <h2>${esc(d.title)}</h2><p>${esc(d.explain)}</p>
      ${v.extras?.length ? `<div class="extras">${v.extras.map(x => `<span>${esc(x)}</span>`).join('')}</div>` : ''}
      ${v.note ? `<p class="muted">${esc(v.note)}</p>` : ''}
      ${v.compare ? compareHTML(v.compare) : ''}
      <div class="advice"><b>${esc(ui.advice)}</b>${esc(d.advice)}</div>
      <div class="actions">
        <button class="btn primary" data-act="open">${esc(ui.save_report)}</button>
        <button class="btn" data-act="live-again">${esc(L.again)}</button>
        <button class="btn" data-act="copy">${esc(ui.copy)}</button>
        <button class="btn link" data-act="details" aria-expanded="${state.details}">${esc(state.details ? ui.hide_details : ui.details)} ${state.details ? '▴' : '▾'}</button>
      </div></section></div>
    ${state.details ? liveDetailsHTML(v) : ''}`;
}

function liveDetailsHTML(v) {
  const L = state.L, view = state.live.chart;
  const links = `<div class="table-wrap"><table class="hosts"><thead><tr><th>${esc(L.col_link)}</th><th>${esc(L.col_addr)}</th>
      <th class="num">${esc(L.col_usual)}</th><th class="num">${esc(L.col_p95)}</th><th class="num">${esc(L.col_worst)}</th>
      <th class="num">${esc(L.col_loss)}</th><th class="num">${esc(L.col_own)}</th></tr></thead><tbody>
      ${v.links.map(l => { const bad = l.state === 'unstable'; return `<tr><td><span class="dot ${bad ? 'bad' : l.state === 'silent' ? 'warn' : 'ok'}"></span>${esc(l.name)}</td>
        <td class="host">${esc(l.addr)}${l.stand_in ? ` → ${esc(l.stand_in)}` : ''}</td><td class="num">${ms(l.median)}</td>
        <td class="num ${bad ? 'bad' : ''}">${ms(l.p95)}</td><td class="num ${bad ? 'bad' : ''}">${ms(l.worst)}</td>
        <td class="num">${num(l.loss_pct)} %</td><td class="num ${bad ? 'bad' : ''}">${l.own}</td></tr>`; }).join('')}</tbody></table></div>`;
  const trace = v.trace?.length ? `<div class="details-title">${esc(v.trace_title)}</div>
    <div class="table-wrap"><table class="hosts"><thead><tr><th class="num">${esc(L.col_ttl)}</th><th>${esc(L.col_node)}</th><th>${esc(L.col_network)}</th>
      <th class="num">${esc(L.col_loss)}</th><th class="num">${esc(L.col_trace_ms)}</th><th>${esc(L.col_note)}</th></tr></thead><tbody>
      ${v.trace.map(h => `<tr><td class="num">${h.ttl}</td><td class="host">${esc(h.addr)}</td><td>${esc(h.network)}</td>
        <td class="num">${h.lost} / ${h.sent}</td><td class="num">${h.addr === '*' ? '—' : `${num(h.median)} / ${num(h.worst)} ${esc(L.ms)}`}</td>
        <td class="${h.bad ? 'bad' : ''}">${esc(h.note)}</td></tr>`).join('')}</tbody></table></div>` : '';
  const w = v.wifi?.last;
  const wifi = w ? `<div class="fact-card"><b>${esc(L.wifi_title)}</b><span>${esc(L.wifi_signal)}:</span> ${v.wifi.avg_signal_pct} %${w.rssi_dbm ? ` (${w.rssi_dbm} dBm)` : ''}<br>
      <span>${esc(L.wifi_band)}:</span> ${esc((w.band || '?').replace('.', ','))} ${esc(L.ghz)}${w.channel ? `, ${esc(L.wifi_channel)} ${w.channel}` : ''}<br>
      ${w.phy ? `<span>${esc(L.wifi_phy)}:</span> ${esc(w.phy)}<br>` : ''}${w.rx_mbps ? `<span>${esc(L.wifi_rate)}:</span> ${w.rx_mbps} ${esc(L.mbps)}<br>` : ''}
      <span>${esc(L.wifi_ssid_hidden)}</span></div>` : '';
  const lags = v.lags?.length ? `<div class="fact-card"><b>${esc(L.lags_title)}</b>${v.lags.map(m => `<span>${esc(m.time)}:</span> ${esc(m.text)}`).join('<br>')}</div>` : '';
  const cpu = v.cpu ? `<div class="fact-card"><b>${esc(L.link_cpu)}</b>${esc(fill(L.cpu_line, { avg: v.cpu.avg_pct, max: v.cpu.max_pct, busy: v.cpu.busy_pct.toLocaleString('ru-RU', { minimumFractionDigits: 1, maximumFractionDigits: 1 }) }))}</div>` : '';
  return `<div class="details">
    <div class="charthead"><div class="details-title">${esc(L.chart_title)}</div>
      <div class="seg small" role="tablist"><button type="button" data-act="view-lanes" aria-selected="${view === 'lanes'}">${esc(L.view_lanes)}</button>
        <button type="button" data-act="view-lines" aria-selected="${view === 'lines'}">${esc(L.view_lines)}</button></div></div>
    <div class="chartbox hover" id="full-box"><svg class="chart" id="full-chart" viewBox="0 0 900 180" preserveAspectRatio="none"></svg><div class="tip" id="tip" hidden></div></div>
    <div class="legend">${legendHTML(view)}</div>
    <div class="details-title">${esc(L.links_title)}</div>${links}${trace}
    ${wifi || cpu || lags ? `<div class="details-grid">${wifi}${cpu}${lags}</div>` : ''}</div>`;
}

function drawFullChart() {
  const v = state.live.view, svg = $('full-chart');
  if (!v || !svg) return;
  const ids = v.links.map(l => l.id), n = Math.max(...ids.map(id => v.chart.series[id]?.ms.length || 0));
  const marks = (v.lags || []).map(m => ({ i: Math.round(m.at / v.chart.step), back: Math.round(m.back / v.chart.step) }));
  svg.innerHTML = (state.live.chart === 'lanes' ? lanesSVG(v.chart, ids, n, 900, marks)
    : linesSVG(Object.fromEntries(ids.map(id => [id, v.chart.series[id].ms])), ids, n, 900, marks))
    + '<line id="cursor" x1="-10" x2="-10" y1="0" y2="180" stroke="#e2e8ec" opacity=".5" vector-effect="non-scaling-stroke"/>';
}

// Hover over the whole-session chart: the moment to the second, every link's ping, who was to blame.
function chartHover(e) {
  const v = state.live.view, svg = $('full-chart'), tip = $('tip'), box = $('full-box');
  if (!v || !svg) return;
  const L = state.L, ids = v.links.map(l => l.id), n = v.chart.series[ids[0]].ms.length;
  const rect = svg.getBoundingClientRect();
  const i = Math.max(0, Math.min(n - 1, Math.round((e.clientX - rect.left) / rect.width * (n - 1))));
  const x = n > 1 ? i / (n - 1) * 900 : 0, cur = $('cursor');
  cur.setAttribute('x1', x); cur.setAttribute('x2', x);
  const time = new Date(v.chart.start + i * v.chart.step * 1000).toLocaleTimeString('ru-RU');
  const blame = ids.find(id => v.chart.series[id].state[i] === 1);
  const at = i * v.chart.step, lag = (v.lags || []).find(m => at >= m.at - m.back && at <= m.at + 1);
  tip.innerHTML = `<div class="tt">${esc(time)}</div>` + v.links.map(l => {
    const val = v.chart.series[l.id].ms[i];
    return `<div class="row"><span><span class="key" style="background:${LINK_COLOR[l.id]}"></span>${esc(l.name)}</span><span>${val < 0 ? esc(L.lost) : ms(val)}</span></div>`;
  }).join('') + (v.chart.cpu?.length ? `<div class="row"><span><span class="key" style="background:#4f6a85"></span>${esc(L.cpu_title)}</span><span>${v.chart.cpu[i] < 0 ? '—' : `${v.chart.cpu[i]} %`}</span></div>` : '')
    + (lag ? `<div class="why lag">${esc(L.tip_lag)} ${esc(lag.time)}: ${esc(lag.text)}</div>` : '')
    + (blame ? `<div class="why">${fill(esc(L.tip_blame), { link: `<b>${esc(linkName(blame))}</b>` })}</div>` : `<div class="why calm">${esc(L.tip_calm).replace(/:\s*(.+)$/, ': <b>$1</b>')}</div>`);
  tip.hidden = false;
  const bx = box.getBoundingClientRect(), px = e.clientX - bx.left, tw = tip.offsetWidth;
  tip.style.left = `${Math.max(0, px + 14 + tw > bx.width ? px - tw - 14 : px + 14)}px`;
  tip.style.top = `${Math.max(0, e.clientY - bx.top - 40)}px`;
}

function liveHTML() {
  const lv = state.live, L = state.L;
  switch (lv.phase) {
    case 'starting': return `<p class="hint">${esc(L.starting)}</p>`;
    case 'recording': return liveRecHTML();
    case 'done': return liveDoneHTML();
  }
  return `<p class="hint">${esc(L.idle_text)}</p><p class="hint">${esc(L.idle_lag)}</p>${lv.error ? `<p class="hint error-text" role="alert">${esc(lv.error)}</p>` : ''}`;
}

async function startLive(target, compare) {
  const lv = state.live;
  if (state.phase === 'running' || lv.phase === 'starting' || lv.phase === 'recording') return;
  const chart = lv.chart;
  state.live = { ...freshLive(), phase: 'starting', target, chart };
  state.details = false;
  render();
  try {
    const s = await StartLive(target, compare);
    Object.assign(state.live, { phase: 'recording', links: s.links, hotkey: s.hotkey });
    render();
  } catch (err) {
    Object.assign(state.live, { phase: 'idle', error: err?.message ?? String(err) });
    render();
  }
}

function showLiveResult(view) {
  Object.assign(state.live, { phase: 'done', view, links: view.links });
  render();
}

EventsOn('live-tick', p => {
  const lv = state.live;
  if (lv.phase !== 'recording') return;
  lv.seconds = p.seconds;
  lv.lags = p.lags;
  for (const l of p.links) { lv.now[l.id] = l.ms; lv.own[l.id] = l.own; }
  for (const [id, vals] of Object.entries(p.new || {})) {
    const s = (lv.series[id] ||= []);
    s.push(...vals);
    if (s.length > LIVE_POINTS * 2) s.splice(0, s.length - LIVE_POINTS);
  }
  updateLiveRec();
});
EventsOn('live-lag', () => { if (state.live.phase === 'recording') { state.live.lags++; updateLiveRec(); } });
EventsOn('live-done', view => { if (state.live.phase === 'recording') showLiveResult(view); }); // an hour has passed

/* ---------- App check ---------- */

const toneClass = t => ({ bad: 'bad', warn: 'warnc', ok: 'good' })[t] ?? 'dim';
// A program's letter tile: its colour from its name, so it stays the same between lists.
const tileColor = name => `hsl(${[...name].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7)} 45% 38%)`;

let programsTimer = 0;
async function loadPrograms() {
  try { state.app.list = (await ListPrograms()) || []; } catch (err) { state.app.error = err?.message ?? String(err); }
  if (state.mode === 'app' && state.app.phase === 'idle') render();
}
// While the picker is open, the list follows programs started and closed.
function watchPrograms(on) {
  clearInterval(programsTimer);
  if (!on) return;
  loadPrograms();
  programsTimer = setInterval(() => { if (state.app.phase === 'idle' && !document.hidden) loadPrograms(); }, 4000);
}

function appPickerHTML() {
  const A = state.A, ap = state.app, q = input.value.trim().toLowerCase();
  const match = p => !q || p.name.toLowerCase().includes(q) || p.exe.toLowerCase().includes(q) || (p.title || '').toLowerCase().includes(q);
  const row = p => `<button type="button" class="pitem${p.exe === ap.selected ? ' on' : ''}${p.browser ? ' dim' : ''}" data-act="app-pick" data-exe="${esc(p.exe)}" aria-selected="${p.exe === ap.selected}">
      <span class="pico" style="background:${tileColor(p.name)}" aria-hidden="true">${esc((p.name[0] || '?').toUpperCase())}</span>
      <span class="pname"><b>${esc(p.name)}</b><small>${esc(p.title && p.title !== p.name ? p.title : p.exe)}</small></span>
      <span class="pconns">${p.conns ? esc(fill(A.conns, { n: p.conns })) : esc(A.no_conns)}</span></button>`;
  const progs = ap.list.filter(p => !p.browser && match(p)), browsers = ap.list.filter(p => p.browser && match(p));
  return `<p class="hint">${esc(A.idle)}</p>${ap.error ? `<p class="hint error-text" role="alert">${esc(ap.error)}</p>` : ''}
    <div class="plist" role="listbox">${progs.map(row).join('')}
      ${browsers.length ? `<div class="pgrp">${esc(A.browsers)}</div>${browsers.map(row).join('')}` : ''}</div>
    ${progs.length || browsers.length ? '' : `<p class="hint">${esc(A.none_found)}</p>`}
    <label class="udpopt"><input type="checkbox" id="app-udp"${ap.udp ? ' checked' : ''}><span><b>${esc(A.udp_opt_title)}</b>${esc(A.udp_opt_text)}</span></label>
    <p class="hint small">${esc(A.idle_note)}</p>`;
}

function appRecHTML() {
  const A = state.A, ap = state.app;
  return `<section class="apprec" aria-live="off">
    <div class="rec"><span class="dotrec"></span><span class="t" id="app-time">${clock(ap.tick?.seconds ?? 0)}</span>
      <span class="muted">${esc(fill(A.rec_title, { name: ap.name }))}</span>
      <span class="actions push"><button class="btn link" data-act="app-cancel">${esc(A.cancel)}</button>
        <button class="btn primary" data-act="app-done">${esc(A.done)}</button></span></div>
    <p class="muted">${esc(fill(A.rec_hint, { name: ap.name }))}</p>
    <div id="app-rec">${appRecBodyHTML()}</div></section>`;
}

function appRecBodyHTML() {
  const A = state.A, t = state.app.tick;
  if (!t) return `<p class="muted">${esc(state.app.udp ? A.udp_wait_uac : A.rec_wait)}</p>`;
  if (!t.servers?.length) return `<p class="muted">${esc(A.rec_wait)}</p>`;
  const udp = t.udp ? ` · ${esc(fill(A.udp_count, { sent: t.udp_sent, recv: t.udp_recv }))}` : '';
  return `<div class="muted">${esc(fill(A.count, { servers: t.servers.length, conns: t.conns, cut: t.cut, stuck: t.stuck }))}${udp}</div>
    <div class="table-wrap"><table class="hosts"><thead><tr><th>${esc(A.col_server)}</th><th class="num">${esc(A.col_conns)}</th><th>${esc(A.col_seen)}</th></tr></thead>
      <tbody>${t.servers.map(s => `<tr><td class="host">${protoTag(s)}${esc(s.addr)}</td><td class="num">${s.proto === 'UDP' ? '—' : s.conns}</td>
        <td class="${toneClass(s.seen_tone)}">${esc(s.seen)}</td></tr>`).join('')}</tbody></table></div>`;
}

const protoTag = s => `<span class="proto${s.proto === 'UDP' ? ' u' : ''}">${esc(s.proto)}</span>`;

function appDoneHTML() {
  const v = state.app.view;
  const note = v.udp_note ? `<p class="hint small">${esc(v.udp_note)}</p>` : '';
  return `<div class="grid"><div><ol class="steps">${stepsHTML(v.steps)}</ol><div class="version">whynet ${esc(state.version)}</div></div>${verdictCard(v.verdict, note)}</div>`
    + moreBarHTML(fill(state.A.details_hint, { name: v.program })) + (state.details ? appDetailsHTML(v) : '');
}

function appDetailsHTML(v) {
  const A = state.A;
  const rows = (v.servers || []).map(s => `<tr>
      <td class="host"><span class="dot ${esc(s.tone || 'none')}"></span>${protoTag(s)}${esc(s.label)}${s.ad ? ` <span class="tag">${esc(A.ad)}</span>` : ''}<small>${esc(s.name_from)}</small></td>
      <td class="host">${esc(s.addr)}</td><td>${esc(s.owner || '—')}</td>
      <td class="${toneClass(s.seen_tone)}">${esc(s.seen)}</td><td class="${toneClass(s.tone)}">${esc(s.check)}</td>
      <td class="num">${esc(s.ping || '—')}</td>${v.zapret ? `<td>${esc(s.zapret || '—')}</td>` : ''}</tr>`).join('');
  return `<div class="details"><div class="own-sum">${esc(v.summary)}</div>
    <div class="details-title">${esc(A.servers_title)}</div>
    <div class="table-wrap"><table class="hosts"><thead><tr><th>${esc(A.col_server)}</th><th>${esc(A.col_addr)}</th><th>${esc(A.col_owner)}</th>
      <th>${esc(A.col_seen)}</th><th>${esc(A.col_check)}</th><th class="num">${esc(A.col_ping)}</th>${v.zapret ? `<th>${esc(A.col_zapret)}</th>` : ''}</tr></thead>
      <tbody>${rows}</tbody></table></div>
    <div class="details-title">${esc(state.ui.facts_title)}</div><div class="details-grid">${factCards(v.steps)}</div></div>`;
}

function appHTML() {
  const ap = state.app;
  switch (ap.phase) {
    case 'recording': return appRecHTML();
    case 'checking': return `<div class="grid"><div><ol class="steps">${stepsHTML(ap.steps)}</ol></div>
      <section class="verdict waiting" aria-live="polite"><strong>${esc(fill(state.A.checking, { name: ap.name }))}</strong></section></div>`;
    case 'done': return appDoneHTML();
  }
  return appPickerHTML();
}

async function startApp() {
  const ap = state.app, A = state.A;
  if (state.phase === 'running' || ['recording', 'checking'].includes(ap.phase)) return;
  const q = input.value.trim().toLowerCase();
  let p = ap.list.find(x => x.exe === ap.selected);
  if (!p && q) p = ap.list.find(x => !x.browser && (x.name.toLowerCase().includes(q) || x.exe.toLowerCase().includes(q)));
  if (!p) { toast(A.pick_first); return; }
  if (p.browser) { toast(fill(A.browser, { name: p.name })); return; }
  Object.assign(ap, { phase: 'recording', selected: p.exe, name: p.name, tick: null, view: null, error: '', steps: [] });
  input.value = p.name;
  state.details = false;
  render();
  try {
    const view = await RecordApp(p.exe, !!ap.udp);
    if (view) Object.assign(ap, { phase: 'done', view });
    else { ap.phase = 'idle'; loadPrograms(); } // cancelled
  } catch (err) {
    Object.assign(ap, { phase: 'idle', error: err?.message ?? String(err) });
    loadPrograms();
  }
  render();
}

// The recording screen is patched in place: a full render every second would pull the buttons from under the cursor.
EventsOn('app-tick', t => {
  const ap = state.app;
  if (ap.phase !== 'recording') return;
  ap.tick = t;
  const time = $('app-time'), body = $('app-rec');
  if (time) time.textContent = clock(t.seconds);
  if (body) { body.innerHTML = appRecBodyHTML(); fitWindow(); }
});

// The check's steps after the recording; the first one ends the recording screen.
EventsOn('app-step', s => {
  const ap = state.app;
  if (ap.phase === 'recording') Object.assign(ap, { phase: 'checking', steps: state.appBlank.map((x, i) => ({ ...x, status: i === 0 ? 'run' : 'wait' })) });
  if (ap.phase !== 'checking') return;
  const i = ap.steps.findIndex(x => x.id === s.id);
  if (i < 0) return;
  ap.steps[i] = s;
  if (ap.steps[i + 1]) ap.steps[i + 1] = { ...ap.steps[i + 1], status: 'run' };
  render();
});

/* ---------- Window ---------- */

function render() {
  const live = state.mode === 'live', app = state.mode === 'app';
  // Before the first check the window is just the address bar and one line of help.
  const site = state.phase === 'idle'
    ? `<p class="hint">${esc(state.ui.idle_text)}</p>${state.error ? `<p class="hint error-text" role="alert">${esc(state.error)}</p>` : ''}`
    : `<div class="grid"><div><ol class="steps">${stepsHTML()}</ol><div class="version">whynet ${esc(state.version)}</div></div>${rightHTML()}</div>`
      + (state.phase === 'done' ? moreBarHTML() : '') + (state.details && state.phase === 'done' ? detailsHTML() : '');
  out.innerHTML = `<div class="content" id="content">${updateHTML()}${live ? liveHTML() : app ? appHTML() : site}</div>`
    + (state.modal ? modalHTML() : '') + (state.ownModal ? ownModalHTML() : '');
  const busy = state.phase === 'running' || ['starting', 'recording'].includes(state.live.phase) || ['recording', 'checking'].includes(state.app.phase);
  checkBtn.disabled = busy;
  input.disabled = (live || app) && busy;
  checkBtn.textContent = live ? state.L.start : app ? state.A.record : state.ui.check;
  input.placeholder = live ? state.L.placeholder : app ? state.A.placeholder : state.ui.placeholder;
  for (const b of modeEl.querySelectorAll('button')) {
    b.setAttribute('aria-selected', String(b.dataset.mode === state.mode));
    b.disabled = busy && b.dataset.mode !== state.mode;
  }
  if (live && state.live.phase === 'recording') updateLiveRec();
  if (live && state.live.phase === 'done' && state.details) drawFullChart();
  if (state.modal) refreshPreview();
  fitWindow();
}

// The window grows and shrinks with what it shows, up to 90% of the screen; past that it scrolls.
let frame = null;
let narrowW = null; // the width before "Details" widened the window: back to it when they close
const MAX_W = 1400; // wider than this a table wraps its longest cells instead

// How many px the widest table in "Details" lacks to show every column without scrolling.
function lackWidth() {
  let lack = 0;
  for (const t of document.querySelectorAll('.details table')) {
    t.style.width = 'max-content';
    lack = Math.max(lack, t.offsetWidth - t.parentElement.clientWidth);
    t.style.width = '';
  }
  return lack;
}

async function fitWindow(again = true) {
  try {
    const size = await WindowGetSize();
    if (frame === null) {
      frame = size.h - window.innerHeight; // title bar and borders
      if (!(frame >= 0 && frame < 150)) frame = 40;
    }
    // Width: "Details" open widens the window to its tables (up to the screen), closing them narrows it back.
    let w = size.w;
    const shown = state.mode === 'site' ? state.phase === 'done' : state.mode === 'app' && state.app.phase === 'done';
    const lack = state.details && shown ? lackWidth() : 0;
    if (lack > 2) {
      narrowW ??= size.w;
      w = Math.max(size.w, Math.min(Math.floor(screen.availWidth * 0.95), MAX_W, size.w + lack));
    } else if (!state.details && narrowW) {
      w = narrowW;
      narrowW = null;
    }
    const want = form.offsetHeight + $('content').offsetHeight + frame;
    const dialog = $('own-dialog');
    const h = Math.min(Math.floor(screen.availHeight * 0.9), Math.max(want, state.modal ? 620 : 0, dialog ? dialog.scrollHeight + 32 + frame : 0));
    if (Math.abs(size.h - h) <= 2 && w === size.w) return;
    // Growing keeps the top-left corner, unless the window would go off the screen: then it moves up or left.
    const pos = await WindowGetPosition();
    const x = pos.x + w > screen.availWidth ? Math.max(0, screen.availWidth - w) : pos.x;
    const y = pos.y + h > screen.availHeight ? Math.max(0, screen.availHeight - h) : pos.y;
    if (x !== pos.x || y !== pos.y) WindowSetPosition(x, y);
    WindowSetSize(w, h);
    if (w !== size.w && again) setTimeout(() => fitWindow(false), 80); // wider rows wrap less: refit the height once
  } catch { /* outside Wails (vite preview): nothing to resize */ }
}

async function refreshPreview() {
  const pre = $('preview');
  if (pre) pre.textContent = await Report(!state.reveal);
}

let toastTimer = 0;
function toast(text) {
  toastEl.textContent = text;
  toastEl.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toastEl.hidden = true; }, 3500);
}
const failed = err => toast(fill(state.ui.failed, { error: err?.message ?? err }));

// Each finished step arrives as an event; the next one starts spinning.
EventsOn('step', s => {
  const i = state.steps.findIndex(x => x.id === s.id);
  if (i < 0 || state.phase !== 'running') return;
  state.steps[i] = s;
  if (state.steps[i + 1]) state.steps[i + 1] = { ...state.steps[i + 1], status: 'run' };
  render();
});

EventsOn('browser-progress', p => {
  if (!state.visible || state.phase !== 'running') return;
  state.progress = p;
  const live = document.querySelector('.live');
  if (live) live.textContent = fill(state.ui.browser_live, p); // no full render: keep the button under the cursor
  else render();
});

async function startCheck(site, visible) {
  if (!site || state.phase === 'running') return;
  running(site, visible);
  try {
    const view = await (visible ? CheckInBrowser(site) : Check(site));
    showView(view);
    if (visible && view.steps.find(s => s.id === 'browser')?.facts?.some(([k, v]) => k === 'br.status' && v === 'unavailable')) {
      toast(state.ui.browser_missing);
    }
  } catch (err) {
    Object.assign(state, { phase: 'idle', error: err?.message ?? String(err), steps: state.blank });
  }
  state.visible = false;
  render();
}

function running(site, visible) {
  Object.assign(state, { phase: 'running', host: site, error: '', details: false, modal: false, reveal: false, hosts: [], slow: [], own: null,
    visible, progress: null, steps: state.blank.map((s, i) => ({ ...s, status: i === 0 ? 'run' : 'wait' })) });
  render();
}

function showView(view) {
  Object.assign(state, { phase: 'done', host: view.host, verdict: view.verdict, steps: view.steps,
    hosts: view.hosts || [], slow: view.slow || [], own: view.own || null });
}

// A record pasted, picked or dropped: read it, then compare it with whynet's check (of its site, first, when needed).
async function readOwn(reading) {
  const m = state.ownModal;
  if (!m || m.status === 'reading') return;
  let info;
  try { info = await reading; } catch (err) { Object.assign(m, { status: 'warn', msg: err?.message ?? String(err) }); render(); return; }
  if (!info?.site) return; // the file dialog was cancelled
  if (info.recheck) { state.ownModal = null; running(info.site, false); }
  else { Object.assign(m, { status: 'reading', msg: fill(state.ui.own_reading, info) }); render(); }
  try {
    showView(await CompareHAR());
    Object.assign(state, { ownModal: null, details: true });
  } catch (err) {
    if (state.ownModal) Object.assign(state.ownModal, { status: 'warn', msg: err?.message ?? String(err) });
    else Object.assign(state, { phase: 'idle', error: err?.message ?? String(err), steps: state.blank });
  }
  render();
}

// Each mode keeps what was typed in it.
const typed = { site: '', live: '', app: '' };
modeEl.addEventListener('click', e => {
  const b = e.target.closest('button[data-mode]');
  if (!b || b.disabled || b.dataset.mode === state.mode) return;
  typed[state.mode] = input.value;
  state.mode = b.dataset.mode;
  input.value = typed[state.mode];
  state.details = false;
  watchPrograms(state.mode === 'app');
  render();
  input.focus();
});

form.addEventListener('submit', e => {
  e.preventDefault();
  if (state.mode === 'live') startLive(input.value.trim(), false);
  else if (state.mode === 'app') startApp();
  else startCheck(input.value.trim(), false);
});

// In the app mode the field searches the list; typing after a result goes back to the list.
input.addEventListener('input', () => {
  const ap = state.app;
  if (state.mode !== 'app') return;
  if (ap.phase === 'done') { ap.phase = 'idle'; state.details = false; loadPrograms(); }
  if (ap.phase === 'idle') { ap.selected = ''; render(); }
});

out.addEventListener('dblclick', e => {
  const el = e.target.closest('[data-act="app-pick"]');
  if (el) { state.app.selected = el.dataset.exe; startApp(); }
});

out.addEventListener('click', async e => {
  const el = e.target.closest('[data-act]');
  const act = el?.dataset.act;
  if (act === 'details') { state.details = !state.details; render(); }
  else if (act === 'update-no') { state.update = null; render(); }
  else if (act === 'update-yes') {
    state.updating = true; render();
    try { await Update(); } // on success the app restarts into the new version
    catch (err) { const v = state.update.version; Object.assign(state, { update: null, updating: false }); render(); toast(fill(state.ui.update_failed, { error: err?.message ?? err, version: v })); }
  }
  else if (act === 'in-browser') startCheck(input.value.trim() || state.host, true);
  else if (act === 'own') { state.ownModal = { tab: state.browser, status: '', msg: '' }; render(); }
  else if (act === 'own-tab') { state.ownModal.tab = el.dataset.tab; render(); }
  else if (act === 'own-close' || (act === 'own-backdrop' && e.target === el)) {
    if (state.ownModal?.status !== 'reading') { state.ownModal = null; render(); }
  }
  else if (act === 'own-paste') readOwn(PasteHAR());
  else if (act === 'own-pick') readOwn(PickHAR());
  else if (act === 'finish') { el.disabled = true; FinishBrowser(); }
  else if (act === 'app-pick') {
    const p = state.app.list.find(x => x.exe === el.dataset.exe);
    if (p?.browser) toast(fill(state.A.browser, { name: p.name }));
    else if (p) { state.app.selected = p.exe; render(); }
  }
  else if (act === 'app-done') { el.disabled = true; FinishApp(); }
  else if (act === 'app-cancel') CancelApp();
  else if (act === 'live-done') {
    el.disabled = true;
    try { showLiveResult(await FinishLive()); } catch (err) { failed(err); }
  }
  else if (act === 'live-cancel') { await CancelLive(); state.live = { ...freshLive(), chart: state.live.chart }; render(); }
  else if (act === 'live-mark') { MarkLag(); state.live.lags++; updateLiveRec(); }
  else if (act === 'live-again') startLive(state.live.target, true);
  else if (act === 'view-lanes' || act === 'view-lines') { state.live.chart = act.slice(5); render(); }
  else if (act === 'open') { state.modal = true; render(); $('m-title')?.closest('.m').querySelector('[data-act="save"]').focus(); }
  else if (act === 'close' || (act === 'backdrop' && e.target === el)) { state.modal = false; render(); }
  else if (act === 'copy') {
    try { await CopyReport(!(state.modal && state.reveal)); toast(state.ui.copied); } catch (err) { failed(err); }
  } else if (act === 'save') {
    try { const path = await SaveReport(!state.reveal); if (path) toast(fill(state.ui.saved, { path })); } catch (err) { failed(err); }
  }
});

out.addEventListener('mousemove', e => { if (e.target.closest('#full-box')) chartHover(e); });
out.addEventListener('mouseout', e => {
  if (!e.target.closest('#full-box') || e.relatedTarget?.closest?.('#full-box')) return;
  const tip = $('tip'), cur = $('cursor');
  if (tip) tip.hidden = true;
  if (cur) { cur.setAttribute('x1', -10); cur.setAttribute('x2', -10); }
});

out.addEventListener('change', e => {
  if (e.target.id === 'app-udp') {
    state.app.udp = e.target.checked;
    try { localStorage.setItem('app-udp', state.app.udp ? '1' : '0'); } catch { /* remembered for this run only */ }
    return;
  }
  if (e.target.id !== 'reveal') return;
  state.reveal = e.target.checked;
  refreshPreview();
});

document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && state.modal) { state.modal = false; render(); }
  if (e.key === 'Escape' && state.ownModal && state.ownModal.status !== 'reading') { state.ownModal = null; render(); }
});

// Ctrl+V with the record dialog open: the copied record.
document.addEventListener('paste', e => {
  if (!state.ownModal) return;
  e.preventDefault();
  readOwn(ReadHAR(e.clipboardData?.getData('text') ?? ''));
});

// A .har file dropped on the window while the record dialog is open.
OnFileDrop((x, y, paths) => { if (state.ownModal && paths?.length) readOwn(ReadHARFile(paths[0])); }, false);

Info().then(info => {
  Object.assign(state, { ui: info.ui, L: info.live || {}, A: info.app || {}, version: info.version, blank: info.steps, steps: info.steps,
    appBlank: info.app_steps || [], browser: info.browser || 'chromium' });
  const [siteBtn, liveBtn, appBtn] = modeEl.querySelectorAll('button');
  siteBtn.textContent = state.L.mode_site;
  liveBtn.innerHTML = `${esc(state.L.mode_live)} <small>· ${esc(state.L.mode_live_hint)}</small>`;
  appBtn.textContent = state.A.mode;
  modeEl.hidden = false;
  render();
  input.focus();
  CheckUpdate().then(rel => { if (rel) { state.update = rel; render(); } }).catch(() => {});
});
