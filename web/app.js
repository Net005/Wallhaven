'use strict';
const $ = (s, e = document) => e.querySelector(s), $$ = (s, e = document) => [...e.querySelectorAll(s)];
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const store = { get(k, d) { try { return localStorage.getItem(k) ?? d } catch { return d } }, set(k, v) { try { localStorage.setItem(k, v) } catch { } } };
const fmtBytes = n => { n = +n || 0; const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0; while (n >= 1024 && i < 4) { n /= 1024; i++ } return (i ? n.toFixed(n < 10 ? 2 : 1) : n) + ' ' + u[i] };
const fmtDur = s => { s = Math.max(0, Math.round(s)); const h = Math.floor(s / 3600), m = Math.floor(s % 3600 / 60); return h ? `${h}h ${m}m` : m ? `${m}m ${s % 60}s` : `${s}s` };
const isZero = t => !t || String(t).startsWith('0001');
const ago = t => { if (isZero(t)) return '—'; const s = (Date.now() - new Date(t)) / 1000; if (s < 60) return 'just now'; if (s < 3600) return Math.floor(s / 60) + ' min ago'; if (s < 86400) return Math.floor(s / 3600) + ' h ago'; return Math.floor(s / 86400) + ' d ago' };
const clock = ms => new Date(ms).toLocaleTimeString([], { hour12: false });

async function api(method, url, body) {
  const r = await fetch(url, { method, headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined });
  let d = null; try { d = await r.json() } catch { }
  if (!r.ok) throw new Error(d?.error || r.statusText);
  return d;
}
function toast(msg, kind = '') {
  const t = document.createElement('div'); t.className = 'toast ' + kind; t.textContent = msg;
  $('#toasts').append(t); setTimeout(() => t.remove(), 4200);
}
const act = async (p, ok) => { try { const r = await p; if (ok) toast(ok, 'ok'); return r } catch (e) { toast(e.message, 'err') } };

const S = { cur: null, queue: [], paused: false, histRev: -1, hist: [], cfg: null, stats: null };
let view = null;

/* ───────────── live log dock ───────────── */
const logEl = $('#log'); let logQ = '', logN = 0, pend = [], raf = 0;
function lineEl(l) {
  const d = document.createElement('div'); d.className = 'ln l-' + l.lvl; d.dataset.t = l.msg.toLowerCase();
  d.innerHTML = `<span class="t">${clock(l.t)}</span>${esc(l.msg)}`;
  if (logQ && !d.dataset.t.includes(logQ)) d.classList.add('nomatch');
  return d;
}
function flushLog() {
  raf = 0; const stick = $('#autoscroll').checked;
  const frag = document.createDocumentFragment(); pend.forEach(l => frag.append(lineEl(l))); logN += pend.length; pend = [];
  logEl.append(frag);
  while (logEl.childElementCount > 3000) logEl.firstChild.remove();
  $('#logcount').textContent = logN + ' lines';
  if (stick) logEl.scrollTop = logEl.scrollHeight;
}
const addLog = l => { pend.push(l); if (!raf) raf = requestAnimationFrame(flushLog) };
logEl.addEventListener('scroll', () => { $('#autoscroll').checked = logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 30 });
$('#autoscroll').addEventListener('change', e => { if (e.target.checked) logEl.scrollTop = logEl.scrollHeight });
$('#chips').addEventListener('click', e => { const b = e.target.closest('button'); if (!b) return; b.classList.toggle('on'); logEl.classList.toggle('h-' + b.dataset.l, !b.classList.contains('on')) });
$('#logq').addEventListener('input', e => { logQ = e.target.value.toLowerCase(); $$('.ln', logEl).forEach(n => n.classList.toggle('nomatch', !!logQ && !n.dataset.t.includes(logQ))) });
$('#logclr').onclick = () => act(api('POST', '/api/logs/clear'));
$('#logdl').onclick = () => { location.href = '/api/logs/download' };
$('#dockSize').onclick = () => { const d = $('#dock'); const n = (+d.dataset.size + 1) % 3; d.dataset.size = n; store.set('dock', n) };
$('#dock').dataset.size = store.get('dock', '1');

/* ───────────── SSE ───────────── */
function connect() {
  const es = new EventSource('/api/events');
  es.onopen = () => { $('#conn').textContent = 'live'; $('#conn').classList.remove('off') };
  es.onerror = () => { $('#conn').textContent = 'offline'; $('#conn').classList.add('off'); $('#dockdot').classList.remove('live') };
  es.addEventListener('backlog', e => { logEl.innerHTML = ''; logN = 0; pend = JSON.parse(e.data); flushLog() });
  es.addEventListener('log', e => addLog(JSON.parse(e.data)));
  es.addEventListener('clear', () => { logEl.innerHTML = ''; logN = 0; $('#logcount').textContent = '' });
  es.addEventListener('state', e => {
    const s = JSON.parse(e.data), prevRev = S.histRev;
    S.cur = s.current; S.queue = s.queue; S.paused = s.paused; S.histRev = s.hist_rev;
    $('#dockdot').classList.toggle('live', !!s.current);
    const qb = $('#qbadge'); qb.hidden = !s.queue.length; qb.textContent = s.queue.length;
    if (prevRev !== s.hist_rev && prevRev !== -1) { S.stats = null; view?.onDone?.() }
    view?.onState?.();
  });
}

