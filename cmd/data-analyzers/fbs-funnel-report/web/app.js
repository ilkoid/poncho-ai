/* FBS-дашборд: все данные в #payload (JSON-куб), фильтры считает браузер.
   Статусы ленты: buyout=выкуп, cancel=отмена, return/returnDefective=возврат,
   прочее (created…) = в пути. Деньги — цена продавца без комиссий WB. */
'use strict';

/* Видимый фейл-фаст: любая ошибка приложения показывается в шапке. */
window.addEventListener('error', e => {
  const el = document.getElementById('chips');
  if (el) el.innerHTML = '<span class="chip" style="background:#FDEEEE;border-color:#E41A2A;color:#E41A2A">Ошибка: ' +
    (e.message || 'неизвестная') + '</span>';
});

const $ = s => document.querySelector(s);
const D = JSON.parse($('#payload').textContent);
const F = D.facts, DIM = D.dims, META = D.meta;
const DAY = 86400000;

/* ── форматирование ── */
const nf = new Intl.NumberFormat('ru-RU');
const fmtInt = v => nf.format(Math.round(v));
const fmtRub = v => nf.format(Math.round(v)) + ' \u20BD';
const fmtShort = v => {
  const a = Math.abs(v);
  if (a >= 1e9) return (v / 1e9).toLocaleString('ru-RU', {maximumFractionDigits: 2}) + ' млрд';
  if (a >= 1e6) return (v / 1e6).toLocaleString('ru-RU', {maximumFractionDigits: 1}) + ' млн';
  if (a >= 1e4) return Math.round(v / 1e3).toLocaleString('ru-RU') + ' тыс';
  return nf.format(Math.round(v));
};
const fmtPct = v => (v < 0 ? '—' : v.toLocaleString('ru-RU', {maximumFractionDigits: 1}) + '%');
const dd = s => s.slice(8, 10) + '.' + s.slice(5, 7);
const dmy = s => s.slice(8, 10) + '.' + s.slice(5, 7) + '.' + s.slice(0, 4);
const d2t = s => Date.parse(s + 'T00:00:00Z');
const pct = (a, b) => b > 0 ? a / b * 100 : -1;

/* ── предрасчёты ── */
const N = F.nm.length;
const CLS = new Uint8Array(N);            // 0=в пути 1=выкуп 2=отмена 3=возврат
for (let i = 0; i < N; i++) {
  const s = DIM.status[F.status[i]];
  CLS[i] = s === 'buyout' ? 1 : s === 'cancel' ? 2 : (s === 'return' || s === 'returnDefective') ? 3 : 0;
}
const cat1List = [], cat1Idx = new Map();  // индексы категорий 1С (уровень 1)
const cat2List = [], cat2Idx = new Map();
const nmCat1 = new Int32Array(DIM.nm.length), nmCat2 = new Int32Array(DIM.nm.length);
DIM.nm.forEach((nm, i) => {
  const c1 = nm[5] || 'Прочее', c2 = nm[6] || '';
  if (!cat1Idx.has(c1)) { cat1Idx.set(c1, cat1List.length); cat1List.push(c1); }
  nmCat1[i] = cat1Idx.get(c1);
  if (c2) {
    if (!cat2Idx.has(c2)) { cat2Idx.set(c2, cat2List.length); cat2List.push(c2); }
    nmCat2[i] = cat2Idx.get(c2);
  } else nmCat2[i] = -1;
});
// недели: ключ = понедельник (UTC-мс) даты события
const weekK = DIM.event.map(s => { const t = d2t(s); return t - (new Date(t).getUTCDay() + 6) % 7 * DAY; });
const evT = DIM.event.map(d2t);
const lastT = evT.length ? evT[evT.length - 1] : 0;

/* ── состояние ── */
const state = {
  from: 0, to: Math.max(0, DIM.event.length - 1),
  gran: 'day', unit: 'rub',
  city: -1, cat1: -1, cat2: -1, nm: -1,
  sort: 'rubBuy', sortDir: -1,
};

