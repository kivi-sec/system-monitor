'use strict';

/* ---------- formatting helpers ---------- */

function formatBytes(bytes) {
  if (bytes === undefined || bytes === null || isNaN(bytes)) return '—';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / Math.pow(1024, i);
  return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function formatSpeed(bytesPerSec) {
  return `${formatBytes(bytesPerSec)}/s`;
}

function formatPercent(v) {
  if (v === undefined || v === null || isNaN(v)) return '—';
  return `${v.toFixed(1)}%`;
}

function formatUptime(seconds) {
  if (!seconds || seconds <= 0) return '—';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const parts = [];
  if (d) parts.push(`${d}d`);
  if (h || d) parts.push(`${h}h`);
  parts.push(`${m}m`);
  return parts.join(' ');
}

function escapeHtml(str) {
  const div = document.createElement('div');
  div.textContent = str ?? '';
  return div.innerHTML;
}

/* ---------- rolling history buffers (~60s @ 1s/tick) ---------- */

const HISTORY_LEN = 60;

function makeHistory() {
  return { labels: Array(HISTORY_LEN).fill(''), values: Array(HISTORY_LEN).fill(null) };
}

function pushHistory(hist, value) {
  hist.labels.push('');
  hist.labels.shift();
  hist.values.push(value);
  hist.values.shift();
}

const cpuHistory = makeHistory();
const memHistory = makeHistory();
const netDownHistory = makeHistory();
const netUpHistory = makeHistory();

/* ---------- chart setup ---------- */

const sparkOptions = (color) => ({
  responsive: true,
  maintainAspectRatio: false,
  animation: { duration: 250 },
  plugins: { legend: { display: false }, tooltip: { enabled: false } },
  elements: { point: { radius: 0 }, line: { borderWidth: 2, tension: 0.35 } },
  scales: {
    x: { display: false },
    y: { display: false, min: 0, max: 100 },
  },
});

const cpuChart = new Chart(document.getElementById('chart-cpu'), {
  type: 'line',
  data: { labels: cpuHistory.labels, datasets: [{ data: cpuHistory.values, borderColor: '#22d3ee', backgroundColor: 'rgba(34,211,238,0.12)', fill: true }] },
  options: sparkOptions('#22d3ee'),
});

const memChart = new Chart(document.getElementById('chart-mem'), {
  type: 'line',
  data: { labels: memHistory.labels, datasets: [{ data: memHistory.values, borderColor: '#818cf8', backgroundColor: 'rgba(129,140,248,0.12)', fill: true }] },
  options: sparkOptions('#818cf8'),
});

const netChart = new Chart(document.getElementById('chart-net'), {
  type: 'line',
  data: {
    labels: netDownHistory.labels,
    datasets: [
      { label: 'Download', data: netDownHistory.values, borderColor: '#22d3ee', backgroundColor: 'rgba(34,211,238,0.08)', fill: true, borderWidth: 2, tension: 0.3, pointRadius: 0 },
      { label: 'Upload', data: netUpHistory.values, borderColor: '#818cf8', backgroundColor: 'rgba(129,140,248,0.08)', fill: true, borderWidth: 2, tension: 0.3, pointRadius: 0 },
    ],
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    animation: { duration: 250 },
    plugins: { legend: { display: false }, tooltip: { enabled: false } },
    scales: {
      x: { display: false },
      y: { display: true, beginAtZero: true, grid: { color: '#1c202a' }, ticks: { color: '#64748b', font: { size: 10 }, callback: (v) => formatBytes(v) } },
    },
  },
});

/* ---------- toasts ---------- */

function toast(message, kind = 'info') {
  const container = document.getElementById('toast-container');
  const el = document.createElement('div');
  el.className = 'toast';
  const iconColor = kind === 'error' ? '#f87171' : kind === 'success' ? '#34d399' : '#22d3ee';
  el.innerHTML = `<span style="color:${iconColor}">●</span><span>${escapeHtml(message)}</span>`;
  container.appendChild(el);
  setTimeout(() => {
    el.classList.add('leaving');
    setTimeout(() => el.remove(), 220);
  }, 3500);
}

/* ---------- connection status ---------- */

const wsDot = document.getElementById('ws-dot');
const wsLabel = document.getElementById('ws-label');

function setStatus(state) {
  wsDot.classList.remove('bg-slate-500', 'bg-good', 'bg-warn', 'bg-bad', 'pulse-good');
  if (state === 'connected') {
    wsDot.classList.add('bg-good', 'pulse-good');
    wsLabel.textContent = 'Live';
    wsLabel.classList.remove('text-slate-400');
    wsLabel.classList.add('text-good');
  } else if (state === 'connecting') {
    wsDot.classList.add('bg-warn');
    wsLabel.textContent = 'Connecting';
    wsLabel.classList.remove('text-good', 'text-bad');
    wsLabel.classList.add('text-slate-400');
  } else {
    wsDot.classList.add('bg-bad');
    wsLabel.textContent = 'Reconnecting…';
    wsLabel.classList.remove('text-good', 'text-slate-400');
    wsLabel.classList.add('text-bad');
  }
}

/* ---------- process table state ---------- */

let latestProcesses = [];
let procSearch = '';
let procSort = 'cpu';

const procSearchInput = document.getElementById('proc-search');
const procSortSelect = document.getElementById('proc-sort');
procSearchInput.addEventListener('input', (e) => { procSearch = e.target.value.trim().toLowerCase(); renderProcesses(); });
procSortSelect.addEventListener('change', (e) => { procSort = e.target.value; renderProcesses(); });

function sortProcesses(list) {
  const copy = [...list];
  switch (procSort) {
    case 'mem': return copy.sort((a, b) => b.memBytes - a.memBytes);
    case 'name': return copy.sort((a, b) => a.name.localeCompare(b.name));
    case 'pid': return copy.sort((a, b) => a.pid - b.pid);
    case 'cpu':
    default: return copy.sort((a, b) => b.cpuPercent - a.cpuPercent);
  }
}

function renderProcesses() {
  const tbody = document.getElementById('proc-tbody');
  let list = latestProcesses;

  if (procSearch) {
    list = list.filter((p) => p.name.toLowerCase().includes(procSearch) || String(p.pid).includes(procSearch));
  }
  list = sortProcesses(list).slice(0, 20);

  if (list.length === 0) {
    tbody.innerHTML = `<tr><td colspan="7" class="py-8 text-center text-slate-500">No matching processes</td></tr>`;
    document.getElementById('proc-count').textContent = '';
    return;
  }

  tbody.innerHTML = list.map((p) => {
    const hot = p.cpuPercent > 80 || p.memPercent > 80;
    const warm = !hot && (p.cpuPercent > 50 || p.memPercent > 50);
    const rowClass = hot ? 'proc-row-hot' : warm ? 'proc-row-warm' : '';
    const statusColor = p.status === 'running' ? 'text-good' : p.status === 'sleep' ? 'text-slate-500' : 'text-slate-400';
    return `
      <tr class="${rowClass}">
        <td class="py-2 px-2 font-mono text-slate-400">${p.pid}</td>
        <td class="py-2 px-2 text-slate-200 truncate max-w-[180px]" title="${escapeHtml(p.name)}">${escapeHtml(p.name)}</td>
        <td class="py-2 px-2 font-mono ${hot ? 'text-bad' : warm ? 'text-warn' : 'text-slate-300'}">${formatPercent(p.cpuPercent)}</td>
        <td class="py-2 px-2 font-mono text-slate-300">${formatBytes(p.memBytes)}</td>
        <td class="py-2 px-2 font-mono ${hot ? 'text-bad' : 'text-slate-300'}">${formatPercent(p.memPercent)}</td>
        <td class="py-2 px-2 ${statusColor}">${escapeHtml(p.status)}</td>
        <td class="py-2 px-2 text-right">
          <button class="terminate-btn px-2.5 py-1 rounded-md text-[11px] bg-panel2 border border-border text-slate-400 hover:text-bad hover:border-bad/40 transition" data-pid="${p.pid}" data-name="${escapeHtml(p.name)}">
            Terminate
          </button>
        </td>
      </tr>`;
  }).join('');

  document.getElementById('proc-count').textContent = `Showing ${list.length} of ${latestProcesses.length} processes`;
}

/* ---------- terminate flow ---------- */

const confirmOverlay = document.getElementById('confirm-overlay');
const confirmText = document.getElementById('confirm-text');
const confirmOk = document.getElementById('confirm-ok');
const confirmCancel = document.getElementById('confirm-cancel');
let pendingTerminate = null;

document.getElementById('proc-tbody').addEventListener('click', (e) => {
  const btn = e.target.closest('.terminate-btn');
  if (!btn) return;
  pendingTerminate = { pid: btn.dataset.pid, name: btn.dataset.name };
  confirmText.textContent = `This will send a termination signal to "${pendingTerminate.name}" (PID ${pendingTerminate.pid}). This cannot be undone.`;
  confirmOverlay.classList.remove('hidden');
  confirmOverlay.classList.add('flex');
});

confirmCancel.addEventListener('click', closeConfirm);
confirmOverlay.addEventListener('click', (e) => { if (e.target === confirmOverlay) closeConfirm(); });

function closeConfirm() {
  confirmOverlay.classList.add('hidden');
  confirmOverlay.classList.remove('flex');
  pendingTerminate = null;
}

confirmOk.addEventListener('click', async () => {
  if (!pendingTerminate) return;
  const { pid, name } = pendingTerminate;
  closeConfirm();
  try {
    const res = await fetch('/api/process/terminate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pid: Number(pid) }),
    });
    const data = await res.json();
    if (data.success) {
      toast(`Terminated "${name}" (PID ${pid})`, 'success');
    } else {
      toast(`Failed to terminate "${name}": ${data.message}`, 'error');
    }
  } catch (err) {
    toast(`Request failed: ${err.message}`, 'error');
  }
});