/* ───────────── router ───────────── */
const views = {};
async function route() {
  const name = (location.hash.replace('#/', '') || 'dashboard').split('/')[0];
  const v = views[name] || views.dashboard;
  $$('#nav a').forEach(a => a.classList.toggle('on', a.dataset.v === name));
  if (views.dashboard.timer) { clearInterval(views.dashboard.timer); views.dashboard.timer = null }
  view?.unmount?.();
  view = null; $('#view').innerHTML = '';
  if (!S.cfg) S.cfg = await api('GET', '/api/config');
  view = v; await v.mount();
}
window.addEventListener('hashchange', route);
async function refreshCfg() { S.cfg = await api('GET', '/api/config') }
const presetById = id => S.cfg.presets.find(p => p.id === id);

/* ───────────── shared bits ───────────── */
const catTags = c => ['g', 'a', 'p'].map((k, i) => `<span class="tag ${k} ${c[i] === '1' ? '' : 'off'}">${'GAP'[i]}</span>`).join('');
const purTags = p => ['sfw', 'sketchy', 'nsfw'].map((k, i) => `<span class="tag ${k} ${p[i] === '1' ? '' : 'off'}">${['SFW', 'SKT', 'NSFW'][i]}</span>`).join('');
const stripe = p => p[2] === '1' ? 'var(--nsfw)' : p[1] === '1' ? 'var(--sketchy)' : 'var(--sfw)';
function progressOf(j) {
  if (!j) return 0; const t = j.target || 0; if (!t) return 0;
  const v = j.params.count_new ? j.downloaded : j.scanned; return Math.min(100, v / t * 100);
}
function modal(html, cls = '') {
  const r = $('#modal-root'); r.innerHTML = `<div class="ov"><div class="modal ${cls}">${html}</div></div>`;
  const close = () => { r.innerHTML = '' };
  $('.ov', r).addEventListener('mousedown', e => { if (e.target.classList.contains('ov')) close() });
  return { root: $('.modal', r), close };
}
document.addEventListener('keydown', e => { if (e.key === 'Escape') { $('#modal-root').innerHTML = ''; $('#lb').classList.remove('on') } });

function runDialog(p) {
  const m = modal(`<header>${esc(p.icon)} Run “${esc(p.name)}”</header>
   <div class="body"><div class="c3"><label class="f">Count</label><input type="number" id="rc" value="${p.count}" min="1"></div>
   <div class="c3"><label class="f">Start page</label><input type="number" id="rs" value="${p.start_page}" min="1"></div>
   <div class="c6"><label class="chk"><input type="checkbox" id="rn" ${p.count_new ? 'checked' : ''}> Count only <b>new</b> wallpapers (keep going until N new files were downloaded)</label></div></div>
   <footer><button class="btn" id="x">Cancel</button><button class="btn green" id="go">▶ Run</button></footer>`);
  $('#x', m.root).onclick = m.close;
  $('#go', m.root).onclick = async () => {
    const r = await act(api('POST', '/api/run', { preset_id: p.id, count: +$('#rc').value, start_page: +$('#rs').value, count_new: $('#rn').checked }), 'Queued'); if (r) m.close();
  };
}
const quickRun = async p => { await act(api('POST', '/api/run', { preset_id: p.id }), `Queued “${p.name}”`) };