/* ── агрегатор: один проход по фактам ── */
function compute() {
  const K = {rubBuy: 0, rubLost: 0, rubOrder: 0, pc: 0, pcBuy: 0, pcCan: 0, pcRet: 0, pcFly: 0};
  const ev = [];                                   // по датам событий (полный массив)
  for (let i = 0; i < DIM.event.length; i++) ev.push({rubBuy: 0, rubLost: 0, rubOrder: 0, pcBuy: 0, pcCan: 0, pcRet: 0, pcFly: 0});
  const coh = [];                                  // по когортам (без окна событий)
  for (let i = 0; i < DIM.cohort.length; i++) coh.push({cnt: 0, buy: 0, can: 0, ret: 0, fly: 0});
  const city = new Map(), cat1 = new Map(), cat2 = new Map(), nmAgg = new Map();
  const reasons = {app: 0, receipt: 0, expire: 0, other: 0, unknown: 0, returns: 0};
  const sparkEnd = evT[state.to], nmSpark = new Map();

  for (let i = 0; i < N; i++) {
    const nmI = F.nm[i];
    if (state.city >= 0 && F.city[i] !== state.city) continue;
    if (state.nm >= 0 && nmI !== state.nm) continue;
    if (state.cat1 >= 0 && nmCat1[nmI] !== state.cat1) continue;
    if (state.cat2 >= 0 && nmCat2[nmI] !== state.cat2) continue;

    // когортные агрегаты — без окна событий (когорта отвечает сама за себя)
    const c = coh[F.cohort[i]] || (coh[F.cohort[i]] = {cnt: 0, buy: 0, can: 0, ret: 0, fly: 0});
    c.cnt += F.cnt[i];

    const e = F.event[i];
    if (e < state.from || e > state.to) continue;

    const cls = CLS[i], cnt = F.cnt[i], kop = F.kop[i];
    K.pc += cnt; K.rubOrder += kop;
    const eb = ev[e];
    eb.rubOrder += kop; if (cls === 0) eb.pcFly += cnt;

    if (cls === 1) {
      K.pcBuy += cnt; K.rubBuy += kop; c.buy += cnt;
      eb.pcBuy += cnt; eb.rubBuy += kop;
      const off = Math.round((evT[e] - sparkEnd) / DAY);
      if (off >= -13 && off <= 0) {
        let sp = nmSpark.get(nmI); if (!sp) nmSpark.set(nmI, sp = new Float64Array(14));
        sp[off + 13] += kop / 100;
      }
    } else if (cls === 2) {
      K.pcCan += cnt; K.rubLost += kop; c.can += cnt;
      eb.pcCan += cnt; eb.rubLost += kop;
      const ct = DIM.ctype[F.ctype[i]];
      if (ct in reasons) reasons[ct] += kop; else reasons.unknown += kop;
    } else if (cls === 3) {
      K.pcRet += cnt; K.rubLost += kop; c.ret += cnt;
      eb.pcRet += cnt; eb.rubLost += kop;
      reasons.returns += kop;
    } else {
      K.pcFly += cnt;
    }

    let cityB = city.get(F.city[i]); if (!cityB) city.set(F.city[i], cityB = {rubBuy: 0, rubLost: 0, cnt: 0, buy: 0, fin: 0});
    let c1B = cat1.get(nmCat1[nmI]); if (!c1B) cat1.set(nmCat1[nmI], c1B = {rubBuy: 0, rubLost: 0, cnt: 0});
    const c2k = nmCat2[nmI];                       // -1 = без детализации уровня 2
    let c2B = cat2.get(c2k); if (!c2B) cat2.set(c2k, c2B = {rubBuy: 0, rubLost: 0, cnt: 0});
    let nmB = nmAgg.get(nmI); if (!nmB) nmAgg.set(nmI, nmB = {cnt: 0, buy: 0, can: 0, ret: 0, rubBuy: 0, rubLost: 0});

    cityB.cnt += cnt; cityB.rubBuy += cls === 1 ? kop : 0; cityB.rubLost += cls >= 2 ? kop : 0;
    if (cls === 1) { cityB.buy += cnt; cityB.fin += cnt; }
    if (cls >= 2) cityB.fin += cnt;
    c1B.cnt += cnt; c1B.rubBuy += cls === 1 ? kop : 0; c1B.rubLost += cls >= 2 ? kop : 0;
    c2B.cnt += cnt; c2B.rubBuy += cls === 1 ? kop : 0; c2B.rubLost += cls >= 2 ? kop : 0;
    nmB.cnt += cnt; nmB.rubBuy += cls === 1 ? kop : 0; nmB.rubLost += cls >= 2 ? kop : 0;
    if (cls === 1) nmB.buy += cnt; else if (cls === 2) nmB.can += cnt; else if (cls === 3) nmB.ret += cnt;
  }
  return {K, ev, coh, city, cat1, cat2, nmAgg, reasons, nmSpark};
}

/* ── echarts-база ── */
const FONT = '-apple-system,BlinkMacSystemFont,"SF Pro Text","Segoe UI",Roboto,Arial,sans-serif';
const C = {violet: '#7758B3', violet2: '#5B4887', green: '#018849', red: '#E41A2A',
  orange: '#FF8000', grey: '#ADADAD', line: '#E7E7E7', ink: '#2B2536', muted: '#757575'};
