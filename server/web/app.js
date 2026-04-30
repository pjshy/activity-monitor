const state = {
  processes: [],
  summary: null,
  query: '',
  sortKey: 'cpu',
  sortDirection: 'desc',
};

const elements = {
  status: document.querySelector('#status'),
  processCount: document.querySelector('#processCount'),
  cpuUsage: document.querySelector('#cpuUsage'),
  memoryUsage: document.querySelector('#memoryUsage'),
  loadAverage: document.querySelector('#loadAverage'),
  sampledAt: document.querySelector('#sampledAt'),
  processRows: document.querySelector('#processRows'),
  searchInput: document.querySelector('#searchInput'),
  refreshButton: document.querySelector('#refreshButton'),
  sortButtons: document.querySelectorAll('[data-sort]'),
};

async function loadProcesses() {
  setStatus('SYNCING');
  try {
    const response = await fetch('/api/processes', { cache: 'no-store' });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    const snapshot = await response.json();
    state.summary = snapshot.summary;
    state.processes = snapshot.processes ?? [];
    render(snapshot.error);
  } catch (error) {
    setStatus('OFFLINE');
    elements.processRows.innerHTML = `
      <tr>
        <td colspan="7" class="empty">Unable to load process data: ${escapeHtml(error.message)}</td>
      </tr>
    `;
  }
}

function render(error) {
  renderSummary(error);
  renderTable();
  updateSortButtons();
}

function renderSummary(error) {
  const summary = state.summary ?? {};
  setStatus(error ? 'DEGRADED' : 'LIVE');
  elements.processCount.textContent = formatNumber(summary.processCount);
  elements.cpuUsage.textContent = `${formatNumber(summary.cpu, 1)}%`;
  elements.memoryUsage.textContent = `${formatNumber(summary.memoryUsed, 1)}%`;
  elements.loadAverage.textContent = summary.loadAverage || 'n/a';
  elements.sampledAt.textContent = summary.sampledAt
    ? `Sampled ${new Date(summary.sampledAt).toLocaleTimeString()}`
    : 'Waiting for first sample';
}

function renderTable() {
  const rows = filteredProcesses().slice(0, 250);

  if (rows.length === 0) {
    elements.processRows.innerHTML = `
      <tr>
        <td colspan="7" class="empty">No processes match this filter.</td>
      </tr>
    `;
    return;
  }

  elements.processRows.innerHTML = rows.map((process) => `
    <tr>
      <td class="numeric">${process.pid}</td>
      <td>${escapeHtml(process.user)}</td>
      <td>${escapeHtml(shortCommand(process))}</td>
      <td class="numeric hot">${formatNumber(process.cpu, 1)}%</td>
      <td class="numeric">${formatNumber(process.memory, 1)}%</td>
      <td class="numeric">${formatBytes((process.rssKb ?? 0) * 1024)}</td>
      <td>${escapeHtml(process.state || '-')}</td>
    </tr>
  `).join('');
}

function filteredProcesses() {
  const query = state.query.trim().toLowerCase();
  const rows = query
    ? state.processes.filter((process) => {
        const haystack = [
          process.pid,
          process.user,
          process.command,
          process.args,
          process.state,
        ].join(' ').toLowerCase();
        return haystack.includes(query);
      })
    : [...state.processes];

  rows.sort((a, b) => compare(a, b, state.sortKey) * (state.sortDirection === 'asc' ? 1 : -1));
  return rows;
}

function compare(a, b, key) {
  const left = a[key];
  const right = b[key];
  if (typeof left === 'number' && typeof right === 'number') {
    return left - right;
  }
  return String(left ?? '').localeCompare(String(right ?? ''));
}

function updateSortButtons() {
  elements.sortButtons.forEach((button) => {
    const active = button.dataset.sort === state.sortKey;
    button.classList.toggle('active', active);
    button.textContent = active
      ? `${button.dataset.label} ${state.sortDirection === 'asc' ? '↑' : '↓'}`
      : button.dataset.label;
  });
}

function setStatus(value) {
  elements.status.textContent = value;
}

function shortCommand(process) {
  const source = process.args || process.command || '';
  const segments = source.split('/');
  return segments[segments.length - 1] || source;
}

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${formatNumber(value, value >= 10 ? 0 : 1)} ${units[unit]}`;
}

function formatNumber(value, digits = 0) {
  if (!Number.isFinite(Number(value))) return '0';
  return Number(value).toLocaleString(undefined, {
    maximumFractionDigits: digits,
    minimumFractionDigits: digits,
  });
}

function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}

elements.searchInput.addEventListener('input', (event) => {
  state.query = event.target.value;
  renderTable();
});

elements.refreshButton.addEventListener('click', loadProcesses);

elements.sortButtons.forEach((button) => {
  button.addEventListener('click', () => {
    const nextSort = button.dataset.sort;
    if (state.sortKey === nextSort) {
      state.sortDirection = state.sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      state.sortKey = nextSort;
      state.sortDirection = 'desc';
    }
    renderTable();
    updateSortButtons();
  });
});

loadProcesses();
setInterval(loadProcesses, 3000);