/* ───────────── dashboard ───────────── */
views.dashboard = {
  sig: '',
  async mount() {
    $('#view').innerHTML = `<h1>Dashboard</h1>
    <div class="panel"><div id="hero"><div class="hero-ico" id="hi">💤</div><div class="grow">
      <div class="row"><b id="ht" style="font-size:16px;color:#fff">Idle</b><span class="muted" id="hs">Queue is empty</span><span class="grow"></span>
        <button class="btn danger sm" id="stop" hidden>■ Stop</button>
        <button class="btn sm" id="pause"></button></div>
      <div class="bar idle" style="margin-top:8px"><i id="hb"></i></div>
      <div class="kv" id="hk"></div></div></div></div>
    <div class="row" style="margin-top:12px"><button class="btn green" id="runall">▶ Run all presets</button><button class="btn" id="clrq">Clear queue</button><button class="btn" id="custom">＋ Custom run…</button></div>
    <div class="tiles" id="tiles"></div>
    <h2>Presets</h2><div class="grid" id="grid"></div>`;
    $('#stop').onclick = () => act(api('POST', '/api/stop'));
    $('#pause').onclick = () => act(api('POST', S.paused ? '/api/queue/resume' : '/api/queue/pause'));
    $('#runall').onclick = async () => { const r = await act(api('POST', '/api/run-all')); if (r) toast(`Queued ${r.queued} preset(s)`, 'ok') };
    $('#clrq').onclick = () => act(api('POST', '/api/queue/clear'));
    $('#custom').onclick = () => presetEditor(null, true);
    $('#grid').addEventListener('click', e => {
      const b = e.target.closest('[data-a]'); if (!b) return; const p = presetById(b.dataset.id);
      if (b.dataset.a === 'run') quickRun(p); else if (b.dataset.a === 'runx') runDialog(p); else if (b.dataset.a === 'edit') presetEditor(p);
    });
    this.sig = '';
    await this.loadStats(); this.onState();
    this.timer = setInterval(() => { $$('.ago').forEach(n => { n.textContent = ago(n.dataset.t) }) }, 30000);
  },
  async loadStats() { S.stats = await api('GET', '/api/stats'); if (!$('#tiles')) return; this.tiles(); this.grid() },
  onDone() { this.loadStats() },
  tiles() {
    const s = S.stats; if (!s) return;
    const t = (n, l) => `<div class="tile"><div class="n">${n}</div><div class="l">${l}</div></div>`;
    $('#tiles').innerHTML = t(s.total_files.toLocaleString(), 'Wallpapers in library') + t(fmtBytes(s.total_bytes), 'Library size') + t(s.disk_total ? fmtBytes(s.disk_free) : '—', 'Disk free') +
      t(s.new_24h.toLocaleString(), 'New in last 24 h') + t(fmtBytes(s.bytes_24h), 'Downloaded 24 h') + t(s.runs_total, 'Runs on record');
  },
  grid() {
    const st = Object.fromEntries((S.stats?.presets || []).map(x => [x.id, x]));
    $('#grid').innerHTML = S.cfg.presets.map(p => {
      const x = st[p.id] || {}, run = S.cur?.preset_id === p.id, q = S.queue.some(j => j.preset_id === p.id);
      const sub = p.type === 'useruploads' ? '@' + p.user : p.type === 'collections' ? p.user + ' / ' + p.collection : p.query || p.type;
      return `<div class="card ${run ? 'running' : ''}" style="--stripe:${stripe(p.purity)}"><div class="in">
      <div class="t"><span class="ico">${esc(p.icon)}</span><div class="grow"><div class="name">${esc(p.name)}</div><div class="sub">${esc(sub)}</div></div>
        <button class="btn sm" data-a="edit" data-id="${p.id}" title="Edit">⚙</button></div>
      <div class="meta"><span>${catTags(p.categories)} ${purTags(p.purity)}</span><span><b>${(x.files ?? 0).toLocaleString()}</b> files · ${fmtBytes(x.bytes || 0)}</span></div>
      <div class="row"><span class="muted grow" style="font-size:11px">Last run: <span class="ago" data-t="${x.last_run || ''}">${x.last_run ? ago(x.last_run) : 'never'}</span></span>
        <button class="btn sm" data-a="runx" data-id="${p.id}">…</button>
        <button class="btn green sm" data-a="run" data-id="${p.id}" ${run || q ? 'disabled' : ''}>${run ? 'Running' : q ? 'Queued' : '▶ Run'}</button></div></div></div>`;
    }).join('') || '<div class="empty">No presets yet.</div>';
  },
  onState() {
    const j = S.cur; if (!$('#hero')) return;
    $('#pause').textContent = S.paused ? '▶ Resume queue' : '⏸ Pause queue';
    $('#stop').hidden = !j;
    $('#hb').parentElement.classList.toggle('idle', !j);
    if (j) {
      const el = (Date.now() - new Date(j.started_at)) / 1000;
      $('#hi').textContent = j.icon; $('#ht').textContent = j.name;
      $('#hs').textContent = `page ${j.page || '…'} / ${j.pages || '?'}  ·  ${j.current_file || ''}`;
      $('#hb').style.width = progressOf(j) + '%';
      $('#hk').innerHTML = `<span>Scanned <b>${j.scanned}</b>/${j.target || '?'}</span><span>New <b style="color:var(--ok)">${j.downloaded}</b></span><span>Skipped <b>${j.skipped}</b></span><span>Failed <b style="color:${j.failed ? 'var(--err)' : 'inherit'}">${j.failed}</b></span><span>Size <b>${fmtBytes(j.bytes)}</b></span><span>Speed <b>${fmtBytes(j.bytes / Math.max(el, 1))}/s</b></span><span>Elapsed <b>${fmtDur(el)}</b></span><span>Queue <b>${S.queue.length}</b></span>`;
    } else {
      $('#hi').textContent = S.paused ? '⏸' : '💤'; $('#ht').textContent = S.paused ? 'Paused' : 'Idle';
      $('#hs').textContent = S.queue.length ? `${S.queue.length} job(s) waiting` : 'Queue is empty'; $('#hb').style.width = '0';
      $('#hk').innerHTML = '';
    }
    const sig = (S.cur?.preset_id || '') + '|' + S.queue.map(q => q.preset_id).join(',');
    if (sig !== this.sig) { this.sig = sig; if (S.stats) this.grid() }
  }
};

/* ───────────── preset editor ───────────── */
const COLORS = ['660000', '990000', 'cc0000', 'cc3333', 'ea4c88', '993399', '663399', '333399', '0066cc', '0099cc', '66cccc', '77cc33', '669900', '336600', '666600', '999900', 'cccc33', 'ffff00', 'ffcc33', 'ff9900', 'ff6600', 'cc6633', '996633', '663300', '000000', '999999', 'cccccc', 'ffffff', '424153'];
const seg = (name, cls, labels, val) => `<div class="seg ${cls}" data-seg="${name}">${labels.map((l, i) => `<button type="button" class="${val[i] === '1' ? 'on' : ''}">${l}</button>`).join('')}</div>`;
const segVal = (root, n) => $$(`[data-seg=${n}] button`, root).map(b => b.classList.contains('on') ? '1' : '0').join('');
const opts = (list, cur) => list.map(([v, l]) => `<option value="${v}" ${v === cur ? 'selected' : ''}>${l}</option>`).join('');