const baseTooltip = {
  backgroundColor: '#fff', borderColor: C.line, borderWidth: 1, padding: [8, 12],
  textStyle: {color: C.ink, fontSize: 12, fontFamily: FONT},
  extraCssText: 'box-shadow:0 4px 16px rgba(43,37,54,.12);border-radius:10px;',
};
const axisStyle = {
  axisLine: {lineStyle: {color: C.line}}, axisTick: {show: false},
  axisLabel: {color: C.muted, fontSize: 11, fontFamily: FONT},
  splitLine: {lineStyle: {color: '#F1F1F4'}},
};

const money = $('#chart-money'), funnel = $('#chart-funnel'), reasonsEl = $('#chart-reasons'),
  cohortEl = $('#chart-cohort'), geoEl = $('#chart-geo'), catEl = $('#chart-cat');
const ch = {
  money: echarts.init(money), funnel: echarts.init(funnel), reasons: echarts.init(reasonsEl),
  cohort: echarts.init(cohortEl), geo: echarts.init(geoEl), cat: echarts.init(catEl),
};
window.addEventListener('resize', () => Object.values(ch).forEach(c => c.resize()));

/* ── один проход → все виджеты ── */
function render() {
  const A = compute();

  /* KPI */
  const kp = A.K;
  const buyPct = pct(kp.pcBuy, kp.pcBuy + kp.pcCan + kp.pcRet);
  const lostPct = pct(kp.rubLost / 100, kp.rubOrder / 100);
  const avg = kp.pcBuy > 0 ? kp.rubBuy / 100 / kp.pcBuy : 0;
  $('#kpis').innerHTML = [
    kpi('hero', 'Выручка', fmtShort(kp.rubBuy / 100) + ' \u20BD', fmtRub(kp.rubBuy / 100) + ' · выкупы'),
    kpi('bad', 'Упущено', fmtShort(kp.rubLost / 100) + ' \u20BD', fmtPct(lostPct) + ' от заказанного'),
    kpi('good', 'Выкуп', fmtPct(buyPct), 'среди завершённых: ' + fmtInt(kp.pcBuy + kp.pcCan + kp.pcRet) + ' шт'),
    kpi('', 'Заказано', fmtInt(kp.pc), fmtShort(kp.rubOrder / 100) + ' \u20BD'),
    kpi('', 'Средний чек', fmtRub(avg), 'выручка / выкупы'),
    kpi('', 'Цикл до выкупа', META.lifecycle_median_days >= 0 ? META.lifecycle_median_days.toLocaleString('ru-RU', {maximumFractionDigits: 1}) + ' сут' : '—',
      'медиана, весь период'),
  ].join('');

  renderMoney(A);
  renderFunnel(A);
  renderReasons(A);
  renderCohort(A);
  renderGeo(A);
  renderCat(A);
  renderTable(A);
  renderChips();
}

function kpi(cls, l, v, s) {
  return `<div class="kpi ${cls}"><div class="l">${l}</div><div class="v" title="${s}">${v}</div><div class="s">${s}</div></div>`;
}

/* ── деньги по дням/неделям ── */
/* Период задаёт только шапка (пресеты/чипы): у графика нет своего зума,
   мышь над ним ничего не масштабирует. */
function buckets() {
  if (state.gran === 'day') {
    const idx = []; for (let i = state.from; i <= state.to; i++) idx.push(i);
    return {labels: idx.map(i => dd(DIM.event[i])), idx};
  }
  const wIdx = [], wLbl = [], wPos = new Map();
  for (let i = state.from; i <= state.to; i++) {
    const k = weekK[i];
    if (!wPos.has(k)) { wPos.set(k, wIdx.length); wIdx.push({k, from: i, to: i}); }
    wIdx[wPos.get(k)].to = i;
  }
  return {labels: wIdx.map(w => dd(new Date(w.k).toISOString().slice(0, 10))), idx: wIdx};
}