/* ---------- rendering a metrics snapshot ---------- */

function renderSnapshot(m) {
  // Header
  const sys = m.system || {};
  document.getElementById('host-line').textContent = sys.hostname ? `${sys.hostname}` : '—';
  document.getElementById('hdr-os').lastChild
    ? (document.getElementById('hdr-os').innerHTML = document.getElementById('hdr-os').innerHTML.replace(/(&mdash;|[^<]*)$/, sys.os || sys.platform || '—'))
    : null;
  document.getElementById('hdr-uptime').innerHTML = document.getElementById('hdr-uptime').innerHTML.replace(/(&mdash;|[^<]*)$/, formatUptime(sys.uptimeSeconds));

  // CPU
  const cpu = m.cpu || {};
  document.getElementById('cpu-value').textContent = formatPercent(cpu.usage);
  document.getElementById('cpu-cores').textContent = `${cpu.cores || 0} cores`;
  document.getElementById('cpu-load').textContent = cpu.load1 !== undefined ? `load ${cpu.load1.toFixed(2)}` : 'load —';
  pushHistory(cpuHistory, cpu.usage ?? null);
  cpuChart.update('none');

  // Memory
  const mem = m.memory || {};
  document.getElementById('mem-value').textContent = formatPercent(mem.usage);
  document.getElementById('mem-used').textContent = formatBytes(mem.used);
  document.getElementById('mem-total').textContent = `of ${formatBytes(mem.total)}`;
  pushHistory(memHistory, mem.usage ?? null);
  memChart.update('none');

  // Disk
  const disk = m.disk || {};
  document.getElementById('disk-value').textContent = disk.available ? formatPercent(disk.usage) : 'N/A';
  document.getElementById('disk-bar').style.width = `${disk.available ? disk.usage : 0}%`;
  document.getElementById('disk-used').textContent = disk.available ? formatBytes(disk.used) : '—';
  document.getElementById('disk-total').textContent = disk.available ? `of ${formatBytes(disk.total)}` : '';
  const ioEl = document.getElementById('disk-io');
  ioEl.textContent = disk.io ? `R ${formatSpeed(disk.io.readBytesPerSec)}  ·  W ${formatSpeed(disk.io.writeBytesPerSec)}` : '';

  // Temperature
  const tempList = document.getElementById('temp-list');
  const temps = m.temperatures || [];
  document.getElementById('temp-value').textContent = temps.length ? `${temps[0].temperature.toFixed(1)}°C` : 'N/A';
  if (temps.length === 0) {
    tempList.innerHTML = `<span class="text-slate-500">Temperature data unavailable</span>`;
  } else {
    tempList.innerHTML = temps.slice(0, 6).map((t) => {
      const hot = t.temperature >= 80;
      return `<div class="flex items-center justify-between"><span class="truncate max-w-[120px]">${escapeHtml(t.sensor)}</span><span class="font-mono ${hot ? 'text-bad' : 'text-slate-300'}">${t.temperature.toFixed(1)}°C</span></div>`;
    }).join('');
  }

  // Network
  const net = m.network || {};
  document.getElementById('net-down').textContent = formatSpeed(net.download);
  document.getElementById('net-up').textContent = formatSpeed(net.upload);
  document.getElementById('net-total-down').textContent = `Total received: ${formatBytes(net.totalReceived)}`;
  document.getElementById('net-total-up').textContent = `Total sent: ${formatBytes(net.totalTransmitted)}`;
  pushHistory(netDownHistory, net.download ?? 0);
  pushHistory(netUpHistory, net.upload ?? 0);
  netChart.data.datasets[0].data = netDownHistory.values;
  netChart.data.datasets[1].data = netUpHistory.values;
  netChart.update('none');

  // Processes
  latestProcesses = m.processes || [];
  renderProcesses();
}

/* ---------- websocket with auto-reconnect ---------- */

let reconnectDelay = 1000;
const MAX_RECONNECT_DELAY = 15000;
let socket = null;
let reconnectTimer = null;

function connect() {
  setStatus('connecting');
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  socket = new WebSocket(`${proto}://${location.host}/ws`);

  socket.onopen = () => {
    setStatus('connected');
    reconnectDelay = 1000;
  };

  socket.onmessage = (event) => {
    try {
      const data = JSON.parse(event.data);
      renderSnapshot(data);
    } catch (err) {
      console.error('Failed to parse metrics payload', err);
    }
  };

  socket.onclose = () => {
    setStatus('disconnected');
    scheduleReconnect();
  };

  socket.onerror = () => {
    socket.close();
  };
}

function scheduleReconnect() {
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(() => {
    reconnectDelay = Math.min(reconnectDelay * 1.6, MAX_RECONNECT_DELAY);
    connect();
  }, reconnectDelay);
}

// Also do an immediate one-shot REST fetch on load so the UI isn't empty
// while the websocket handshake completes.
fetch('/api/system').then((r) => r.json()).then(renderSnapshot).catch(() => {});

connect();