function presetEditor(p, customRun = false) {
  const isNew = !p;
  p = p || { icon: '🖼️', name: customRun ? 'Custom run' : '', type: 'search', categories: '100', purity: '110', count: 640, start_page: 1, atleast: '1920x1080', resolutions: '', ratios: '16x9,16x10,21x9', sorting: 'date_added', order: 'desc', top_range: '1M', colors: '', in_all: true, query: '', user: '', collection: '', location: '' };
  const m = modal(`<header>${customRun ? 'Custom run (not saved)' : isNew ? 'New preset' : 'Edit preset'}</header>
  <div class="body">
   <div class="c1"><label class="f">Icon</label><input type="text" id="e_icon" value="${esc(p.icon)}" maxlength="4"></div>
   <div class="c3"><label class="f">Name</label><input type="text" id="e_name" value="${esc(p.name)}"></div>
   <div class="c2"><label class="f">Type</label><select id="e_type">${opts([['search', 'Search / tag'], ['standard', 'Standard (no query)'], ['useruploads', 'User uploads'], ['collections', 'Collection']], p.type)}</select></div>
   <div class="c6 t-search"><label class="f">Search query <span class="muted">(tag: id:37 · text · +tag -tag)</span></label><input type="text" id="e_query" value="${esc(p.query)}"></div>
   <div class="c3 t-user"><label class="f">Username</label><input type="text" id="e_user" value="${esc(p.user)}"></div>
   <div class="c3 t-coll"><label class="f">Collection name</label><input type="text" id="e_collection" value="${esc(p.collection)}"></div>
   <div class="c3"><label class="f">Categories</label>${seg('cat', 'cat', ['General', 'Anime', 'People'], p.categories)}</div>
   <div class="c3"><label class="f">Purity</label>${seg('pur', 'pur', ['SFW', 'Sketchy', 'NSFW'], p.purity)}</div>
   <div class="c2"><label class="f">Count</label><input type="number" id="e_count" min="1" value="${p.count}"></div>
   <div class="c2"><label class="f">Start page</label><input type="number" id="e_start" min="1" value="${p.start_page}"></div>
   <div class="c2"><label class="f">&nbsp;</label><label class="chk"><input type="checkbox" id="e_cnew" ${p.count_new ? 'checked' : ''}> count new only</label></div>
   <div class="c2"><label class="f">Sorting</label><select id="e_sort">${opts([['date_added', 'Date added'], ['relevance', 'Relevance'], ['random', 'Random'], ['views', 'Views'], ['favorites', 'Favorites'], ['toplist', 'Toplist'], ['hot', 'Hot']], p.sorting)}</select></div>
   <div class="c2"><label class="f">Order</label><select id="e_order">${opts([['desc', 'Descending'], ['asc', 'Ascending']], p.order)}</select></div>
   <div class="c2"><label class="f">Toplist range</label><select id="e_top">${opts([['1d', '1 day'], ['3d', '3 days'], ['1w', '1 week'], ['1M', '1 month'], ['3M', '3 months'], ['6M', '6 months'], ['1y', '1 year']], p.top_range || '1M')}</select></div>
   <div class="c2"><label class="f">At least</label><input type="text" id="e_atleast" list="resl" value="${esc(p.atleast)}" placeholder="1920x1080"></div>
   <div class="c2"><label class="f">Exact resolutions</label><input type="text" id="e_res" value="${esc(p.resolutions)}" placeholder="1920x1080,2560x1440"></div>
   <div class="c2"><label class="f">Aspect ratios</label><input type="text" id="e_ratios" value="${esc(p.ratios)}"></div>
   <datalist id="resl">${['1280x720', '1920x1080', '2560x1440', '3440x1440', '3840x2160', '5120x2880', '7680x4320'].map(r => `<option>${r}`).join('')}</datalist>
   <div class="c6"><label class="f">Color</label><div class="sw" id="e_colors"><button type="button" class="${!p.colors ? 'on' : ''}" data-c="" style="background:linear-gradient(135deg,#222 45%,#c33 46%,#c33 54%,#222 55%)" title="any"></button>${COLORS.map(c => `<button type="button" data-c="${c}" class="${p.colors === c ? 'on' : ''}" style="background:#${c}" title="#${c}"></button>`).join('')}</div></div>
   <div class="c4"><label class="f">Location <span class="muted">(relative to base dir, or absolute)</span></label><input type="text" id="e_loc" value="${esc(p.location)}"></div>
   <div class="c2"><label class="f">&nbsp;</label><label class="chk"><input type="checkbox" id="e_sub" ${p.subfolder ? 'checked' : ''}> query subfolder</label></div>
   ${customRun ? '' : `<div class="c6"><label class="chk"><input type="checkbox" id="e_all" ${p.in_all ? 'checked' : ''}> Include in “Run all presets” and schedules for “all”</label></div>`}
   <div class="c6" id="pvbox"></div>
  </div>
  <footer><button class="btn" id="e_prev">🔍 Preview search</button><span class="grow"></span><button class="btn" id="e_x">Cancel</button>
   ${customRun ? '<button class="btn" id="e_savenew">Save as preset</button>' : '<button class="btn primary" id="e_save">Save</button>'}<button class="btn green" id="e_run">▶ ${customRun ? 'Run' : 'Save &amp; run'}</button></footer>`);
  const R = m.root, g = id => $('#' + id, R);
  const showTypes = () => {
    const t = g('e_type').value, show = (sel, on) => $$(sel, R).forEach(n => { n.style.display = on ? '' : 'none' });
    show('.t-search', t === 'search'); show('.t-user', t === 'useruploads' || t === 'collections'); show('.t-coll', t === 'collections');
  };
  g('e_type').onchange = showTypes; showTypes();
  R.addEventListener('click', e => {
    const sb = e.target.closest('.seg button'); if (sb) sb.classList.toggle('on');
    const cb = e.target.closest('#e_colors button'); if (cb) { $$('#e_colors button', R).forEach(b => b.classList.remove('on')); cb.classList.add('on') }
  });
  const collect = () => ({
    id: p.id || '', icon: g('e_icon').value, name: g('e_name').value, type: g('e_type').value, query: g('e_query').value, user: g('e_user').value, collection: g('e_collection').value,
    categories: segVal(R, 'cat'), purity: segVal(R, 'pur'), count: +g('e_count').value, start_page: +g('e_start').value, count_new: g('e_cnew').checked,
    sorting: g('e_sort').value, order: g('e_order').value, top_range: g('e_top').value, atleast: g('e_atleast').value.trim(), resolutions: g('e_res').value.trim(), ratios: g('e_ratios').value.trim(),
    colors: $('#e_colors .on', R)?.dataset.c || '', location: g('e_loc').value, subfolder: g('e_sub').checked, in_all: customRun ? false : g('e_all').checked,
  });
  g('e_x').onclick = m.close;
  g('e_prev').onclick = async () => {
    g('pvbox').innerHTML = '<span class="muted">Asking Wallhaven…</span>';
    try {
      const r = await api('POST', '/api/preview', collect());
      g('pvbox').innerHTML = `<div><b style="color:#fff">${r.total.toLocaleString()}</b> results · ${r.per_page} per page · ${r.pages} pages</div><div class="pv">${r.items.map(i => `<a href="${esc(i.url)}" target="_blank" rel="noopener"><img loading="lazy" src="${esc(i.thumb)}" alt=""><i style="background:${i.purity === 'nsfw' ? 'var(--nsfw)' : i.purity === 'sketchy' ? 'var(--sketchy)' : 'var(--sfw)'}"></i></a>`).join('')}</div>`;
    } catch (e) { g('pvbox').innerHTML = `<span style="color:var(--err)">${esc(e.message)}</span>` }
  };
  const save = async () => {
    const d = collect();
    if (!d.name.trim() && !customRun) { toast('Name required', 'err'); return null }
    const r = await act(isNew || customRun ? api('POST', '/api/presets', d) : api('PUT', '/api/presets/' + p.id, d));
    if (r) { await refreshCfg(); toast('Preset saved', 'ok') }
    return r;
  };
  if (!customRun) g('e_save').onclick = async () => { if (await save()) { m.close(); view?.mount?.() } };
  else g('e_savenew').onclick = async () => { if (await save()) m.close() };
  g('e_run').onclick = async () => {
    if (customRun) { const r = await act(api('POST', '/api/run-custom', collect()), 'Queued'); if (r) m.close(); return }
    const r = await save(); if (r) { await act(api('POST', '/api/run', { preset_id: r.id }), 'Queued'); m.close(); view?.mount?.() }
  };
}