function renderMoney(A) {
  const B = buckets(), isRub = state.unit === 'rub';
  const unit = v => isRub ? v / 100 : v;
  const buy = [], lost = [], pcts = [];
  const fullBuy = A.ev.map(b => unit(b.rubBuy)), fullLost = A.ev.map(b => unit(b.rubLost));
  const ma7 = [];
  for (let i = 0; i < A.ev.length; i++) {
    if (i < 6) { ma7.push(null); continue; }
    let s = 0; for (let j = i - 6; j <= i; j++) s += fullBuy[j];
    ma7.push(Math.round(s / 7));
  }
  B.idx.forEach(slot => {
    let buyV = 0, lostV = 0, pb = 0, pcn = 0, pr = 0;
    const from = typeof slot === 'number' ? slot : slot.from, to = typeof slot === 'number' ? slot : slot.to;
    for (let i = from; i <= to; i++) {
      buyV += fullBuy[i]; lostV += fullLost[i];
      pb += A.ev[i].pcBuy; pcn += A.ev[i].pcCan; pr += A.ev[i].pcRet;
    }
    buy.push(Math.round(buyV)); lost.push(Math.round(lostV));
    const pp = pct(pb, pb + pcn + pr);
    pcts.push(pp < 0 ? null : +pp.toFixed(1));
  });
  const ma = state.gran === 'day'
    ? B.idx.map(i => ma7[i])
    : B.idx.map(w => ma7[w.to]);
  ch.money.setOption({
    animationDuration: 300,
    grid: {left: 66, right: 52, top: 28, bottom: 28},
    tooltip: {...baseTooltip, trigger: 'axis', axisPointer: {type: 'shadow'},
      formatter: ps => {
        const i = ps[0].dataIndex;
        const b = B.idx[i];
        let from = typeof b === 'number' ? b : b.from, to = typeof b === 'number' ? b : b.to;
        let rubB = 0, rubL = 0, pb = 0, pcn = 0, pr = 0;
        for (let j = from; j <= to; j++) { rubB += A.ev[j].rubBuy; rubL += A.ev[j].rubLost; pb += A.ev[j].pcBuy; pcn += A.ev[j].pcCan; pr += A.ev[j].pcRet; }
        return `<b>${B.labels[i]}${state.gran === 'week' ? ' – ' + dd(DIM.event[to]) : ''}</b><br>` +
          `Выручка: <b>${fmtRub(rubB / 100)}</b><br>Упущено: <b style="color:${C.red}">${fmtRub(rubL / 100)}</b><br>` +
          `Выкуп: ${fmtPct(pct(pb, pb + pcn + pr))} · выкупов ${fmtInt(pb)} шт`;
      }},
    legend: {top: 0, right: 0, itemWidth: 10, itemHeight: 10, textStyle: {color: C.muted, fontSize: 11, fontFamily: FONT}},
    xAxis: {type: 'category', data: B.labels, ...axisStyle, splitLine: {show: false}, boundaryGap: true},
    yAxis: [
      {type: 'value', ...axisStyle, axisLabel: {...axisStyle.axisLabel, formatter: fmtShort}},
      {type: 'value', min: 0, max: 100, ...axisStyle, splitLine: {show: false},
        axisLabel: {...axisStyle.axisLabel, formatter: '{value}%'}},
    ],
    series: [
      {name: isRub ? 'Выручка, \u20BD' : 'Выкупы, шт', type: 'bar', stack: 'm', data: buy,
        itemStyle: {color: C.green, borderRadius: [4, 4, 0, 0]}, barMaxWidth: 34},
      {name: isRub ? 'Упущено, \u20BD' : 'Отмены+возвраты, шт', type: 'bar', stack: 'm', data: lost,
        itemStyle: {color: C.red, borderRadius: [4, 4, 0, 0]}, barMaxWidth: 34},
      {name: '% выкупа', type: 'line', yAxisIndex: 1, data: pcts, symbol: 'circle', symbolSize: 5,
        lineStyle: {color: C.violet, width: 2.5}, itemStyle: {color: C.violet}},
      ...(state.gran === 'day' ? [{name: 'MA-7 выручки', type: 'line', data: ma, symbol: 'none',
        lineStyle: {color: C.green, width: 1.5, opacity: .55, type: 'dashed'}}] : []),
    ],
  });
}

/* ── воронка периода ── */
function renderFunnel(A) {
  const K = A.K, rub = state.unit === 'rub';
  const items = rub ? [
    ['Заказано', Math.round(K.rubOrder / 100), C.violet],
    ['Выкуп', Math.round(K.rubBuy / 100), C.green],
    ['Отмены', Math.round((K.rubLost - A.reasons.returns) / 100), C.red],
    ['Возвраты', Math.round(A.reasons.returns / 100), C.orange],
    ['В пути', Math.round((K.rubOrder - K.rubBuy - K.rubLost) / 100), C.grey],
  ] : [
    ['Заказано', K.pc, C.violet],
    ['Выкуп', K.pcBuy, C.green],
    ['Отмены', K.pcCan, C.red],
    ['Возвраты', K.pcRet, C.orange],
    ['В пути', K.pcFly, C.grey],
  ];
  ch.funnel.setOption({
    animationDuration: 300,
    grid: {left: 84, right: 24, top: 8, bottom: 8, containLabel: true},
    tooltip: {...baseTooltip, formatter: p => `<b>${p.name}</b><br>${rub ? fmtRub(p.value) : fmtInt(p.value) + ' шт'}`},
    xAxis: {type: 'value', show: false},
    yAxis: {type: 'category', data: items.map(x => x[0]).reverse(), ...axisStyle, splitLine: {show: false},
      axisLabel: {...axisStyle.axisLabel, fontSize: 12, color: C.ink}},
    series: [{
      type: 'bar', data: items.map(x => ({value: x[1], itemStyle: {color: x[2], borderRadius: [0, 5, 5, 0]}})).reverse(),
      barMaxWidth: 26, label: {show: true, position: 'right', color: C.ink, fontWeight: 600, fontFamily: FONT,
        formatter: p => rub ? fmtShort(p.value) : fmtInt(p.value)},
    }],
  });
}

