import '@fontsource/golos-text/400.css';
import '@fontsource/golos-text/500.css';
import '@fontsource/golos-text/600.css';
import '@fontsource/jetbrains-mono/400.css';
import './style.css';

import { Info, Check, Report, SaveReport, CopyReport } from '../wailsjs/go/main/App';
import { EventsOn, WindowGetPosition, WindowGetSize, WindowSetPosition, WindowSetSize } from '../wailsjs/runtime/runtime';

const GLYPH = { ok: '✓', fail: '✗', error: '✗', warn: '!', skip: '—', wait: '', run: '' };
const TONE = { ok: '✓', warn: '!', bad: '✗' };
const esc = s => String(s ?? '').replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);
const fill = (s, vars) => String(s ?? '').replace(/\{(\w+)\}/g, (m, k) => (k in vars ? vars[k] : m));

const $ = id => document.getElementById(id);
const form = $('ask'), input = $('site'), checkBtn = $('check'), out = $('out'), toastEl = $('toast');

const state = {
  ui: {}, version: '', blank: [], steps: [], hosts: [],
  phase: 'idle', // idle | running | done
  host: '', verdict: null, error: '',
  details: false, modal: false, reveal: false,
};

function stepsHTML() {
  return state.steps.map(s => `<li class="${s.status}">
      <span class="ico ${s.status}">${GLYPH[s.status] ?? ''}</span>
      <span>${esc(s.name)}</span>
      <span class="ms">${s.status === 'run' ? '…' : s.status === 'wait' || s.status === 'skip' ? '' : `${s.ms} мс`}</span>
      ${s.note ? `<span class="note">${esc(s.note)}</span>` : ''}
    </li>`).join('');
}

function rightHTML() {
  const ui = state.ui;
  if (state.phase === 'running') {
    const n = state.steps.findIndex(s => s.status === 'run');
    return `<section class="verdict waiting" aria-live="polite"><strong>${esc(fill(ui.checking, { host: state.host }))}</strong>
      <span>${esc(fill(ui.step_of, { n: n < 0 ? state.steps.length : n + 1, total: state.steps.length }))}${n >= 0 ? `: ${esc(state.steps[n].name.toLowerCase())}` : ''}</span></section>`;
  }
  const v = state.verdict;
  return `<section class="verdict tone-${esc(v.tone)}" aria-live="polite">
      <div class="top"><span class="big" aria-hidden="true">${TONE[v.tone] ?? '!'}</span><span class="pill">${esc(v.confidence)}</span></div>
      <h2>${esc(v.title)}</h2><p>${esc(v.explain)}</p>
      <div class="advice"><b>${esc(ui.advice)}</b>${esc(v.advice)}</div>
      <div class="actions">
        <button class="btn primary" data-act="open">${esc(ui.save_report)}</button>
        <button class="btn" data-act="copy">${esc(ui.copy)}</button>
        <button class="btn link" data-act="details" aria-expanded="${state.details}">${esc(state.details ? ui.hide_details : ui.details)} ${state.details ? '▴' : '▾'}</button>
      </div></section>`;
}

function hostsHTML() {
  const ui = state.ui;
  if (!state.hosts.length) return '';
  const rows = state.hosts.map(h => `<tr>
      <td class="host"><span class="dot ${esc(h.tone || 'none')}"></span>${esc(h.host)}</td>
      <td>${esc(h.kinds)}</td>
      <td class="num">${h.ping_ms ? `${h.ping_ms} мс` : '—'}</td>
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

function detailsHTML() {
  return `<div class="details">${hostsHTML()}
    <div class="details-title">${esc(state.ui.facts_title)}</div><div class="details-grid">${state.steps.map(s =>
    `<div class="fact-card"><b>${GLYPH[s.status] ?? ''} ${esc(s.name)}</b>${(s.facts || []).map(([k, v]) => `<span>${esc(k)}:</span> ${esc(v)}`).join('<br>')}</div>`).join('')}</div></div>`;
}

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

function render() {
  // Before the first check the window is just the address bar and one line of help.
  const body = state.phase === 'idle'
    ? `<p class="hint">${esc(state.ui.idle_text)}</p>${state.error ? `<p class="hint error-text" role="alert">${esc(state.error)}</p>` : ''}`
    : `<div class="grid"><div><ol class="steps">${stepsHTML()}</ol><div class="version">whynet ${esc(state.version)}</div></div>${rightHTML()}</div>`
      + (state.details && state.phase === 'done' ? detailsHTML() : '');
  out.innerHTML = `<div class="content" id="content">${body}</div>` + (state.modal ? modalHTML() : '');
  checkBtn.disabled = state.phase === 'running';
  if (state.modal) refreshPreview();
  fitWindow();
}

// The window grows and shrinks with what it shows, up to 90% of the screen; past that it scrolls.
let frame = null;
async function fitWindow() {
  try {
    const size = await WindowGetSize();
    if (frame === null) {
      frame = size.h - window.innerHeight; // title bar and borders
      if (!(frame >= 0 && frame < 150)) frame = 40;
    }
    const want = form.offsetHeight + $('content').offsetHeight + frame;
    const h = Math.min(Math.floor(screen.availHeight * 0.9), Math.max(want, state.modal ? 620 : 0));
    if (Math.abs(size.h - h) <= 2) return;
    // Growing keeps the top edge, unless the bottom would go under the taskbar: then move up.
    const pos = await WindowGetPosition();
    if (pos.y + h > screen.availHeight) WindowSetPosition(pos.x, Math.max(0, screen.availHeight - h));
    WindowSetSize(size.w, h);
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

form.addEventListener('submit', async e => {
  e.preventDefault();
  const site = input.value.trim();
  if (!site || state.phase === 'running') return;
  Object.assign(state, { phase: 'running', host: site, error: '', details: false, modal: false, reveal: false, hosts: [],
    steps: state.blank.map((s, i) => ({ ...s, status: i === 0 ? 'run' : 'wait' })) });
  render();
  try {
    const view = await Check(site);
    Object.assign(state, { phase: 'done', host: view.host, verdict: view.verdict, steps: view.steps, hosts: view.hosts || [] });
  } catch (err) {
    Object.assign(state, { phase: 'idle', error: err?.message ?? String(err), steps: state.blank });
  }
  render();
});

out.addEventListener('click', async e => {
  const el = e.target.closest('[data-act]');
  const act = el?.dataset.act;
  if (act === 'details') { state.details = !state.details; render(); }
  else if (act === 'open') { state.modal = true; render(); $('m-title')?.closest('.m').querySelector('[data-act="save"]').focus(); }
  else if (act === 'close' || (act === 'backdrop' && e.target === el)) { state.modal = false; render(); }
  else if (act === 'copy') {
    try { await CopyReport(!(state.modal && state.reveal)); toast(state.ui.copied); } catch (err) { failed(err); }
  } else if (act === 'save') {
    try { const path = await SaveReport(!state.reveal); if (path) toast(fill(state.ui.saved, { path })); } catch (err) { failed(err); }
  }
});

out.addEventListener('change', e => {
  if (e.target.id !== 'reveal') return;
  state.reveal = e.target.checked;
  refreshPreview();
});

document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && state.modal) { state.modal = false; render(); }
});

Info().then(info => {
  Object.assign(state, { ui: info.ui, version: info.version, blank: info.steps, steps: info.steps });
  input.placeholder = info.ui.placeholder;
  checkBtn.textContent = info.ui.check;
  render();
  input.focus();
});
