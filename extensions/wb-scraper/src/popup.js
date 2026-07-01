// popup.js — UI logic. Talks to the SW only via chrome.runtime.sendMessage.

const $ = (id) => document.getElementById(id);
let currentMode = 'recon';

function send(msg) {
  return chrome.runtime.sendMessage(msg).catch((e) => console.warn('[popup] send failed', e));
}

function setMode(m) {
  currentMode = m;
  $('modeRecon').classList.toggle('active', m === 'recon');
  $('modeCollect').classList.toggle('active', m === 'collect');
  $('reconPanel').classList.toggle('hidden', m !== 'recon');
  $('collectPanel').classList.toggle('hidden', m !== 'collect');
}

// Turn a user-entered target line into a WB URL.
// number  -> product card ; text -> search ; http(s) -> as-is
function targetToUrl(line) {
  const t = (line || '').trim();
  if (!t) return null;
  if (/^https?:\/\//i.test(t)) return { kind: 'url', url: t };
  if (/^\d{5,12}$/.test(t)) return { kind: 'card', url: `https://www.wildberries.ru/catalog/${t}/detail.aspx` };
  return { kind: 'search', url: `https://www.wildberries.ru/catalog/0/search.aspx?search=${encodeURIComponent(t)}` };
}

function refreshStatus() {
  chrome.runtime.sendMessage({ type: 'GET_STATE' }, (st) => {
    if (chrome.runtime.lastError || !st) return;
    // Status follows the LOCAL mode (user's selection), not the SW mode —
    // otherwise the poll yanks the UI back to the old mode mid-use.
    if (currentMode === 'collect') {
      const running = st.collectRunning ? ' · <span class="ok">сбор идёт…</span>' : '';
      $('status').innerHTML = `Collect: ${st.collectCount} в буфере${running}`;
    } else {
      $('status').innerHTML = `Recon: ${st.reconCount} в буфере`;
    }
  });
}

// ---------- wiring ----------
$('modeRecon').addEventListener('click', () => { setMode('recon'); send({ type: 'SET_MODE', mode: 'recon' }); });
$('modeCollect').addEventListener('click', () => { setMode('collect'); send({ type: 'SET_MODE', mode: 'collect' }); });

$('reconOpen').addEventListener('click', () => {
  const url = $('reconUrl').value.trim();
  if (!url) return;
  send({ type: 'RECON_OPEN', url });
});

$('collectStart').addEventListener('click', () => {
  const targets = $('collectTargets').value
    .split('\n')
    .map(targetToUrl)
    .filter(Boolean);
  if (!targets.length) return;
  send({ type: 'COLLECT_START', targets });
});

$('collectStop').addEventListener('click', () => send({ type: 'COLLECT_STOP' }));

$('export').addEventListener('click', () => {
  send({ type: 'EXPORT' });
});

$('clear').addEventListener('click', () => {
  if (confirm('Очистить буфер(ы)? (файлы уже экспортированы не затронуты)')) send({ type: 'CLEAR' });
});

// ---------- persist user inputs across popup close/reopen ----------
// Popup is destroyed on close, so textarea/input values vanish. Restore from storage on open,
// autosave (debounced ~400ms) on input. Kept in storage.local (short text), not IndexedDB.
chrome.storage.local.get(['collectTargets', 'reconUrl']).then((s) => {
  if (s.collectTargets) $('collectTargets').value = s.collectTargets;
  if (s.reconUrl) $('reconUrl').value = s.reconUrl;
}).catch(() => {});

let persistTimer;
function persistOnInput(key, el) {
  el.addEventListener('input', () => {
    clearTimeout(persistTimer);
    persistTimer = setTimeout(() => chrome.storage.local.set({ [key]: el.value }).catch(() => {}), 400);
  });
}
persistOnInput('collectTargets', $('collectTargets'));
persistOnInput('reconUrl', $('reconUrl'));

// sync mode once on open (from SW), then poll only the counts — never yank mode back
chrome.runtime.sendMessage({ type: 'GET_STATE' }, (st) => { if (st) setMode(st.mode || 'recon'); });
refreshStatus();
const poll = setInterval(refreshStatus, 1500);
window.addEventListener('unload', () => clearInterval(poll));
