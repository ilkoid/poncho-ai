/* SLA-дашборд поставок на СЦ: день → таблица поставок → деталь.
   Все данные в #payload; сеть не нужна. Порог SLA переключается на клиенте:
   бакеты гистограмм выровнены по 12/24/36 ч. */
'use strict';

const P = JSON.parse(document.getElementById('payload').textContent);
const S = P.sups, M = P.meta;

const BUCKETS = ['0–6', '6–12', '12–18', '18–24', '24–36', '36–48', '48–72', '>72'];
const BUCKET_UP = [6, 12, 18, 24, 36, 48, 72, Infinity];
const ALL = '__ALL__';

const C = { green: '#018849', red: '#E41A2A', grey: '#9E9AA8', violet: '#7758B3', ink2: '#757575' };

const state = { day: ALL, thr: 24, sortCol: 0, sortDir: 1, sel: -1 };

/* ── индексы и агрегаты ── */
const days = [...new Set(S.day)].sort();
const idx = (arr) => arr;
const isScanned = (i) => S.scanned[i] !== '';
const isRejected = (i) => S.rejected[i] === 1;
const isOpen = (i) => !isScanned(i) && !isRejected(i);
const histSum = (i) => S.hist[i].reduce((a, b) => a + b, 0);
const okCount = (i, thr) => S.hist[i].reduce((a, b, k) => a + (BUCKET_UP[k] <= thr ? b : 0), 0);

/* МСК-время 'YYYY-MM-DD HH:MM' → Date: явный +03:00, чтобы парсинг
   не зависел от таймзоны и переходов DST просмотрщика. */
const mskDate = (s) => new Date(s.replace(' ', 'T') + ':00+03:00');

function verdict(i, thr) {
  if (isRejected(i)) return { cls: 'rej', txt: 'Отказ приёмки', rank: 3 };
  if (!isScanned(i)) {
    const from = S.first[i] || S.created[i];
    const ageH = from ? (Date.now() - mskDate(from).getTime()) / 36e5 : 0;
    if (ageH > 72) return { cls: 'wait', txt: `Ждёт приёмки · ${Math.floor(ageH / 24)} дн`, rank: 2 };
    return { cls: 'wait', txt: 'Ждёт приёмки', rank: 2 };
  }
  // Реальный отрицательный лаг (приёмка раньше первого задания) — аномалия
  // данных, а не «в срок»: sentinel −1 сюда не попадает (перехвачен выше).
  if (S.lag_h[i] < 0) return { cls: 'anom', txt: 'Аномалия: приёмка раньше задания', rank: 4 };
  return S.lag_h[i] <= thr ? { cls: 'ok', txt: 'В срок', rank: 0 } : { cls: 'bad', txt: 'Просрочена', rank: 1 };
}

/* поставки текущего выбора (день) в исходном порядке */
function rowsInView() {
  const out = [];
  for (let i = 0; i < S.id.length; i++) {
    if (state.day === ALL || S.day[i] === state.day) out.push(i);
  }
  return out;
}

/* агрегаты по выборке: поставки ok/bad/wait/anom, задания, медиана лага */
function agg(idxs, thr) {
  const a = { sup: idxs.length, ok: 0, bad: 0, rej: 0, wait: 0, anom: 0, tasks: 0, tasksOk: 0, tasksDen: 0, lags: [] };
  for (const i of idxs) {
    a.tasks += S.n_tasks[i];
    if (isRejected(i)) {
      a.rej++;
    } else if (!isScanned(i)) {
      a.wait++;
    } else if (S.lag_h[i] < 0) {
      // аномалия: отдельно от ok/bad, задания в знаменатель входят
      a.anom++;
      a.tasksDen += histSum(i);
      a.tasksOk += okCount(i, thr);
    } else {
      a.lags.push(S.lag_h[i]);
      const den = histSum(i), ok = okCount(i, thr);
      a.tasksDen += den; a.tasksOk += ok;
      if (S.lag_h[i] <= thr) a.ok++; else a.bad++;
    }
  }
  a.lags.sort((x, y) => x - y);
  a.medLag = a.lags.length ? a.lags[Math.floor(a.lags.length / 2)] : null;
  return a;
}

