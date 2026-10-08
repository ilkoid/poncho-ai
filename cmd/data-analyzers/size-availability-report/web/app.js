/* Доступность размеров · WB ↔ 1С: фильтры → KPI → категории → таблица → деталь.
   Все данные в #payload; сеть не нужна. Пороги представленности переключаются
   на клиенте: вердикты пересчитываются из пер-размерных количеств (sizes). */
'use strict';

const P = JSON.parse(document.getElementById('payload').textContent);
const A = P.arts, Z = P.sizes, M = P.meta;

const C = { green: '#018849', red: '#E41A2A', grey: '#9E9AA8', violet: '#7758B3', ink2: '#757575' };

/* сентинел фильтра «категория пуста в 1С»: '' занят как «фильтр выключен» */
const CAT_EMPTY = '∅';
const catLabel = (c) => (c === CAT_EMPTY ? '— без категории —' : c);

const state = {
  cat1: '', cat2: '', year: '', season: '', search: '',
  units: M.min_units,       // размер представлен ⇔ qty > units
  minSizes: M.min_sizes,    // недоступен ⇔ представлено < minSizes
  view: 'unavail',          // unavail | all | nocard
  sortCol: 6, sortDir: -1,  // по умолчанию: 1С free по убыванию
  sel: -1,
};

/* ── индекс размеров: nm → массив индексов Z (один проход, Z отсортирован по nm) ── */
const sizesOf = new Map();
for (let k = 0; k < Z.nm.length; k++) {
  let arr = sizesOf.get(Z.nm[k]);
  if (!arr) { arr = []; sizesOf.set(Z.nm[k], arr); }
  arr.push(k);
}

/* представленность артикула при пороге units: размеры с qty > units */
function presCnt(i, u) {
  if (!A.has_card[i]) return 0;
  const arr = sizesOf.get(A.nm_id[i]);
  if (!arr) return 0;
  let c = 0;
  for (const k of arr) if (Z.qty[k] > u) c++;
  return c;
}
const unavail = (i) => presCnt(i, state.units) < state.minSizes;

/* ── фильтры ── */
function rowsInView() {
  const q = state.search.trim().toUpperCase();
  const c1 = state.cat1 === CAT_EMPTY ? '' : state.cat1;
  const out = [];
  for (let i = 0; i < A.article.length; i++) {
    if (state.cat1 && A.cat1[i] !== c1) continue;
    if (state.cat2 && A.cat2[i] !== state.cat2) continue;
    if (state.year && String(A.year[i] || '—') !== state.year) continue;
    if (state.season && A.season[i] !== state.season) continue;
    if (q && !A.article[i].toUpperCase().includes(q)) continue;
    if (state.view === 'unavail' && !unavail(i)) continue;
    if (state.view === 'nocard' && A.has_card[i]) continue;
    out.push(i);
  }
  return out;
}