/* ── причины упущенного ── */
const REASON_RU = {app: 'Отмена в приложении', receipt: 'Не забрали из ПВЗ', expire: 'Истёк срок хранения',
  other: 'Другое', unknown: 'Не указана', returns: 'Возвраты'};
const REASON_C = {app: C.red, receipt: '#C2185B', expire: C.orange, other: C.violet2, unknown: C.grey, returns: '#7986CB'};
function renderReasons(A) {
  const r = A.reasons;
  const data = Object.entries(r).filter(([, v]) => v > 0)
    .map(([k, v]) => ({name: REASON_RU[k], value: Math.round(v / 100), itemStyle: {color: REASON_C[k]}}));
  const total = data.reduce((s, d) => s + d.value, 0);
  ch.reasons.setOption({
    animationDuration: 300,
    tooltip: {...baseTooltip, formatter: p => `<b>${p.name}</b><br>${fmtRub(p.value)} · ${fmtPct(pct(p.value, total))}`},
    series: [{
      type: 'pie', radius: ['52%', '76%'], center: ['50%', '52%'],
      itemStyle: {borderColor: '#fff', borderWidth: 2, borderRadius: 5},
      label: {color: C.muted, fontSize: 11, fontFamily: FONT, formatter: p => p.percent >= 6 ? p.name : ''},
      labelLine: {length: 8, length2: 6, lineStyle: {color: C.line}},
      data,
    }],
  });
}

/* ── когорты ── */
function renderCohort(A) {
  const rows = [];
  const matureD = META.mature_after_days || 0;
  DIM.cohort.forEach((d, i) => {
    const c = A.coh[i];
    if (!c || c.cnt < 5) return;                      // шум малых когорт
    const p = pct(c.buy, c.buy + c.can + c.ret);
    if (p < 0) return;
    rows.push({date: d, p: +p.toFixed(1), cnt: c.cnt, c,
      mature: matureD === 0 ? true : (lastT - d2t(d)) / DAY >= matureD});
  });
  ch.cohort.setOption({
    animationDuration: 300,
    grid: {left: 44, right: 52, top: 28, bottom: 24},
    tooltip: {...baseTooltip, trigger: 'axis',
      formatter: ps => {
        const r = rows[ps[0].dataIndex];
        return `<b>${dmy(r.date)}</b> · ${r.mature ? 'зрелая' : '<b style="color:#9A6700">не созрела</b>'}<br>` +
          `Заказано: ${fmtInt(r.cnt)}<br>Выкуп: <b>${fmtPct(r.p)}</b> · ${fmtInt(r.c.buy)} шт<br>` +
          `Отмены: ${fmtInt(r.c.can)} · Возвраты: ${fmtInt(r.c.ret)}`;
      }},
    legend: {top: 0, right: 0, itemWidth: 10, itemHeight: 10, textStyle: {color: C.muted, fontSize: 11, fontFamily: FONT}},
    xAxis: {type: 'category', data: rows.map(r => dd(r.date)), ...axisStyle, splitLine: {show: false}},
    yAxis: [
      {type: 'value', min: 0, max: 100, ...axisStyle, axisLabel: {...axisStyle.axisLabel, formatter: '{value}%'}},
      {type: 'value', ...axisStyle, splitLine: {show: false}, axisLabel: {...axisStyle.axisLabel, formatter: fmtShort}},
    ],
    series: [
      {name: '% финального выкупа', type: 'bar', data: rows.map(r => ({
        value: r.p, itemStyle: {color: C.violet, opacity: r.mature ? 1 : .32, borderRadius: [4, 4, 0, 0]}})),
        barMaxWidth: 26},
      {name: 'Заказано, шт', type: 'line', yAxisIndex: 1, data: rows.map(r => r.cnt), symbol: 'none',
        lineStyle: {color: C.violet2, width: 1.5, opacity: .6}},
    ],
  });
}

