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
  cat1: '', cat2: '', year: '', search: '',
  seasons: new Set(),       // выбранные сезоны; пустое множество = все
  units: M.min_units,       // размер представлен ⇔ qty > units
  minSizes: M.min_sizes,    // недоступен ⇔ представлено < minSizes
  view: 'unavail',          // unavail | all | nocard
  mode: 'deficit',          // deficit (v1: каталог дефицита) | recovery (v1.5: план восстановления)
  gapBucket: null,          // recovery-фильтр: 1|2|3|'zero'|'nocard'; null = все бакеты
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

/* ── recovery (v1.5): добор — сколько размеров поднять выше порога до «оживёт» ── */
function gapOf(i) {
  if (!A.has_card[i]) return -1;             // сетки нет — добор неприменим
  return Math.max(0, state.minSizes - presCnt(i, state.units));
}
/* бакет плана восстановления: 1|2|3 (3 = «+3 и больше»), 'zero', 'nocard' */
function bucketOf(i) {
  if (!A.has_card[i]) return 'nocard';
  if (presCnt(i, state.units) === 0) return 'zero';
  return Math.min(3, gapOf(i));
}
const gapTxt = (g) => (g === 1 ? '+1 размер' : g === 2 ? '+2 размера' : '+' + g + '+ размеров');

function verdictRec(i) {
  if (!unavail(i)) return { cls: 'ok', txt: 'Доступен', rank: 0 };
  const b = bucketOf(i);
  if (b === 'nocard') return { cls: 'wait', txt: 'Нет карточки WB', rank: 5 };
  if (b === 'zero') return { cls: 'bad', txt: 'В нуле', rank: 4 };
  return { cls: b === 1 ? 'quick' : 'mid', txt: gapTxt(gapOf(i)), rank: b };
}