/* ───────────── presets view ───────────── */
views.presets = {
  async mount() {
    await refreshCfg();
    $('#view').innerHTML = `<h1>Presets <span class="grow"></span><button class="btn" id="cr">▶ Custom run…</button><button class="btn primary" id="np">＋ New preset</button></h1>
    <div class="panel" style="padding:0"><table><thead><tr><th></th><th>Name</th><th>Query</th><th>Filters</th><th>Count</th><th>Location</th><th>All</th><th></th></tr></thead><tbody id="pb"></tbody></table></div>`;
    $('#np').onclick = () => presetEditor(null); $('#cr').onclick = () => presetEditor(null, true);
    $('#pb').innerHTML = S.cfg.presets.map((p, i) => `<tr><td style="font-size:20px">${esc(p.icon)}</td><td><b style="color:#fff">${esc(p.name)}</b><div class="muted" style="font-size:11px">${esc(p.type)} · ${esc(p.sorting)} ${esc(p.order)}${p.atleast ? ' · ≥' + esc(p.atleast) : ''}</div></td>
      <td>${esc(p.type === 'useruploads' ? '@' + p.user : p.type === 'collections' ? p.collection : p.query)}</td><td>${catTags(p.categories)} ${purTags(p.purity)}</td><td>${p.count}${p.count_new ? ' new' : ''}</td><td class="muted" style="font-size:11px">${esc(p.location)}</td><td>${p.in_all ? '✓' : ''}</td>
      <td style="white-space:nowrap"><button class="btn sm" data-a="up" data-id="${p.id}" ${i ? '' : 'disabled'}>↑</button> <button class="btn sm" data-a="dn" data-id="${p.id}" ${i < S.cfg.presets.length - 1 ? '' : 'disabled'}>↓</button>
        <button class="btn sm" data-a="run" data-id="${p.id}">▶</button> <button class="btn sm" data-a="edit" data-id="${p.id}">Edit</button> <button class="btn sm" data-a="dup" data-id="${p.id}">Copy</button> <button class="btn sm danger" data-a="del" data-id="${p.id}">✕</button></td></tr>`).join('');
    $('#pb').onclick = async e => {
      const b = e.target.closest('[data-a]'); if (!b) return; const p = presetById(b.dataset.id), a = b.dataset.a;
      if (a === 'run') quickRun(p); else if (a === 'edit') presetEditor(p);
      else if (a === 'dup') { await act(api('POST', '/api/presets', { ...p, name: p.name + ' (copy)', id: '' })); this.mount() }
      else if (a === 'del') { if (confirm(`Delete preset “${p.name}”? Downloaded files are kept.`)) { await act(api('DELETE', '/api/presets/' + p.id)); this.mount() } }
      else { await act(api('POST', '/api/presets/move', { id: p.id, dir: a === 'up' ? -1 : 1 })); this.mount() }
    };
  }
};