/* ── форматирование ── */
const fmtN = (v) => v.toLocaleString('ru-RU');
const esc = (s) => String(s).replace(/[&<>"']/g, (ch) => (
  { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
const delta = (now, prev) => {
  if (prev == null || !M.prev) return '';
  const d = now - prev;
  return d === 0 ? 'без изменений к ' + M.prev.date.slice(5) :
    (d > 0 ? '↗+' : '↘') + fmtN(d) + ' к ' + M.prev.date.slice(5);
};

/* ── шапка ── */
function renderHeader() {
  document.getElementById('subtitle').textContent =
    `снапшот ${M.snapshot_date} · стоки WB ${M.stocks_date} · остатки 1С ${M.rests_date} · склад «${M.onec_storage}» · сгенерирован ${M.generated_at}`;
  const b = document.getElementById('db-badge');
  if (M.db === 'wb_data_prod') { b.textContent = 'прод'; b.classList.remove('test'); }
  else { b.textContent = 'тест: ' + M.db; b.classList.add('test'); }

  const chips = document.getElementById('chips');
  chips.innerHTML = '';
  const mk = (label, val, clear) => {
    if (!val) return;
    const el = document.createElement('span');
    el.className = 'chip';
    el.innerHTML = `${label}: <b>${esc(val)}</b><button class="x" title="Сбросить">×</button>`;
    el.querySelector('.x').onclick = clear;
    chips.appendChild(el);
  };
  mk('Категория', catLabel(state.cat1), () => { state.cat1 = ''; state.cat2 = ''; wireSelects(); render(); });
  mk('Подкатегория', state.cat2, () => { state.cat2 = ''; wireSelects(); render(); });
  mk('Год', state.year, () => { state.year = ''; wireSelects(); render(); });
  mk('Сезон', state.season, () => { state.season = ''; wireSelects(); render(); });
  mk('Поиск', state.search.trim(), () => { state.search = ''; document.getElementById('f-search').value = ''; render(); });
}

/* опции селектов строятся из данных с учётом уже выбранных фильтров
   (cat1 фильтрует список cat2, остальные независимы) */
function wireSelects() {
  const c1 = state.cat1 === CAT_EMPTY ? '' : state.cat1;
  const uniqOf = (vals) => {
    const seen = new Set();
    const out = [];
    for (let i = 0; i < vals.length; i++) {
      const v = vals[i];
      if (v === '' || seen.has(v)) continue;
      if (vals === A.cat2 && c1 && A.cat1[i] !== c1) continue;
      seen.add(v);
      out.push(v);
    }
    return out.sort((a, b) => a.localeCompare(b, 'ru'));
  };
  const ru = uniqOf(A.cat1);
  const years = [...new Set(A.year)].filter((v) => v > 0).sort((a, b) => b - a).map(String);

  const fill = (id, opts, cur, label) => {
    const el = document.getElementById(id);
    el.innerHTML = `<option value="">${label}</option>` +
      opts.map((v) => `<option value="${esc(v)}"${v === cur ? ' selected' : ''}>${esc(v)}</option>`).join('');
  };
  fill('f-cat1', ru, state.cat1, 'Все категории');
  fill('f-cat2', uniqOf(A.cat2), state.cat2, 'Все подкатегории');
  fill('f-year', years, state.year, 'Все годы');
  fill('f-season', uniqOf(A.season), state.season, 'Все сезоны');
}

function bindControls() {
  wireSelects();
  document.getElementById('f-cat1').onchange = (e) => { state.cat1 = e.target.value; state.cat2 = ''; wireSelects(); render(); };
  document.getElementById('f-cat2').onchange = (e) => { state.cat2 = e.target.value; render(); };
  document.getElementById('f-year').onchange = (e) => { state.year = e.target.value; render(); };
  document.getElementById('f-season').onchange = (e) => { state.season = e.target.value; render(); };
  let tmr = null;
  document.getElementById('f-search').oninput = (e) => {
    clearTimeout(tmr);
    tmr = setTimeout(() => { state.search = e.target.value; render(); }, 150);
  };

  /* пороги — числа, view — строка: конвертер задаёт каждый сегмент;
     dataset всегда строка, +'' = 0 (а не «строка 0 как truthy») */
  const seg = (id, attr, apply, num = true) => {
    document.querySelectorAll('#' + id + ' button').forEach((b) => b.onclick = () => {
      document.querySelectorAll('#' + id + ' button').forEach((x) => x.classList.remove('active'));
      b.classList.add('active');
      apply(num ? +b.dataset[attr] : b.dataset[attr]);
      render();
      if (state.sel >= 0) openDetail(state.sel);
    });
  };
  seg('seg-units', 'u', (v) => { state.units = v; });
  seg('seg-sizes', 's', (v) => { state.minSizes = v; });
  seg('seg-view', 'v', (v) => { state.view = v; }, false);

  /* активная кнопка порогов — из state (пороги приходят из конфига,
     зашитая в шаблоне подсветка врала бы при нестандартных порогах) */
  const markSeg = (id, attr, val) => {
    document.querySelectorAll('#' + id + ' button').forEach((b) =>
      b.classList.toggle('active', +b.dataset[attr] === val));
  };
  markSeg('seg-units', 'u', state.units);
  markSeg('seg-sizes', 's', state.minSizes);
}

/* ── KPI ── */
function agg(idxs) {
  const a = { total: idxs.length, unavail: 0, free: 0, nocard: 0, nocardFree: 0, zero: 0, pres: [] };
  for (const i of idxs) {
    if (!unavail(i)) continue;
    a.unavail++;
    a.free += A.onec_free[i];
    if (!A.has_card[i]) { a.nocard++; a.nocardFree += A.onec_free[i]; }
    else { const c = presCnt(i, state.units); a.pres.push(c); if (c === 0) a.zero++; }
  }
  a.pres.sort((x, y) => x - y);
  a.medPres = a.pres.length ? a.pres[Math.floor(a.pres.length / 2)] : null;
  return a;
}

function renderKpis(idxs) {
  const a = agg(idxs);
  const kp = document.getElementById('kpis');
  const kpi = (l, v, s, cls = '') => `<div class="kpi ${cls}"><div class="l">${l}</div><div class="v">${v}</div><div class="s">${s}</div></div>`;
  kp.innerHTML =
    kpi('Недоступные артикулы', fmtN(a.unavail), delta(a.unavail, M.prev && M.prev.unavailable) || `представлено <${state.minSizes} размеров >${state.units} шт`, 'hero') +
    kpi('Свободно на 1С', fmtN(a.free) + ' шт', `по недоступным · склад «${M.onec_storage}»`, 'bad') +
    kpi('Без карточки WB', fmtN(a.nocard), `${fmtN(a.nocardFree)} шт на 1С — вообще не выставлено`) +
    kpi('Ноль представленных', fmtN(a.zero), 'карточка есть, но ни одного размера >' + state.units + ' шт') +
    kpi('Медиана размеров', a.medPres != null ? a.medPres + ' из ' + state.minSizes : '—', 'среди недоступных с карточкой') +
    kpi('Артикулов в срезе', fmtN(a.total), `со свободным остатком 1С (всего ${fmtN(A.article.length)})`);
}

/* ── график по категориям ── */
let catsChart = null;
function renderCatsChart(idxs) {
  const el = document.getElementById('chart-cats');
  catsChart = catsChart || echarts.init(el);
  const byCat = new Map();
  for (const i of idxs) {
    if (!unavail(i)) continue;
    const c = A.cat1[i];
    let o = byCat.get(c);
    if (!o) { o = { key: c, label: c || '— без категории —', n: 0, card: 0, nocard: 0, free: 0 }; byCat.set(c, o); }    o.n++; o.free += A.onec_free[i];
    if (A.has_card[i]) o.card++; else o.nocard++;
  }
  const top = [...byCat.values()].sort((x, y) => y.free - x.free).slice(0, 12);
  const short = (s) => s.length > 22 ? s.slice(0, 21) + '…' : s;

  catsChart.clear();
  catsChart.setOption({
    animation: false,
    grid: { left: 48, right: 64, top: 30, bottom: 100 },
    legend: { top: 0, textStyle: { fontSize: 11 }, data: ['С карточкой', 'Без карточки WB', 'Шт на 1С'] },
    tooltip: { trigger: 'axis', axisPointer: 'shadow', confine: true },
    xAxis: { type: 'category', data: top.map((o) => short(o.label)), axisLabel: { fontSize: 10, interval: 0, rotate: 32, hideOverlap: false } },
    yAxis: [
      { type: 'value', name: 'артикулы', nameTextStyle: { fontSize: 10 } },
      { type: 'value', name: 'шт 1С', nameTextStyle: { fontSize: 10 }, splitLine: { show: false } },
    ],
    series: [
      { name: 'С карточкой', type: 'bar', stack: 's', data: top.map((o) => o.card), itemStyle: { color: C.violet }, barMaxWidth: 30 },
      { name: 'Без карточки WB', type: 'bar', stack: 's', data: top.map((o) => o.nocard), itemStyle: { color: C.grey } },
      { name: 'Шт на 1С', type: 'line', yAxisIndex: 1, data: top.map((o) => o.free), itemStyle: { color: C.red }, lineStyle: { width: 2 }, symbolSize: 4 },
    ],
  });
  catsChart.off('click');
  catsChart.on('click', (p) => {
    const hit = top[p.dataIndex];
    if (!hit) return;
    state.cat1 = hit.key || CAT_EMPTY; // пустая категория 1С — тоже фильтр, '' занят как «выкл»
    state.cat2 = '';
    wireSelects();
    render();
  });
}

/* ── таблица ── */
const COLS = [
  { t: 'Артикул', get: (i) => A.article[i], sub: (i) => A.brand[i], num: false, wide: true },
  { t: 'Категория', get: (i) => (A.cat2[i] ? A.cat2[i] : A.cat1[i]), key: (i) => A.cat1[i] + '/' + A.cat2[i], num: false },
  { t: 'Сезон', get: (i) => A.season[i] || '—', num: false },
  { t: 'Год', get: (i) => A.year[i], num: true, fmt: (v) => (v ? v : '—') },
  { t: 'Размеров', get: (i) => A.has_card[i] ? presCnt(i, state.units) + '/' + A.sizes_total[i] : '—', key: (i) => A.has_card[i] ? presCnt(i, state.units) : -1, num: true, cls: (i) => !A.has_card[i] ? '' : (presCnt(i, state.units) < state.minSizes ? 'pct-bad' : 'pct-good') },
  { t: 'WB, шт', get: (i) => A.wb_qty[i], num: true },
  { t: '1С free, шт', get: (i) => A.onec_free[i], num: true },
  { t: '1С резерв', get: (i) => A.onec_reserv[i], num: true },
  { t: 'Вердикт', get: (i) => verdict(i).rank, num: true, badge: (i) => verdict(i) },
];

function verdict(i) {
  if (!A.has_card[i]) return { cls: 'wait', txt: 'Нет карточки WB', rank: 2 };
  return unavail(i)
    ? { cls: 'bad', txt: 'Недоступен', rank: 1 }
    : { cls: 'ok', txt: 'Доступен', rank: 0 };
}

function renderTable(idxs) {
  const title = document.getElementById('tbl-title').firstChild;
  const viewName = { unavail: 'Недоступные артикулы', all: 'Все артикулы среза', nocard: 'Артикулы без карточки WB' }[state.view];
  title.textContent = viewName + ' ';

  const tbl = document.getElementById('tbl');
  const view = [...idxs];
  const col = COLS[state.sortCol];
  const keyOf = col.key || col.get;
  view.sort((a, b) => {
    let va = keyOf(a), vb = keyOf(b);
    if (va === null) va = -Infinity; if (vb === null) vb = -Infinity;
    const c = col.num ? va - vb : String(va).localeCompare(String(vb), 'ru');
    return c * state.sortDir || a - b;
  });
  // таблица тяжёлеет на десятках тысяч строк — режем до 500 с пометкой
  const CAP = 500;
  const shown = view.slice(0, CAP);

  tbl.querySelector('thead').innerHTML = '<tr>' + COLS.map((c, j) =>
    `<th class="${c.num ? '' : 'tl'}${j === state.sortCol ? ' sorted' : ''}" data-j="${j}">${c.t}${c.num ? '' : ' ↕'}</th>`).join('') + '</tr>';
  tbl.querySelector('tbody').innerHTML = shown.map((i) => {
    const v = verdict(i);
    return '<tr data-i="' + i + '">' + COLS.map((c) => {
      if (c.badge) return `<td class="tl"><span class="verdict ${v.cls}">${v.txt}</span></td>`;
      const raw = c.get(i);
      const txt = raw === null ? '—' : (c.fmt ? c.fmt(raw) : esc(raw));
      const cls = (c.cls ? c.cls(i) : '') + (c.num ? '' : ' tl');
      if (c.sub) return `<td class="tl"><div class="nm">${txt}</div><div class="sid">${esc(c.sub(i))}</div></td>`;
      return `<td class="${cls}">${txt}</td>`;
    }).join('') + '</tr>';
  }).join('') || '<tr><td colspan="9" class="empty">Нет артикулов под текущие фильтры</td></tr>';

  if (view.length > CAP) {
    tbl.querySelector('tbody').insertAdjacentHTML('beforeend',
      `<tr><td colspan="9" class="empty" style="padding:10px">Показаны первые ${CAP} из ${fmtN(view.length)} — уточните фильтры или воспользуйтесь поиском</td></tr>`);
  }

  tbl.querySelectorAll('th').forEach((th) => th.onclick = () => {
    const j = +th.dataset.j;
    if (state.sortCol === j) state.sortDir *= -1; else { state.sortCol = j; state.sortDir = -1; }
    renderTable(idxs);
  });
  tbl.querySelectorAll('tbody tr[data-i]').forEach((tr) => tr.onclick = () => openDetail(+tr.dataset.i));
}

/* ── деталь артикула ── */
function openDetail(i) {
  state.sel = i;
  const v = verdict(i);
  document.getElementById('d-title').textContent = A.article[i];
  document.getElementById('d-sub').textContent =
    (A.brand[i] ? A.brand[i] + ' · ' : '') + (A.nm_id[i] ? 'nm ' + A.nm_id[i] : 'карточки WB нет');

  const arr = sizesOf.get(A.nm_id[i]) || [];
  const maxQ = Math.max(1, ...arr.map((k) => Z.qty[k]));
  const notPres = state.units === 0 ? '0 шт' : `≤ ${state.units} шт`;
  const rows = arr.map((k) => {
    const pres = Z.qty[k] > state.units;
    return `<tr><td class="tl">${esc(Z.size[k] || '—')}</td>` +
      `<td><span class="sz-bar" style="width:${Math.max(2, 60 * Z.qty[k] / maxQ)}px;${pres ? '' : 'background:var(--grey)'};opacity:${pres ? 1 : .55}"></span>${fmtN(Z.qty[k])}</td>` +
      `<td>${pres ? `<span class="verdict ok">представлен</span>` : `<span class="verdict bad">${notPres}</span>`}</td></tr>`;
  }).join('');

  document.getElementById('d-body').innerHTML =
    `<div class="badge-line"><span class="verdict ${v.cls}">${v.txt}</span>` +
    `<span class="verdict ${unavail(i) ? 'bad' : 'ok'}">${A.has_card[i] ? presCnt(i, state.units) + ' из ' + state.minSizes + ' размеров' : '0 размеров'}</span></div>` +
    `<div class="kv">
       <b>Категория</b><span>${esc(A.cat1[i] || '—')}${A.cat2[i] ? ' · ' + esc(A.cat2[i]) : ''}</span>
       <b>Сезон / год</b><span>${esc(A.season[i] || '—')} · ${A.year[i] || '—'}</span>
       <b>Размеров в карточке</b><span>${A.sizes_total[i]}</span>
       <b>Остаток WB</b><span>${fmtN(A.wb_qty[i])} шт (склады WB, снапшот ${M.stocks_date})</span>
       <b>1С свободно</b><span>${fmtN(A.onec_free[i])} шт (склад «${esc(M.onec_storage)}», ${M.rests_date})</span>
       <b>1С резерв</b><span>${fmtN(A.onec_reserv[i])} шт</span>
     </div>` +
    (A.has_card[i]
      ? `<h3 style="font-size:11px;font-weight:700;letter-spacing:.7px;text-transform:uppercase;color:var(--violet);margin:12px 0 4px">Сетка размеров</h3>` +
        `<table class="sizes-tbl"><thead><tr><th class="tl">Размер</th><th>WB, шт</th><th>Статус</th></tr></thead><tbody>${rows || '<tr><td colspan="3" class="empty">В карточке нет размеров</td></tr>'}</tbody></table>` +
        `<p style="font-size:11px;color:var(--ink-2);margin-top:8px">Полоса — остаток размера на складах WB относительно максимума сетки. Серые — не представлены (≤ ${state.units} шт при текущем пороге).</p>`
      : `<p style="font-size:12.5px;color:var(--ink-2);margin-top:10px">Карточки WB нет — размеры смотреть не у чего. Свободный остаток 1С: ${fmtN(A.onec_free[i])} шт.</p>`);

  document.getElementById('detail').classList.add('open');
  document.getElementById('detail-back').classList.add('open');
  document.querySelectorAll('tbody tr[data-i]').forEach((tr) => tr.classList.toggle('active', +tr.dataset.i === i));
}

function closeDetail() {
  state.sel = -1;
  document.getElementById('detail').classList.remove('open');
  document.getElementById('detail-back').classList.remove('open');
}
document.getElementById('d-close').onclick = closeDetail;
document.getElementById('detail-back').onclick = closeDetail;

/* ── глоссарий ── */
const gloss = document.getElementById('glossary');
document.getElementById('glossary-btn').onclick = () => { gloss.classList.add('open'); document.getElementById('glossary-back').classList.add('open'); };
const closeGloss = () => { gloss.classList.remove('open'); document.getElementById('glossary-back').classList.remove('open'); };
document.getElementById('glossary-close').onclick = closeGloss;
document.getElementById('glossary-back').onclick = closeGloss;
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') { closeGloss(); closeDetail(); } });

/* ── главный рендер ── */
function render() {
  renderHeader();
  const idxs = rowsInView();
  renderKpis(idxs);
  renderCatsChart(idxs);
  renderTable(idxs);
}
bindControls();
render();