/* ── фильтры ── */
function rowsInView() {
  const q = state.search.trim().toUpperCase();
  const c1 = state.cat1 === CAT_EMPTY ? '' : state.cat1;
  const out = [];
  for (let i = 0; i < A.article.length; i++) {
    if (state.cat1 && A.cat1[i] !== c1) continue;
    if (state.cat2 && A.cat2[i] !== state.cat2) continue;
    if (state.year && String(A.year[i] || '—') !== state.year) continue;
    if (state.seasons.size && !state.seasons.has(A.season[i])) continue;
    if (q && !A.article[i].toUpperCase().includes(q)) continue;
    if (state.view === 'unavail' && !unavail(i)) continue;
    if (state.view === 'nocard' && A.has_card[i]) continue;
    if (state.mode === 'recovery' && state.gapBucket !== null && bucketOf(i) !== state.gapBucket) continue;
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
  for (const s of [...state.seasons].sort((a, b) => a.localeCompare(b, 'ru'))) {
    mk('Сезон', s, () => { state.seasons.delete(s); syncSeasonUI(); render(); });
  }
  if (state.mode === 'recovery' && state.gapBucket !== null) {
    const names = { 1: '+1 размер', 2: '+2 размера', 3: '+3+ размеров', zero: 'в нуле', nocard: 'без карточки' };
    const val = state.gapBucket;
    mk('Добор', names[val], () => { state.gapBucket = null; render(); });
  }
  mk('Год', state.year, () => { state.year = ''; wireSelects(); render(); });
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
  buildSeasonPop();
}

/* ── мультиселект сезонов: дропдаун с чекбоксами и счётчиком артикулов ── */
const seasonCnt = (() => {
  const m = new Map();
  for (const s of A.season) if (s) m.set(s, (m.get(s) || 0) + 1);
  return m;
})();

function buildSeasonPop() {
  const pop = document.getElementById('f-season-pop');
  const items = [...seasonCnt.keys()].sort((a, b) => a.localeCompare(b, 'ru')).map((s) =>
    `<label class="msel-item"><input type="checkbox" value="${esc(s)}"${state.seasons.has(s) ? ' checked' : ''}>` +
    `<span>${esc(s)}</span><span class="cnt">${fmtN(seasonCnt.get(s))}</span></label>`).join('');
  pop.innerHTML = `<button type="button" class="msel-all">Все сезоны</button>` + items;
  pop.querySelector('.msel-all').onclick = () => { state.seasons.clear(); syncSeasonUI(); render(); };
  pop.querySelectorAll('input').forEach((cb) => cb.onchange = () => {
    if (cb.checked) state.seasons.add(cb.value); else state.seasons.delete(cb.value);
    syncSeasonUI();
    render();
  });
}

/* кнопка и открытый дропдаун синхронизируются с state.seasons */
function syncSeasonUI() {
  const btn = document.getElementById('f-season-btn');
  const n = state.seasons.size;
  btn.classList.toggle('active', n > 0);
  btn.childNodes[0].nodeValue = n === 0 ? 'Все сезоны'
    : n === 1 ? [...state.seasons][0]
    : n + ' ' + ((n % 10 >= 2 && n % 10 <= 4 && (n % 100 < 12 || n % 100 > 14)) ? 'сезона' : 'сезонов');
  buildSeasonPop();
}

function bindControls() {
  wireSelects();
  document.getElementById('f-cat1').onchange = (e) => { state.cat1 = e.target.value; state.cat2 = ''; wireSelects(); render(); };
  document.getElementById('f-cat2').onchange = (e) => { state.cat2 = e.target.value; render(); };
  document.getElementById('f-year').onchange = (e) => { state.year = e.target.value; render(); };
  let tmr = null;
  document.getElementById('f-search').oninput = (e) => {
    clearTimeout(tmr);
    tmr = setTimeout(() => { state.search = e.target.value; render(); }, 150);
  };

  /* мультиселект сезонов: открыть/закрыть, клик мимо закрывает */
  const msel = document.getElementById('m-season');
  document.getElementById('f-season-btn').onclick = (e) => {
    e.stopPropagation();
    msel.classList.toggle('open');
  };
  document.addEventListener('click', (e) => { if (!msel.contains(e.target)) msel.classList.remove('open'); });

  /* сегменты-переключатели: пороги — числа, view/mode — строки;
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
  const markSeg = (id, attr, val, num = true) => {
    document.querySelectorAll('#' + id + ' button').forEach((b) =>
      b.classList.toggle('active', num ? +b.dataset[attr] === val : b.dataset[attr] === val));
  };
  markSeg('seg-units', 'u', state.units);
  markSeg('seg-sizes', 's', state.minSizes);

  /* режим интерпретации: дефицит (v1) / восстановление (v1.5) */
  seg('seg-mode', 'm', (v) => { state.mode = v; state.gapBucket = null; applyModeSort(); }, false);
  markSeg('seg-mode', 'm', state.mode, false);
}

/* сортировка по умолчанию своего режима: дефицит — 1С free↓, восстановление — добор↑ */
function applyModeSort() {
  if (state.mode === 'recovery') { state.sortCol = 5; state.sortDir = 1; }
  else { state.sortCol = 6; state.sortDir = -1; }
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

/* recovery (v1.5): лестница добора — сколько артикулов/шт 1С у каждого шага */
function aggRecovery(idxs) {
  const a = { total: idxs.length, ok: 0, potFree: 0,
    b: { 1: { n: 0, free: 0 }, 2: { n: 0, free: 0 }, 3: { n: 0, free: 0 }, zero: { n: 0, free: 0 }, nocard: { n: 0, free: 0 } } };
  for (const i of idxs) {
    if (!unavail(i)) { a.ok++; continue; }
    a.potFree += A.onec_free[i];
    const cell = a.b[bucketOf(i)];
    cell.n++; cell.free += A.onec_free[i];
  }
  return a;
}

const kpiCard = (l, v, s, cls = '') => `<div class="kpi ${cls}"><div class="l">${l}</div><div class="v">${v}</div><div class="s">${s}</div></div>`;

function renderKpis(idxs) {
  const a = agg(idxs);
  document.getElementById('kpis').innerHTML =
    kpiCard('Недоступные артикулы', fmtN(a.unavail), delta(a.unavail, M.prev && M.prev.unavailable) || `представлено <${state.minSizes} размеров >${state.units} шт`, 'hero') +
    kpiCard('Свободно на 1С', fmtN(a.free) + ' шт', `по недоступным · склад «${M.onec_storage}»`, 'bad') +
    kpiCard('Без карточки WB', fmtN(a.nocard), `${fmtN(a.nocardFree)} шт на 1С — вообще не выставлено`) +
    kpiCard('Ноль представленных', fmtN(a.zero), 'карточка есть, но ни одного размера >' + state.units + ' шт') +
    kpiCard('Медиана размеров', a.medPres != null ? a.medPres + ' из ' + state.minSizes : '—', 'среди недоступных с карточкой') +
    kpiCard('Артикулов в срезе', fmtN(a.total), `со свободным остатком 1С (всего ${fmtN(A.article.length)})`);
}

/* KPI плана восстановления: не «сколько пропало», а «что вернёт товар в продажу» */
function renderRecoveryKpis(idxs) {
  const a = aggRecovery(idxs);
  const f = (b) => fmtN(b.free) + ' шт на 1С';
  document.getElementById('kpis').innerHTML =
    kpiCard('Потенциал возврата', fmtN(a.potFree) + ' шт', `на 1С по недоступным · склад «${M.onec_storage}» — вернётся в продажу`, 'hero') +
    kpiCard('+1 размер', fmtN(a.b[1].n), f(a.b[1]) + ' · quick win: один поднятый размер оживит карточку', 'good') +
    kpiCard('+2 размера', fmtN(a.b[2].n), f(a.b[2])) +
    kpiCard('+3 и больше', fmtN(a.b[3].n), f(a.b[3])) +
    kpiCard('В нуле', fmtN(a.b.zero.n), f(a.b.zero) + ' · причина глубже добора', 'bad') +
    kpiCard('Без карточки', fmtN(a.b.nocard.n), f(a.b.nocard) + ' · товар вообще не выставлен');
}

/* ── график: категории (дефицит) или лестница добора (восстановление) ── */
let catsChart = null;

const CHART_HEAD = {
  deficit: ['Недоступность по категориям ',
    'столбики — недоступные артикулы: фиолетовые с карточкой, серые без карточки WB · линия — свободный остаток 1С по недоступным, шт · топ-12 категорий · клик по столбику — фильтр категории'],
  recovery: ['Путь к оживлению · добор размеров ',
    'столбики — сколько размеров добрать до порога: «+1» ближе всех к деньгам · линия — свободный остаток 1С, шт · клик по столбику — фильтр по добору'],
};

function renderChartHead() {
  const t = document.getElementById('chart-title');
  t.firstChild.nodeValue = CHART_HEAD[state.mode][0];
  t.querySelector('.hint').textContent = CHART_HEAD[state.mode][1];
}

function renderRecoveryChart(idxs) {
  const el = document.getElementById('chart-cats');
  catsChart = catsChart || echarts.init(el);
  const keys = [1, 2, 3, 'zero', 'nocard'];
  const labels = ['+1', '+2', '+3+', 'в нуле', 'нет карточки'];
  const by = {};
  keys.forEach((k) => { by[k] = { n: 0, free: 0 }; });
  for (const i of idxs) {
    if (!unavail(i)) continue;
    const b = by[bucketOf(i)];
    b.n++; b.free += A.onec_free[i];
  }
  catsChart.clear();
  catsChart.setOption({
    animation: false,
    grid: { left: 48, right: 64, top: 30, bottom: 60 },
    legend: { top: 0, textStyle: { fontSize: 11 }, data: ['Артикулов', 'Шт на 1С'] },
    tooltip: { trigger: 'axis', axisPointer: 'shadow', confine: true },
    xAxis: { type: 'category', data: labels, axisLabel: { fontSize: 10, interval: 0 } },
    yAxis: [
      { type: 'value', name: 'артикулы', nameTextStyle: { fontSize: 10 } },
      { type: 'value', name: 'шт 1С', nameTextStyle: { fontSize: 10 }, splitLine: { show: false } },
    ],
    series: [
      { name: 'Артикулов', type: 'bar', data: keys.map((k) => by[k].n), itemStyle: { color: C.violet }, barMaxWidth: 44 },
      { name: 'Шт на 1С', type: 'line', yAxisIndex: 1, data: keys.map((k) => by[k].free), itemStyle: { color: C.red }, lineStyle: { width: 2 }, symbolSize: 5 },
    ],
  });
  catsChart.off('click');
  catsChart.on('click', (p) => {
    if (p.dataIndex == null || !keys[p.dataIndex]) return;
    const k = keys[p.dataIndex];
    state.gapBucket = state.gapBucket === k ? null : k; // повторный клик снимает
    render();
  });
}

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

/* колонки плана восстановления: вместо «Вердикт» — «Добрать» (gap + free
   в одном ключе сортировки: сначала меньше добора, внутри — больше 1С) */
const COLS_REC = [
  { t: 'Артикул', get: (i) => A.article[i], sub: (i) => A.brand[i], num: false, wide: true },
  { t: 'Категория', get: (i) => (A.cat2[i] ? A.cat2[i] : A.cat1[i]), key: (i) => A.cat1[i] + '/' + A.cat2[i], num: false },
  { t: 'Сезон', get: (i) => A.season[i] || '—', num: false },
  { t: 'Год', get: (i) => A.year[i], num: true, fmt: (v) => (v ? v : '—') },
  { t: 'Сетка', get: (i) => A.has_card[i] ? presCnt(i, state.units) + '/' + A.sizes_total[i] : '—', key: (i) => A.has_card[i] ? presCnt(i, state.units) : -1, num: true },
  { t: 'Добрать', num: true,
    /* приоритет плана: лестница добора (1..N) → доступные (50, чинить нечего)
       → без карточки (99, это не добор, а выставление) */
    key: (i) => (!A.has_card[i] ? 99 : !unavail(i) ? 50 : Math.max(0, gapOf(i))) * 1e15
      + (1e15 - Math.min(A.onec_free[i], 1e14)),
    html: (i) => { const v = verdictRec(i); return `<span class="verdict ${v.cls}">${v.txt}</span>`; } },
  { t: '1С free, шт', get: (i) => A.onec_free[i], num: true },
  { t: '1С резерв', get: (i) => A.onec_reserv[i], num: true },
];

function renderTable(idxs) {
  const rec = state.mode === 'recovery';
  const cols = rec ? COLS_REC : COLS;
  const vf = rec ? verdictRec : verdict;
  const title = document.getElementById('tbl-title').firstChild;
  const viewName = { unavail: 'Недоступные артикулы', all: 'Все артикулы среза', nocard: 'Артикулы без карточки WB' }[state.view];
  title.textContent = (rec ? 'План восстановления · ' : '') + viewName + ' ';

  const tbl = document.getElementById('tbl');
  const view = [...idxs];
  const col = cols[state.sortCol];
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

  tbl.querySelector('thead').innerHTML = '<tr>' + cols.map((c, j) =>
    `<th class="${c.num ? '' : 'tl'}${j === state.sortCol ? ' sorted' : ''}" data-j="${j}">${c.t}${c.num ? '' : ' ↕'}</th>`).join('') + '</tr>';
  tbl.querySelector('tbody').innerHTML = shown.map((i) => {
    const v = vf(i);
    return '<tr data-i="' + i + '">' + cols.map((c) => {
      if (c.html) return `<td class="tl">${c.html(i)}</td>`;
      if (c.badge) return `<td class="tl"><span class="verdict ${v.cls}">${v.txt}</span></td>`;
      const raw = c.get(i);
      const txt = raw === null ? '—' : (c.fmt ? c.fmt(raw) : esc(raw));
      const cls = (c.cls ? c.cls(i) : '') + (c.num ? '' : ' tl');
      if (c.sub) return `<td class="tl"><div class="nm">${txt}</div><div class="sid">${esc(c.sub(i))}</div></td>`;
      return `<td class="${cls}">${txt}</td>`;
    }).join('') + '</tr>';
  }).join('') || `<tr><td colspan="${cols.length}" class="empty">Нет артикулов под текущие фильтры</td></tr>`;

  if (view.length > CAP) {
    tbl.querySelector('tbody').insertAdjacentHTML('beforeend',
      `<tr><td colspan="${cols.length}" class="empty" style="padding:10px">Показаны первые ${CAP} из ${fmtN(view.length)} — уточните фильтры или воспользуйтесь поиском</td></tr>`);
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
    `<span class="verdict ${unavail(i) ? 'bad' : 'ok'}">${A.has_card[i] ? presCnt(i, state.units) + ' из ' + state.minSizes + ' размеров' : '0 размеров'}</span>` +
    (state.mode === 'recovery' && unavail(i) && A.has_card[i]
      ? `<span class="verdict ${bucketOf(i) === 1 ? 'quick' : 'mid'}">добрать ${gapTxt(gapOf(i)).replace('+', '')}</span>` : '') +
    `</div>` +
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
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    document.getElementById('m-season').classList.remove('open');
    closeGloss();
    closeDetail();
  }
});

/* ── главный рендер ── */
function render() {
  renderHeader();
  renderChartHead();
  const idxs = rowsInView();
  if (state.mode === 'recovery') { renderRecoveryKpis(idxs); renderRecoveryChart(idxs); }
  else { renderKpis(idxs); renderCatsChart(idxs); }
  renderTable(idxs);
}
bindControls();
render();