/* ───────────── queue view ───────────── */
views.queue = {
  async mount() {
    $('#view').innerHTML = `<h1>Queue &amp; history <span class="grow"></span><button class="btn sm" id="hc">Clear history</button></h1>
     <h2>Active</h2><div class="panel" style="padding:0"><table><tbody id="act"></tbody></table></div>
     <h2>History</h2><div class="panel" style="padding:0"><table><thead><tr><th>Preset</th><th>Status</th><th>Trigger</th><th>Finished</th><th>Duration</th><th>New</th><th>Skipped</th><th>Failed</th><th>Size</th><th></th></tr></thead><tbody id="hist"></tbody></table></div>`;
    $('#hc').onclick = async () => { if (confirm('Clear run history?')) { await act(api('POST', '/api/history/clear')); this.load() } };
    $('#act').onclick = e => { const b = e.target.closest('[data-q]'); if (b) act(api('DELETE', '/api/queue/' + b.dataset.q)); if (e.target.closest('#stop2')) act(api('POST', '/api/stop')) };
    $('#hist').onclick = e => { const b = e.target.closest('[data-rr]'); if (b) act(api('POST', `/api/jobs/${b.dataset.rr}/rerun`), 'Queued') };
    await this.load(); this.onState();
  },
  async load() { S.hist = await api('GET', '/api/history'); if ($('#hist')) this.drawHist() },
  onDone() { this.load() },
  drawHist() {
    $('#hist').innerHTML = S.hist.map(j => `<tr><td>${esc(j.icon)} ${esc(j.name)}${j.error ? `<div style="color:var(--err);font-size:11px">${esc(j.error)}</div>` : ''}</td><td><span class="st ${j.status}">${j.status}</span></td><td class="muted">${j.trigger}</td>
      <td class="muted">${ago(j.ended_at)}</td><td>${fmtDur((new Date(j.ended_at) - new Date(j.started_at)) / 1000)}</td><td style="color:var(--ok)">${j.downloaded}</td><td>${j.skipped}</td><td>${j.failed}</td><td>${fmtBytes(j.bytes)}</td><td><button class="btn sm" data-rr="${j.id}">↻ Re-run</button></td></tr>`).join('') || '<tr><td colspan="10" class="empty">No runs yet</td></tr>';
  },
  onState() {
    if (!$('#act')) return;
    const rows = [];
    if (S.cur) { const j = S.cur; rows.push(`<tr><td style="width:40%">${esc(j.icon)} <b style="color:#fff">${esc(j.name)}</b> <span class="st running">running</span></td><td style="width:35%"><div class="bar"><i style="width:${progressOf(j)}%"></i></div></td><td class="muted">${j.downloaded} new · ${j.skipped} skipped · ${fmtBytes(j.bytes)}</td><td><button class="btn danger sm" id="stop2">■ Stop</button></td></tr>`) }
    S.queue.forEach((j, i) => rows.push(`<tr><td>${esc(j.icon)} ${esc(j.name)} <span class="st queued">#${i + 1} queued</span></td><td class="muted">${esc(j.trigger)}</td><td></td><td><button class="btn sm" data-q="${j.id}">Remove</button></td></tr>`));
    $('#act').innerHTML = rows.join('') || '<tr><td class="empty">Nothing running or queued</td></tr>';
  }
};