/* ── география ── */
function renderGeo(A) {
  const rows = [...A.city.entries()].map(([ci, v]) => ({name: DIM.city[ci], ...v, p: pct(v.buy, v.fin)}))
    .sort((a, b) => (b.rubBuy + b.rubLost) - (a.rubBuy + a.rubLost)).slice(0, 15);
  ch.geo.setOption({
    animationDuration: 300,
    grid: {left: 8, right: 56, top: 8, bottom: 8, containLabel: true},
    tooltip: {...baseTooltip, formatter: p => {
      const r = rows[p.dataIndex];
      return `<b>${r.name}</b><br>Выручка: <b>${fmtRub(r.rubBuy / 100)}</b><br>` +
        `Упущено: <b style="color:${C.red}">${fmtRub(r.rubLost / 100)}</b><br>` +
        `Выкуп: ${fmtPct(r.p)} · заказов ${fmtInt(r.cnt)}`;
    }},
    xAxis: {type: 'value', show: false},
    yAxis: {type: 'category', inverse: true, data: rows.map(r => r.name), ...axisStyle, splitLine: {show: false},
      axisLabel: {...axisStyle.axisLabel, fontSize: 11.5, color: C.ink}},
    series: [
      {type: 'bar', stack: 'g', name: 'Выручка', data: rows.map(r => ({value: Math.round(r.rubBuy / 100),
        itemStyle: {color: r.name === DIM.city[state.city] ? C.violet2 : C.green, borderRadius: [0, 0, 0, 0]}})),
        barMaxWidth: 15},
      {type: 'bar', stack: 'g', name: 'Упущено', data: rows.map(r => ({value: Math.round(r.rubLost / 100),
        itemStyle: {color: C.red, borderRadius: [0, 4, 4, 0]}})), barMaxWidth: 15},
    ],
  });
  ch.geo.off('click');
  ch.geo.on('click', p => {
    const ci = DIM.city.indexOf(p.name);
    if (ci >= 0) { state.city = state.city === ci ? -1 : ci; render(); }
  });
}

/* ── категории 1С ── */
function renderCat(A) {
  const drilled = state.cat1 >= 0;
  let rows;
  if (!drilled) {
    rows = [...A.cat1.entries()].map(([i, v]) => ({name: cat1List[i], ...v}))
      .sort((a, b) => (b.rubBuy + b.rubLost) - (a.rubBuy + a.rubLost)).slice(0, 12);
  } else {
    rows = [...A.cat2.entries()].map(([i, v]) => ({name: cat2List[i] || 'Без детализации', ...v}))
      .sort((a, b) => (b.rubBuy + b.rubLost) - (a.rubBuy + a.rubLost)).slice(0, 12);
  }
  $('#cat-title').firstChild.textContent = drilled
    ? 'Категории 1С — ' + cat1List[state.cat1] + ': детализация '
    : 'Категории 1С ';
  ch.cat.setOption({
    animationDuration: 300,
    grid: {left: 8, right: 56, top: 8, bottom: 8, containLabel: true},
    tooltip: {...baseTooltip, formatter: p => {
      const r = rows[p.dataIndex];
      return `<b>${r.name}</b><br>Выручка: <b>${fmtRub(r.rubBuy / 100)}</b><br>` +
        `Упущено: <b style="color:${C.red}">${fmtRub(r.rubLost / 100)}</b><br>Заказов: ${fmtInt(r.cnt)}`;
    }},
    xAxis: {type: 'value', show: false},
    yAxis: {type: 'category', inverse: true, data: rows.map(r => r.name), ...axisStyle, splitLine: {show: false},
      axisLabel: {...axisStyle.axisLabel, fontSize: 11.5, color: C.ink,
        formatter: v => v.length > 26 ? v.slice(0, 25) + '…' : v}},
    series: [
      {type: 'bar', stack: 'c', data: rows.map(r => ({value: Math.round(r.rubBuy / 100),
        itemStyle: {color: C.green}})), barMaxWidth: 15},
      {type: 'bar', stack: 'c', data: rows.map(r => ({value: Math.round(r.rubLost / 100),
        itemStyle: {color: C.red, borderRadius: [0, 4, 4, 0]}})), barMaxWidth: 15},
    ],
  });
  ch.cat.off('click');
  ch.cat.on('click', p => {
    const i = rows[p.dataIndex];
    if (!drilled) {
      state.cat1 = cat1Idx.get(i.name) ?? -1;
      if (state.cat1 >= 0) state.cat2 = -1;
    } else {
      const c2 = cat2List.indexOf(i.name);
      state.cat2 = (c2 >= 0 && state.cat2 !== c2) ? c2 : -1;
    }
    render();
  });
}