/* ── форматирование ── */
const fmtN = (v) => v.toLocaleString('ru-RU');
const fmtP = (num, den) => den > 0 ? (100 * num / den).toFixed(0) + '%' : '—';
function dayShort(d) { const [y, m, dd] = d.split('-'); return `${dd}.${m}`; }
function timeShort(ts) { return ts ? ts.slice(11) : '—'; }          // 'HH:MM'
function dtShort(ts) { return ts ? ts.slice(8, 10) + '.' + ts.slice(5, 7) + ' ' + ts.slice(11) : '—'; }
/* name поставки приходит из внешнего API (WB/WMS) и может содержать что
   угодно — вставляется в innerHTML только через esc() */
const esc = (s) => String(s).replace(/[&<>"']/g, (ch) => (
  { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));

/* ── шапка ── */
function renderHeader() {
  document.getElementById('subtitle').textContent =
    `${M.from.slice(8, 10)}.${M.from.slice(5, 7)}–${M.to.slice(8, 10)}.${M.to.slice(5, 7)}.${M.to.slice(0, 4)} · ` +
    `${fmtN(M.supplies)} поставок · ${fmtN(M.tasks)} заданий · сгенерирован ${M.generated_at}`;
  const b = document.getElementById('db-badge');
  if (M.db === 'wb_data_prod') { b.textContent = 'прод'; b.classList.remove('test'); }
  else { b.textContent = 'тест: ' + M.db; b.classList.add('test'); }

  const box = document.getElementById('days');
  box.innerHTML = '';
  const mk = (day, html) => {
    const el = document.createElement('button');
    el.className = 'day-chip' + (state.day === day ? ' active' : '');
    el.innerHTML = html;
    el.onclick = () => { state.day = day; render(); };
    box.appendChild(el);
  };
  mk(ALL, `<b>Все дни</b><span>${fmtN(S.id.length)} поставок</span>`);
  const perDay = {};
  for (let i = 0; i < S.id.length; i++) (perDay[S.day[i]] ??= []).push(i);
  for (const d of days) {
    const a = agg(perDay[d], state.thr);
    const mini = `<div class="mini">
      <i style="background:${C.green};width:${Math.max(a.ok ? 10 : 0, 30 * a.ok / Math.max(1, a.sup))}px"></i>
      <i style="background:${C.red};width:${30 * a.bad / Math.max(1, a.sup)}px"></i>
      <i style="background:${C.grey};width:${30 * (a.wait + a.rej) / Math.max(1, a.sup)}px"></i></div>`;
    mk(d, `<b>${dayShort(d)}</b><span>${a.sup} пост · ${fmtN(a.tasks)} зад.</span>${mini}`);
  }
  // активная прокрутка к выбранному дню
  const act = box.querySelector('.day-chip.active');
  if (act) act.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

/* ── KPI ── */
function renderKpis(idxs) {
  const a = agg(idxs, state.thr);
  const kp = document.getElementById('kpis');
  const kpi = (l, v, s, cls = '') => `<div class="kpi ${cls}"><div class="l">${l}</div><div class="v">${v}</div><div class="s">${s}</div></div>`;
  const verdicts = a.ok + a.bad;
  kp.innerHTML =
    kpi('Поставок', fmtN(a.sup), a.wait + a.rej + a.anom > 0 ? `${a.wait} ждут · ${a.rej} отказ${a.anom ? ` · ${a.anom} аномал` : ''}` : 'все завершены', 'hero') +
    kpi('Заданий', fmtN(a.tasks), 'сборочных в поставках') +
    kpi('Поставок в срок', fmtP(a.ok, verdicts), `≤${state.thr}ч от 1-го задания · ${a.ok} из ${verdicts}`, verdicts && a.ok >= a.bad ? 'good' : 'bad') +
    kpi('Заданий в срок', fmtP(a.tasksOk, a.tasksDen), 'по своим часам каждого задания', a.tasksDen && a.tasksOk >= a.tasksDen / 2 ? 'good' : 'bad') +
    kpi('Медиана лага', a.medLag != null ? a.medLag.toFixed(1) + ' ч' : '—', '1-е задание → приёмка') +
    kpi('Цель', state.thr + ' ч', 'окно SLA, переключатель в шапке');
}

/* ── график дней ── */
let daysChart = null;
function renderDaysChart(thr) {
  const el = document.getElementById('chart-days');
  daysChart = daysChart || echarts.init(el);
  const perDay = {};
  for (const d of days) perDay[d] = [];
  for (let i = 0; i < S.id.length; i++) perDay[S.day[i]].push(i);

  const labels = [], ok = [], bad = [], wait = [], rej = [], tasks = [];
  for (const d of days) {
    const a = agg(perDay[d], thr);
    labels.push(dayShort(d));
    ok.push(a.ok); bad.push(a.bad); wait.push(a.wait); rej.push(a.rej);
    tasks.push(a.tasks);
  }
  daysChart.clear();
  daysChart.setOption({
    animation: false,
    grid: { left: 48, right: 56, top: 30, bottom: 40 },
    legend: { top: 0, textStyle: { fontSize: 11 }, data: ['В срок', 'Просрочена', 'Ждёт приёмки', 'Отказ приёмки', 'Заданий'] },
    tooltip: { trigger: 'axis', axisPointer: 'shadow', confine: true },
    xAxis: { type: 'category', data: labels, axisLabel: { fontSize: 10.5 } },
    yAxis: [
      { type: 'value', name: 'поставки', nameTextStyle: { fontSize: 10 } },
      { type: 'value', name: 'заданий', nameTextStyle: { fontSize: 10 }, splitLine: { show: false } },
    ],
    series: [
      { name: 'В срок', type: 'bar', stack: 's', data: ok, itemStyle: { color: C.green }, barMaxWidth: 26 },
      { name: 'Просрочена', type: 'bar', stack: 's', data: bad, itemStyle: { color: C.red } },
      { name: 'Ждёт приёмки', type: 'bar', stack: 's', data: wait, itemStyle: { color: C.grey } },
      { name: 'Отказ приёмки', type: 'bar', stack: 's', data: rej, itemStyle: { color: '#FF8000' } },
      { name: 'Заданий', type: 'line', yAxisIndex: 1, data: tasks, itemStyle: { color: C.violet }, lineStyle: { width: 2 }, symbolSize: 4 },
    ],
  });
  daysChart.off('click');
  daysChart.on('click', (p) => { if (labels.includes(p.name)) { state.day = days[labels.indexOf(p.name)]; render(); } });
}

/* ── таблица ── */
/* key — ключ сортировки (по умолчанию get): «Формир.» сортируется по ISO-строке
   S.created (хронологически), а не по отображению 'HH:MM dd.MM'; «Вердикт» —
   по семантическому rank (В срок < Просрочена < Ждёт < Отказ < Аномалия),
   а не лексикографически. */
const COLS = [
  { t: 'Формир.', get: (i) => S.created[i].slice(11) + ' ' + dayShort(S.created[i].slice(0, 10)), key: (i) => S.created[i], num: false },
  { t: 'Поставка', get: (i) => S.name[i], sub: (i) => S.id[i], num: false, wide: true },
  { t: '1-е задание', get: (i) => dtShort(S.first[i]), num: false },
  { t: 'Лаг, ч', get: (i) => S.lag_h[i] < 0 ? null : S.lag_h[i], num: true, fmt: (v) => v.toFixed(1) },
  { t: 'Закрыта', get: (i) => timeShort(S.closed[i]), num: false },
  { t: 'Приёмка', get: (i) => timeShort(S.scanned[i]), num: false },
  { t: 'Заданий', get: (i) => S.n_tasks[i], num: true },
  { t: 'В срок %', get: (i) => { const den = histSum(i); return den ? okCount(i, state.thr) / den : null; }, num: true, fmt: (v) => (100 * v).toFixed(0) + '%', cls: (i) => { const den = histSum(i); if (!den) return ''; return okCount(i, state.thr) / den >= 0.5 ? 'pct-good' : 'pct-bad'; } },
  { t: 'Вердикт', get: (i) => verdict(i, state.thr).rank, num: true, badge: (i) => verdict(i, state.thr) },
];

function renderTable(idxs) {
  document.getElementById('tbl-title').firstChild.textContent =
    state.day === ALL ? 'Все поставки ' : `Поставки ${dayShort(state.day)} `;
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

  tbl.querySelector('thead').innerHTML = '<tr>' + COLS.map((c, j) =>
    `<th class="${c.num ? '' : 'tl'}${j === state.sortCol ? ' sorted' : ''}" data-j="${j}">${c.t}${c.num ? '' : ' ↕'}</th>`).join('') + '</tr>';
  tbl.querySelector('tbody').innerHTML = view.map((i) => {
    const v = verdict(i, state.thr);
    return '<tr data-i="' + i + '">' + COLS.map((c, j) => {
      if (c.badge) return `<td class="tl"><span class="verdict ${v.cls}">${v.txt}</span></td>`;
      const raw = c.get(i);
      const txt = raw === null ? '—' : (c.fmt ? c.fmt(raw) : esc(raw));
      const cls = (c.cls ? c.cls(i) : '') + (c.num ? '' : ' tl');
      if (c.sub) return `<td class="tl"><div class="nm">${txt}</div><div class="sid">${esc(c.sub(i))}</div></td>`;
      return `<td class="${cls}">${txt}</td>`;
    }).join('') + '</tr>';
  }).join('') || '<tr><td colspan="9" class="empty">Нет поставок за выбранный день</td></tr>';

  tbl.querySelectorAll('th').forEach((th) => th.onclick = () => {
    const j = +th.dataset.j;
    if (state.sortCol === j) state.sortDir *= -1; else { state.sortCol = j; state.sortDir = 1; }
    renderTable(idxs);
  });
  tbl.querySelectorAll('tbody tr').forEach((tr) => tr.onclick = () => openDetail(+tr.dataset.i));
}

/* ── деталь поставки ── */
function openDetail(i) {
  state.sel = i;
  const thr = state.thr;
  const den = histSum(i), ok = okCount(i, thr);
  const v = verdict(i, thr);
  document.getElementById('d-title').textContent = S.name[i] || S.id[i];
  document.getElementById('d-sub').textContent = S.id[i] + ' · ' + dayShort(S.day[i]);

  const medWait = (() => {
    let acc = 0;
    for (let k = 0; k < 8; k++) {
      const prev = acc; acc += S.hist[i][k];
      if (acc >= den / 2 && den) {
        const lo = k ? BUCKET_UP[k - 1] : 0;
        return ((lo + Math.min(BUCKET_UP[k], lo + 6)) / 2).toFixed(1);
      }
    }
    return null;
  })();

  const tlRow = (t, label, gh) =>
    `<div class="tl-row"><span class="t">${t || '—'}</span><span class="l">${label}</span>` +
    (gh != null ? `<span class="gap">+${gh} ч</span>` : '') + '</div>';

  const body = document.getElementById('d-body');
  body.innerHTML =
    `<div class="badge-line"><span class="verdict ${v.cls}">${v.txt}</span>` +
    `<span class="verdict ${ok / Math.max(1, den) >= 0.5 ? 'ok' : 'bad'}">заданий в срок ${fmtP(ok, den)}</span>` +
    (S.cancel_pre[i] ? `<span class="verdict wait">отмен до приёмки: ${S.cancel_pre[i]}</span>` : '') + `</div>` +
    `<div class="tl-list">` +
    tlRow(dtShort(S.first[i]), 'первое сборочное задание — старт SLA-часов', null) +
    tlRow(dtShort(S.created[i]), 'поставка сформирована (WB-GI создан)', gapH(S.created[i], S.first[i]) != null ? Math.abs(gapH(S.created[i], S.first[i])).toFixed(1).replace('.0', '') : null) +
    tlRow(dtShort(S.closed[i]), 'закрыта, передана в доставку', gapH(S.closed[i], S.created[i]) != null ? gapH(S.closed[i], S.created[i]).toFixed(1).replace('.0', '') : null) +
    tlRow(dtShort(S.scanned[i]), 'принята на сортировочном центре — обязательства выполнены', S.lag_h[i] >= 0 ? S.lag_h[i].toFixed(1) : null) +
    `</div>` +
    `<div class="kv">
       <b>Заданий</b><span>${fmtN(S.n_tasks[i])}</span>
       <b>В срок (≤${thr} ч)</b><span class="${ok / Math.max(1, den) >= 0.5 ? 'pct-good' : 'pct-bad'}">${fmtN(ok)} (${fmtP(ok, den)})</span>
       <b>Провалено</b><span class="pct-bad">${fmtN(den - ok)} (${fmtP(den - ok, den)})</span>
       <b>Отменено до приёмки</b><span>${S.cancel_pre[i]} (из знаменателя исключены)</span>
       <b>Отменено после приёмки</b><span>${S.cancel_post[i]} (покупатель при получении / поздняя отмена — в знаменателе)</span>
       <b>Медиана ожидания задания</b><span>${medWait ? '≈' + medWait + ' ч' : '—'}</span>
     </div>` +
    `<div id="d-hist" style="height:220px"></div>` +
    `<p style="font-size:11px;color:var(--ink-2);margin-top:8px">Гистограмма — сколько заданий ждало приёмки соответствующее число часов от своего создания. Зелёные бакеты — в пределах цели ${thr} ч, красные — провал.</p>`;

  const hc = echarts.init(document.getElementById('d-hist'));
  hc.setOption({
    animation: false, grid: { left: 40, right: 12, top: 24, bottom: 28 },
    tooltip: { confine: true },
    xAxis: { type: 'category', data: BUCKETS, axisLabel: { fontSize: 10 } },
    yAxis: { type: 'value', axisLabel: { fontSize: 10 } },
    series: [{
      type: 'bar', barMaxWidth: 34,
      data: S.hist[i].map((v, k) => ({
        value: v,
        itemStyle: { color: BUCKET_UP[k] <= thr ? C.green : C.red },
      })),
    }],
  });

  document.getElementById('detail').classList.add('open');
  document.getElementById('detail-back').classList.add('open');
  document.querySelectorAll('tbody tr').forEach((tr) => tr.classList.toggle('active', +tr.dataset.i === i));
}

function closeDetail() {
  state.sel = -1;
  document.getElementById('detail').classList.remove('open');
  document.getElementById('detail-back').classList.remove('open');
}
document.getElementById('d-close').onclick = closeDetail;
document.getElementById('detail-back').onclick = closeDetail;

/* часы между 'YYYY-MM-DD HH:MM' строками МСК (b − a); mskDate парсит с явным
   смещением +03:00 — разность не зависит от локали просмотрщика и DST */
function gapH(b, a) {
  if (!a || !b) return null;
  return (mskDate(b).getTime() - mskDate(a).getTime()) / 36e5;
}

/* ── управление ── */
document.querySelectorAll('#seg-threshold button').forEach((b) => b.onclick = () => {
  document.querySelectorAll('#seg-threshold button').forEach((x) => x.classList.remove('active'));
  b.classList.add('active');
  state.thr = +b.dataset.h;
  render();
  if (state.sel >= 0) openDetail(state.sel);
});

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
  renderDaysChart(state.thr);
  renderTable(idxs);
}
render();