/* ───────────── gallery ───────────── */
views.gallery = {
  items: [], total: 0, i: 0, gen: 0, loading: false, loadPromise: null, io: null, visible: false, preloaded: new Set(),
  async mount() {
    $('#view').innerHTML = `<h1>Gallery <span class="grow"></span><select id="gp" style="width:240px"><option value="all">All presets</option>${S.cfg.presets.map(p => `<option value="${p.id}">${esc(p.icon)} ${esc(p.name)}</option>`).join('')}</select></h1>
     <div class="gal" id="gal"></div>
     <div id="sentinel" style="height:1px"></div>
     <div class="row" style="justify-content:center;margin-top:14px"><div class="muted" id="gspin" hidden>Loading…</div><button class="btn" id="more" hidden>Load more</button><span class="muted" id="gcount"></span></div>`;
    $('#gp').onchange = () => this.reset();
    $('#more').onclick = () => this.load();
    $('#gal').onclick = e => { const t = e.target.closest('.thumb'); if (t) this.open(+t.dataset.i) };
    if (this.io) this.io.disconnect();
    this.visible = false;
    this.io = new IntersectionObserver(es => { this.visible = es.some(e => e.isIntersecting); if (this.visible) this.load() }, { rootMargin: '600px' });
    this.io.observe($('#sentinel'));
    await this.reset();
  },
  unmount() { if (this.io) { this.io.disconnect(); this.io = null } },
  async reset() {
    this.gen++; this.loading = false; this.visible = false; this.loadPromise = null;
    this.items = []; this.total = 0;
    $('#gal').innerHTML = ''; $('#gcount').textContent = ''; $('#more').hidden = true;
    await this.load();
  },
  // Returns a promise that resolves once a fetch in flight (or just started) has settled,
  // so callers like step() can await "there may be more items now" instead of racing it.
  load() {
    if (this.loading) return this.loadPromise || Promise.resolve();
    if (this.items.length && this.items.length >= this.total) return Promise.resolve();
    this.loading = true;
    const myGen = this.gen, preset = $('#gp').value;
    $('#gspin').hidden = false;
    this.loadPromise = (async () => {
      const r = await act(api('GET', `/api/gallery?preset=${preset}&offset=${this.items.length}&limit=60`));
      if (myGen !== this.gen || preset !== $('#gp').value) return; // preset changed mid-flight — discard this response
      if (!r) return;
      this.total = r.total; const start = this.items.length; this.items.push(...r.items);
      $('#gal').insertAdjacentHTML('beforeend', r.items.map((it, k) => `<div class="thumb" data-i="${start + k}"><img loading="lazy" src="${it.url}" alt=""><div class="cap">${esc(it.name)} · ${fmtBytes(it.size)}</div></div>`).join(''));
      const done = this.items.length >= this.total;
      $('#more').hidden = true; // infinite scroll drives loading; kept as a hidden manual fallback
      $('#gcount').textContent = `${this.items.length} / ${this.total}`;
      if (!this.total) $('#gal').innerHTML = '<div class="empty" style="grid-column:1/-1">No wallpapers yet — run a preset.</div>';
      // if the sentinel is still on screen after this batch (short/empty viewport), keep filling
      if (!done && this.visible) this.load();
    })().finally(() => { this.loading = false; $('#gspin').hidden = true });
    return this.loadPromise;
  },
  open(i) {
    this.i = i; const it = this.items[i]; if (!it) return; const lb = $('#lb');
    lb.innerHTML = `<div class="bar2"><span class="grow">${esc(it.name)} · ${fmtBytes(it.size)} · ${i + 1}/${this.items.length}</span><a class="btn sm" href="${it.url}" target="_blank">Open</a><button class="btn sm danger" id="lbdel">Delete</button><button class="btn sm" id="lbx">✕</button></div><div class="nav" style="left:0" data-d="-1">‹</div><img src="${it.url}" alt=""><div class="nav" style="right:0" data-d="1">›</div>`;
    lb.classList.add('on');
    this.preloadAround(i);
  },
  // Smart preloading: warm the next few full-res images in either direction so J/K feels
  // instant, and start fetching the next page early once navigation gets close to the edge
  // of what's currently loaded (rather than waiting until we actually run out).
  preloadAround(i) {
    for (const d of [1, -1, 2, -2, 3]) {
      const n = i + d;
      if (n >= 0 && n < this.items.length) this.preloadOne(n);
    }
    if (this.items.length - i <= 6 && this.items.length < this.total) this.load();
  },
  preloadOne(n) {
    const it = this.items[n];
    if (!it || this.preloaded.has(it.url)) return;
    this.preloaded.add(it.url);
    const img = new Image(); img.src = it.url;
  },
  async step(d) {
    let n = this.i + d;
    if (n < 0) return;
    if (n >= this.items.length) {
      if (this.items.length >= this.total) return; // truly the last item
      await this.load();
      if (n >= this.items.length) return; // load settled and there still isn't one
    }
    this.open(n);
  }
};
document.body.insertAdjacentHTML('beforeend', '<div id="lb"></div>');
$('#lb').addEventListener('click', async e => {
  const g = views.gallery;
  if (e.target.id === 'lb' || e.target.id === 'lbx') $('#lb').classList.remove('on');
  else if (e.target.dataset.d) g.step(+e.target.dataset.d);
  else if (e.target.id === 'lbdel') {
    const it = g.items[g.i]; if (!confirm('Delete ' + it.name + ' from disk?')) return;
    if (await act(api('DELETE', `/api/files/${it.preset}/${encodeURIComponent(it.name)}`), 'Deleted')) { $('#lb').classList.remove('on'); g.reset() }
  }
});
document.addEventListener('keydown', e => {
  if (!$('#lb').classList.contains('on')) return;
  const k = e.key.toLowerCase();
  if (e.key === 'ArrowLeft' || k === 'k') { e.preventDefault(); views.gallery.step(-1) }
  else if (e.key === 'ArrowRight' || k === 'j') { e.preventDefault(); views.gallery.step(1) }
});

