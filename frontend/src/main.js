import '@fontsource/golos-text/400.css';
import '@fontsource/golos-text/500.css';
import '@fontsource/golos-text/600.css';
import '@fontsource/jetbrains-mono/400.css';
import './style.css';

import { Info, Check, CheckInBrowser, FinishBrowser, Report, SaveReport, CopyReport, CheckUpdate, Update,
  StartLive, FinishLive, CancelLive, MarkLag } from '../wailsjs/go/main/App';
import { EventsOn, WindowGetPosition, WindowGetSize, WindowSetPosition, WindowSetSize } from '../wailsjs/runtime/runtime';

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

const state = {
  ui: {}, L: {}, version: '', blank: [], steps: [], hosts: [],
  mode: 'site', // site | live
  phase: 'idle', // idle | running | done
  host: '', verdict: null, error: '',
  details: false, modal: false, reveal: false,
  visible: false, progress: null, // a check with a visible browser window
  update: null, updating: false, // a newer release offered at the top
  live: freshLive(),
};

function updateHTML() {
  const u = state.update, ui = state.ui;
  if (!u) return '';
  if (state.updating) return `<div class="update" role="status">${esc(fill(ui.update_busy, u))}</div>`;
  return `<div class="update" role="status"><span>${esc(fill(ui.update_text, u))}</span>
    <span class="actions"><button class="btn primary" data-act="update-yes">${esc(ui.update_yes)}</button>
    <button class="btn" data-act="update-no">${esc(ui.update_no)}</button></span></div>`;
}

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
  const v = state.verdict;
  return `<section class="verdict tone-${esc(v.tone)}" aria-live="polite">
      <div class="top"><span class="big" aria-hidden="true">${TONE[v.tone] ?? '!'}</span><span class="pill">${esc(v.confidence)}</span></div>
      <h2>${esc(v.title)}</h2><p>${esc(v.explain)}</p>
      <div class="advice"><b>${esc(ui.advice)}</b>${esc(v.advice)}</div>
      <div class="actions">
        <button class="btn primary" data-act="open">${esc(ui.save_report)}</button>
        <button class="btn" data-act="copy">${esc(ui.copy)}</button>
        <button class="btn" data-act="in-browser">${esc(ui.in_browser)}</button>
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

/* ---------- Stability check ---------- */

const LINK_COLOR = { router: '#f07468', beyond: '#e6ae52', ctl_ru: '#8fa3b3', ctl_world: '#c2cdd6', server: '#6fb6ff' };
const ENOUGH = 180, MIN_SECONDS = 30, LIVE_POINTS = 240; // 3 minutes; 2 minutes of 0.5 s ticks on the live chart
const num = v => v < 10 ? v.toLocaleString('ru-RU', { maximumFractionDigits: 1 }) : String(Math.round(v));
const ms = v => (v == null || v < 0) ? '—' : `${num(v)} ${state.L.ms}`;
const clock = s => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
const linkName = id => state.live.links.find(l => l.id === id)?.name ?? id;

// Lines: one per link, ms on a fixed scale to 550 (game pings and the spikes above them).
function linesSVG(series, ids, n, w, lagIdx = []) {
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
  return svg + lagSVG(lagIdx, n, w);
}

// Lanes: one row per link; red where the link caused trouble, pale red where it repeated a nearer link's.
function lanesSVG(chart, ids, n, w, lagIdx = []) {
  const H = 180, top = 6, lh = (H - top * 2) / ids.length, cw = w / Math.max(n, 1);
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
    svg += `<text x="8" y="${y + lh / 2 + 4}" fill="#e2e8ec" font-size="11.5" font-family="Golos Text, Segoe UI, sans-serif" paint-order="stroke" stroke="#1c252e" stroke-width="3">${esc(linkName(id))}</text>`;
  });
  return svg + lagSVG(lagIdx, n, w);
}

function lagSVG(lagIdx, n, w) {
  return lagIdx.map(i => {
    const x = n > 1 ? (i / (n - 1)) * w : 0;
    return `<line x1="${x}" x2="${x}" y1="0" y2="180" stroke="#c792ea" stroke-width="1.5" stroke-dasharray="4 3" vector-effect="non-scaling-stroke"/><rect x="${x - 26}" y="2" width="52" height="15" rx="4" fill="#c792ea"/><text x="${x}" y="13" text-anchor="middle" fill="#1b1124" font-size="10.5" font-weight="600" font-family="Golos Text, Segoe UI, sans-serif">${esc(state.L.mark?.toLowerCase())}</text>`;
  }).join('');
}

function legendHTML(view) {
  const L = state.L;
  if (view === 'lanes') return `<span><span class="lane-key" style="background:#1f3529"></span>${esc(L.legend_calm)}</span>
    <span><span class="lane-key" style="background:#f07468"></span>${esc(L.legend_own)}</span>
    <span><span class="lane-key" style="background:#f0746855"></span>${esc(L.legend_repeat)}</span>
    <span><span class="lane-key" style="background:#ff4d3d"></span>${esc(L.legend_lost)}</span>
    <span><span class="lane-key" style="background:#c792ea"></span>${esc(L.legend_lag)}</span>`;
  return state.live.links.map(l => `<span><span class="key" style="background:${LINK_COLOR[l.id]}"></span>${esc(l.name)}</span>`).join('');
}

function liveChainRecHTML() {
  const lv = state.live, L = state.L;
  return lv.links.map(l => {
    const own = lv.own[l.id] || 0;
    return `<li id="link-${l.id}" class="${own ? 'bad' : ''}"><span class="ico ${own ? 'warn' : 'run'}">${own ? '!' : ''}</span>
      <span class="what"><span class="key" style="background:${LINK_COLOR[l.id]}"></span>${esc(l.name)}<small>${esc(l.addr)}</small></span>
      <span class="num">${esc(L.now)} <b>${ms(lv.now[l.id])}</b><br>${esc(fill(L.own, { n: own }))}</span></li>`;
  }).join('');
}

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
  $('live-prog').innerHTML = progressHTML(sec);
  $('live-chain').innerHTML = liveChainRecHTML();
  const early = sec < MIN_SECONDS, done = $('live-done');
  done.disabled = early;
  done.textContent = early ? fill(L.done_in, { n: MIN_SECONDS - sec }) : L.done;
  $('live-hint').textContent = early ? fill(L.rec_hint_early, { n: MIN_SECONDS - sec }) : L.rec_hint;
  const n = Math.min(LIVE_POINTS, Math.max(0, ...Object.values(lv.series).map(s => s.length)));
  const recent = Object.fromEntries(Object.entries(lv.series).map(([id, s]) => [id, s.slice(-LIVE_POINTS)]));
  $('live-chart').innerHTML = linesSVG(recent, lv.links.map(l => l.id), n, 600);
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
  const lags = v.lags?.length ? `<div class="fact-card"><b>${esc(L.lags_title)}</b>${v.lags.map(m => `<span>${esc(m.time)}:</span> ${esc(m.network ? L.lag_short_network : L.lag_short_calm)}`).join('<br>')}</div>` : '';
  return `<div class="details">
    <div class="charthead"><div class="details-title">${esc(L.chart_title)}</div>
      <div class="seg small" role="tablist"><button type="button" data-act="view-lanes" aria-selected="${view === 'lanes'}">${esc(L.view_lanes)}</button>
        <button type="button" data-act="view-lines" aria-selected="${view === 'lines'}">${esc(L.view_lines)}</button></div></div>
    <div class="chartbox hover" id="full-box"><svg class="chart" id="full-chart" viewBox="0 0 900 180" preserveAspectRatio="none"></svg><div class="tip" id="tip" hidden></div></div>
    <div class="legend">${legendHTML(view)}</div>
    <div class="details-title">${esc(L.links_title)}</div>${links}${trace}
    ${wifi || lags ? `<div class="details-grid">${wifi}${lags}</div>` : ''}</div>`;
}

function drawFullChart() {
  const v = state.live.view, svg = $('full-chart');
  if (!v || !svg) return;
  const ids = v.links.map(l => l.id), n = Math.max(...ids.map(id => v.chart.series[id]?.ms.length || 0));
  const lagIdx = (v.lags || []).map(m => Math.round(m.at / v.chart.step));
  svg.innerHTML = (state.live.chart === 'lanes' ? lanesSVG(v.chart, ids, n, 900, lagIdx)
    : linesSVG(Object.fromEntries(ids.map(id => [id, v.chart.series[id].ms])), ids, n, 900, lagIdx))
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
  const lag = (v.lags || []).some(m => Math.abs(m.at - i * v.chart.step) <= 2);
  tip.innerHTML = `<div class="tt">${esc(time)}</div>` + v.links.map(l => {
    const val = v.chart.series[l.id].ms[i];
    return `<div class="row"><span><span class="key" style="background:${LINK_COLOR[l.id]}"></span>${esc(l.name)}</span><span>${val < 0 ? esc(L.lost) : ms(val)}</span></div>`;
  }).join('') + (lag ? `<div class="why lag">${esc(L.tip_lag)}</div>` : '')
    + (blame ? `<div class="why">${fill(esc(L.tip_blame), { link: `<b>${esc(linkName(blame))}</b>` })}</div>` : `<div class="why calm">${esc(L.tip_calm).replace(/:\s*(.+)$/, ': <b>$1</b>')}</div>`);
  tip.hidden = false;
  const bx = box.getBoundingClientRect(), px = e.clientX - bx.left, tw = tip.offsetWidth;
  tip.style.left = `${px + 14 + tw > bx.width ? px - tw - 14 : px + 14}px`;
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

/* ---------- Window ---------- */

function render() {
  const live = state.mode === 'live';
  // Before the first check the window is just the address bar and one line of help.
  const site = state.phase === 'idle'
    ? `<p class="hint">${esc(state.ui.idle_text)}</p>${state.error ? `<p class="hint error-text" role="alert">${esc(state.error)}</p>` : ''}`
    : `<div class="grid"><div><ol class="steps">${stepsHTML()}</ol><div class="version">whynet ${esc(state.version)}</div></div>${rightHTML()}</div>`
      + (state.details && state.phase === 'done' ? detailsHTML() : '');
  out.innerHTML = `<div class="content" id="content">${updateHTML()}${live ? liveHTML() : site}</div>` + (state.modal ? modalHTML() : '');
  const busy = state.phase === 'running' || ['starting', 'recording'].includes(state.live.phase);
  checkBtn.disabled = busy;
  input.disabled = live && busy;
  checkBtn.textContent = live ? state.L.start : state.ui.check;
  input.placeholder = live ? state.L.placeholder : state.ui.placeholder;
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

EventsOn('browser-progress', p => {
  if (!state.visible || state.phase !== 'running') return;
  state.progress = p;
  const live = document.querySelector('.live');
  if (live) live.textContent = fill(state.ui.browser_live, p); // no full render: keep the button under the cursor
  else render();
});

async function startCheck(site, visible) {
  if (!site || state.phase === 'running') return;
  Object.assign(state, { phase: 'running', host: site, error: '', details: false, modal: false, reveal: false, hosts: [],
    visible, progress: null, steps: state.blank.map((s, i) => ({ ...s, status: i === 0 ? 'run' : 'wait' })) });
  render();
  try {
    const view = await (visible ? CheckInBrowser(site) : Check(site));
    Object.assign(state, { phase: 'done', host: view.host, verdict: view.verdict, steps: view.steps, hosts: view.hosts || [] });
    if (visible && view.steps.find(s => s.id === 'browser')?.facts?.some(([k, v]) => k === 'br.status' && v === 'unavailable')) {
      toast(state.ui.browser_missing);
    }
  } catch (err) {
    Object.assign(state, { phase: 'idle', error: err?.message ?? String(err), steps: state.blank });
  }
  state.visible = false;
  render();
}

// Each mode keeps what was typed in it.
const typed = { site: '', live: '' };
modeEl.addEventListener('click', e => {
  const b = e.target.closest('button[data-mode]');
  if (!b || b.disabled || b.dataset.mode === state.mode) return;
  typed[state.mode] = input.value;
  state.mode = b.dataset.mode;
  input.value = typed[state.mode];
  state.details = false;
  render();
  input.focus();
});

form.addEventListener('submit', e => {
  e.preventDefault();
  if (state.mode === 'live') startLive(input.value.trim(), false);
  else startCheck(input.value.trim(), false);
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
  else if (act === 'finish') { el.disabled = true; FinishBrowser(); }
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
  if (e.target.id !== 'reveal') return;
  state.reveal = e.target.checked;
  refreshPreview();
});

document.addEventListener('keydown', e => {
  if (e.key === 'Escape' && state.modal) { state.modal = false; render(); }
});

Info().then(info => {
  Object.assign(state, { ui: info.ui, L: info.live || {}, version: info.version, blank: info.steps, steps: info.steps });
  const [siteBtn, liveBtn] = modeEl.querySelectorAll('button');
  siteBtn.textContent = state.L.mode_site;
  liveBtn.innerHTML = `${esc(state.L.mode_live)} <small>· ${esc(state.L.mode_live_hint)}</small>`;
  modeEl.hidden = false;
  render();
  input.focus();
  CheckUpdate().then(rel => { if (rel) { state.update = rel; render(); } }).catch(() => {});
});