/* ── топ номенклатур ── */
const COLS = [
  {k: 'vc', l: 'Артикул', tl: 1},
  {k: 'name', l: 'Название', tl: 1},
  {k: 'cat', l: 'Категория', tl: 1},
  {k: 'orders', l: 'Заказы'},
  {k: 'pct', l: '% выкупа'},
  {k: 'rubBuy', l: 'Выручка'},
  {k: 'rubLost', l: 'Упущено'},
  {k: 'spark', l: 'Выручка 14д', tl: 1},
];
function renderTable(A) {
  const head = $('#topnm thead'), body = $('#topnm tbody');
  head.innerHTML = '<tr>' + COLS.map(c => {
    const cls = [c.tl ? 'tl' : '', state.sort === c.k ? 'sorted' : ''].filter(Boolean).join(' ');
    const arrow = state.sort === c.k ? (state.sortDir < 0 ? ' ↓' : ' ↑') : '';
    return `<th ${cls ? `class="${cls}"` : ''} data-k="${c.k}">${c.l}${arrow}</th>`;
  }).join('') + '</tr>';
  head.querySelectorAll('th').forEach(th => th.onclick = () => {
    const k = th.dataset.k;
    if (k === 'spark' || k === 'vc' || k === 'name' || k === 'cat') return;
    if (state.sort === k) state.sortDir *= -1; else { state.sort = k; state.sortDir = -1; }
    renderTable(A);
  });

  const rows = [...A.nmAgg.entries()].map(([ni, v]) => {
    const nm = DIM.nm[ni];
    return {ni, vc: nm[1], name: nm[2] || nm[3] || nm[0], cat: nmCat2[ni] >= 0 ? cat2List[nmCat2[ni]] : (cat1List[nmCat1[ni]] || ''),
      orders: v.cnt, pct: pct(v.buy, v.buy + v.can + v.ret), rubBuy: v.rubBuy / 100, rubLost: v.rubLost / 100,
      spark: A.nmSpark.get(ni)};
  }).sort((a, b) => (b[state.sort] - a[state.sort]) * (state.sortDir < 0 ? 1 : -1) ||
    String(a[state.sort]).localeCompare(String(b[state.sort])) * (state.sortDir < 0 ? 1 : -1)).slice(0, 20);

  body.innerHTML = rows.map(r => `<tr data-nm="${r.ni}" ${state.nm === r.ni ? 'class="active"' : ''}>` +
    `<td class="tl"><b>${r.vc}</b></td><td class="tl">${esc(r.name)}</td><td class="tl">${esc(r.cat)}</td>` +
    `<td>${fmtInt(r.orders)}</td>` +
    `<td class="${r.pct >= 0 ? (r.pct >= 47 ? 'pct-good' : 'pct-bad') : ''}">${fmtPct(r.pct)}</td>` +
    `<td><b>${fmtInt(r.rubBuy)}</b></td><td style="color:${C.red}">${fmtInt(r.rubLost)}</td>` +
    `<td class="tl">${sparkSvg(r.spark)}</td></tr>`).join('');
  body.querySelectorAll('tr').forEach(tr => tr.onclick = () => {
    const ni = +tr.dataset.nm;
    state.nm = state.nm === ni ? -1 : ni;
    render();
  });
}
function esc(s) { return String(s).replace(/[&<>"]/g, c => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;'}[c])); }
function sparkSvg(arr) {
  if (!arr) return '';
  const v = Array.from(arr);
  if (!v.some(x => x > 0)) return '';
  const w = 88, h = 24, max = Math.max(...v);
  const pts = v.map((x, i) => `${(i / 13 * w).toFixed(1)},${(h - 3 - x / max * (h - 6)).toFixed(1)}`).join(' ');
  const last = v[13] > 0 ? `<circle cx="${w}" cy="${(h - 3 - v[13] / max * (h - 6)).toFixed(1)}" r="2.4" fill="${C.violet}"/>` : '';
  return `<svg width="${w}" height="${h}" style="vertical-align:middle"><polyline points="${pts}" fill="none" stroke="${C.violet}" stroke-width="1.6" stroke-linejoin="round"/>${last}</svg>`;
}

/* ── чипы фильтров ── */
function renderChips() {
  const chips = [];
  const range = `Период: ${dd(DIM.event[state.from])} – ${dd(DIM.event[state.to])}`;
  chips.push(`<span class="chip muted">${range}<span class="x" data-a="period">×</span></span>`);
  if (state.city >= 0) chips.push(chip('Город', DIM.city[state.city], 'city'));
  if (state.cat1 >= 0) chips.push(chip('Категория', cat1List[state.cat1] + (state.cat2 >= 0 ? ' ▸ ' + cat2List[state.cat2] : ''), 'cat'));
  if (state.nm >= 0) chips.push(chip('Артикул', DIM.nm[state.nm][1] || DIM.nm[state.nm][0], 'nm'));
  $('#chips').innerHTML = chips.join('');
  $('#chips').querySelectorAll('.x').forEach(x => x.onclick = () => {
    const a = x.dataset.a;
    if (a === 'period') setPreset(0);
    else if (a === 'city') state.city = -1;
    else if (a === 'cat') { state.cat1 = -1; state.cat2 = -1; }
    else if (a === 'nm') state.nm = -1;
    render();
  });
}
function chip(l, v, a) { return `<span class="chip">${l}: <b>${esc(v)}</b><span class="x" data-a="${a}">×</span></span>`; }

/* ── управление ── */
function setPreset(days) {
  if (days <= 0 || !DIM.event.length) { state.from = 0; state.to = DIM.event.length - 1; return; }
  const cutoff = evT[state.to] - (days - 1) * DAY;
  let from = 0;
  for (let i = DIM.event.length - 1; i >= 0; i--) { if (evT[i] >= cutoff) from = i; else break; }
  state.from = from;
}
$('#presets').querySelectorAll('button').forEach(b => b.onclick = () => {
  $('#presets').querySelectorAll('button').forEach(x => x.classList.remove('active'));
  b.classList.add('active');
  setPreset(+b.dataset.days);
  render();
});
$('#seg-gran').querySelectorAll('button').forEach(b => b.onclick = () => {
  $('#seg-gran').querySelectorAll('button').forEach(x => x.classList.remove('active'));
  b.classList.add('active');
  state.gran = b.dataset.v;
  render();
});
$('#seg-unit').querySelectorAll('button').forEach(b => b.onclick = () => {
  $('#seg-unit').querySelectorAll('button').forEach(x => x.classList.remove('active'));
  b.classList.add('active');
  state.unit = b.dataset.v;
  render();
});
$('#reset').onclick = () => {
  Object.assign(state, {from: 0, to: DIM.event.length - 1, city: -1, cat1: -1, cat2: -1, nm: -1});
  $('#presets').querySelectorAll('button').forEach(x => x.classList.toggle('active', x.dataset.days === '0'));
  render();
};

/* ── глоссарий: выезжающая панель ── */
const glossaryEl = $('#glossary'), glossaryBack = $('#glossary-back');
function glossaryToggle(open) {
  const on = open ?? !glossaryEl.classList.contains('open');
  glossaryEl.classList.toggle('open', on);
  glossaryBack.classList.toggle('open', on);
}
$('#glossary-btn').onclick = () => glossaryToggle();
$('#glossary-close').onclick = () => glossaryToggle(false);
glossaryBack.onclick = () => glossaryToggle(false);
document.addEventListener('keydown', e => { if (e.key === 'Escape') glossaryToggle(false); });

/* ── шапка/футер ── */
function initChrome() {
  const badge = $('#db-badge');
  if (META.db === 'wb_data_prod') { badge.textContent = META.db; badge.title = 'Прод-база'; }
  else { badge.textContent = 'ТЕСТ · ' + META.db; badge.classList.add('test'); badge.title = 'Тестовая база — не для решений'; }
  $('#subtitle').textContent = `данные ${META.feed_from ? dmy(META.feed_from) + ' – ' + dmy(META.feed_to) : '—'} · ` +
    `${fmtInt(META.total_orders)} заказов · сгенерировано ${META.generated_at} МСК` +
    (META.all_models ? ' · все модели (incl. FBW)' : ' · склад продавца (FBS/DBS)');
  $('#method').innerHTML = '<b>Методика.</b> Дни — календарные МСК. Выручка = цена продавца по выкупам ' +
    '(без комиссий и логистики WB); упущено = отмены + возвраты. Лента несёт текущий статус заказа, ' +
    'день события = дата смены статуса. Когорта зрелая после ' + (META.mature_after_days || '?') +
    ' сут — у незрелых «% выкупа» завышен. Категории: ' +
    (META.onec_categories
      ? 'иерархия 1С (покрытие ' + fmtPct(META.onec_coverage_pct) + ' номенклатур), «WB · …» — предметы WB для немапленных'
      : 'предметы WB (в базе нет 1С-словаря)') +
    '. Источник: download-wb-fbs-orders-v2 → order_feed. Средний чек = выручка / число выкупов. ' +
    'Термины и определения — кнопка «Глоссарий» в шапке.';
}

/* ── старт ── */
if (!N) {
  document.querySelector('main').innerHTML = '<div class="empty">Нет данных: прогоните download-wb-fbs-orders-v2 (фаза order_feed).</div>';
} else {
  initChrome();
  render();
}