/* ───────────── schedules ───────────── */
views.schedules = {
  async mount() {
    await refreshCfg(); this.list = JSON.parse(JSON.stringify(S.cfg.schedules || []));
    $('#view').innerHTML = `<h1>Schedules <span class="grow"></span><button class="btn" id="sa">＋ Add schedule</button><button class="btn primary" id="ss">Save</button></h1>
      <div class="panel" style="padding:0"><table><thead><tr><th>Preset</th><th>Every</th><th>Enabled</th><th>Last run</th><th>Next run</th><th></th></tr></thead><tbody id="sb"></tbody></table></div>
      <p class="muted">Scheduled jobs join the same queue as manual runs. A preset that is already queued or running is skipped.</p>`;
    $('#sa').onclick = () => { this.list.push({ id: '', preset: 'all', interval_min: 720, enabled: true, last_run: '0001-01-01T00:00:00Z' }); this.draw() };
    $('#ss').onclick = async () => { const r = await act(api('PUT', '/api/schedules', this.list), 'Schedules saved'); if (r) this.mount() };
    $('#sb').addEventListener('change', e => { const i = +e.target.closest('tr').dataset.i, f = e.target.dataset.f; this.list[i][f] = f === 'enabled' ? e.target.checked : f === 'interval_min' ? +e.target.value : e.target.value });
    $('#sb').addEventListener('click', e => { const b = e.target.closest('[data-del]'); if (b) { this.list.splice(+b.dataset.del, 1); this.draw() } });
    this.draw();
  },
  draw() {
    const iv = [[60, 'hour'], [180, '3 hours'], [360, '6 hours'], [720, '12 hours'], [1440, 'day'], [2880, '2 days'], [10080, 'week']];
    $('#sb').innerHTML = this.list.map((s, i) => {
      const next = isZero(s.last_run) ? '—' : new Date(new Date(s.last_run).getTime() + s.interval_min * 60000).toLocaleString();
      return `<tr data-i="${i}"><td><select data-f="preset"><option value="all" ${s.preset === 'all' ? 'selected' : ''}>All presets</option>${S.cfg.presets.map(p => `<option value="${p.id}" ${p.id === s.preset ? 'selected' : ''}>${esc(p.icon)} ${esc(p.name)}</option>`).join('')}</select></td>
      <td><select data-f="interval_min">${iv.map(([v, l]) => `<option value="${v}" ${v === s.interval_min ? 'selected' : ''}>${l}</option>`).join('')}${iv.some(x => x[0] === s.interval_min) ? '' : `<option selected value="${s.interval_min}">${s.interval_min} min</option>`}</select></td>
      <td><input type="checkbox" data-f="enabled" ${s.enabled ? 'checked' : ''}></td><td class="muted">${ago(s.last_run)}</td><td class="muted">${s.enabled ? next : '—'}</td><td><button class="btn sm danger" data-del="${i}">✕</button></td></tr>`;
    }).join('') || '<tr><td colspan="6" class="empty">No schedules</td></tr>';
  }
};

/* ───────────── settings ───────────── */
views.settings = {
  async mount() {
    await refreshCfg(); const c = S.cfg;
    $('#view').innerHTML = `<h1>Settings</h1>
    <div class="panel"><div class="modal" style="max-width:none;box-shadow:none;background:none;border:0"><div class="body" style="padding:0">
     <div class="c4"><label class="f">Wallhaven API key ${c.api_key_set ? `<span class="muted">(current: ${esc(c.api_key_hint)})</span>` : '<span style="color:var(--warn)">not set — NSFW &amp; collections need it</span>'}</label><input type="password" id="s_key" placeholder="${c.api_key_set ? 'leave blank to keep · type - to remove' : 'paste key'}" autocomplete="off"></div>
     <div class="c2"><label class="f">&nbsp;</label><button class="btn" id="s_test">Test key</button></div>
     <div class="c6" id="s_res"></div>
     <div class="c6"><label class="f">Base download directory</label><input type="text" id="s_base" value="${esc(c.base_dir)}"></div>
     <div class="c2"><label class="f">Parallel downloads</label><input type="number" id="s_conc" min="1" max="16" value="${c.concurrency}"></div>
     <div class="c2"><label class="f">Min. delay between API calls (ms)</label><input type="number" id="s_delay" min="0" value="${c.api_delay_ms}"></div>
     <div class="c2"><label class="f">Rate-limit cooldown (s)</label><input type="number" id="s_cool" min="1" value="${c.cooldown_sec}"></div>
     <div class="c3"><label class="f">Web UI username <span class="muted">(blank = no login)</span></label><input type="text" id="s_user" value="${esc(c.auth_user)}" autocomplete="off"></div>
     <div class="c3"><label class="f">Web UI password ${c.auth_enabled ? '<span class="muted">(set)</span>' : ''}</label><input type="password" id="s_pass" autocomplete="new-password" placeholder="${c.auth_enabled ? 'leave blank to keep' : ''}"></div>
     <div class="c6 row"><button class="btn primary" id="s_save">Save settings</button><span class="muted">Wallhaven Control v${esc(c.version)}</span></div></div></div></div>`;
    const body = () => ({ api_key: $('#s_key').value, base_dir: $('#s_base').value, concurrency: +$('#s_conc').value, api_delay_ms: +$('#s_delay').value, cooldown_sec: +$('#s_cool').value, auth_user: $('#s_user').value, auth_pass: $('#s_pass').value });
    $('#s_save').onclick = async () => { const r = await act(api('PUT', '/api/settings', body()), 'Settings saved'); if (r) { S.cfg = r; this.mount() } };
    $('#s_test').onclick = async () => {
      if ($('#s_key').value) await act(api('PUT', '/api/settings', body()));
      try {
        const r = await api('POST', '/api/test-key'), d = r.data || {};
        $('#s_res').innerHTML = `<span style="color:var(--ok)">✔ Key works.</span> <span class="muted">Account: ${esc(d.per_page || '?')} per page · purity ${esc((d.purity || []).join(', '))} · categories ${esc((d.categories || []).join(', '))}</span>`;
      } catch (e) { $('#s_res').innerHTML = `<span style="color:var(--err)">✖ ${esc(e.message)}</span>` }
    };
  }
};

connect(); route();
