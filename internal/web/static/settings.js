const form = document.getElementById('settings-form');
const settingsMessage = document.getElementById('settings-message');
const importMessage = document.getElementById('import-message');

function showMessage(node, kind, text) {
  node.className = `message is-visible ${kind}`;
  node.textContent = text;
}

function hideMessage(node) {
  node.className = 'message';
  node.textContent = '';
}

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return '-';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(value >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

function formatTime(value) {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function fillForm(config) {
  document.getElementById('listen_addr').value = config.listen_addr || '';
  document.getElementById('database_path').value = config.database_path || '';
  document.getElementById('steam_api_key').value = config.steam_api_key || '';
  document.getElementById('steam_id').value = config.steam_id || '';
  document.getElementById('collect_interval_seconds').value = config.collect_interval_seconds || 300;
  document.getElementById('collect_on_start').checked = Boolean(config.collect_on_start);
  document.getElementById('auth_enable').checked = Boolean(config.auth?.enable);
  document.getElementById('auth_username').value = config.auth?.username || '';
  document.getElementById('auth_password').value = config.auth?.password || '';
}

function renderSettings(payload) {
  fillForm(payload.config);
  document.getElementById('config-path').textContent = payload.config_path || '-';
  const overrideKeys = Object.keys(payload.environment_overrides || {});
  document.getElementById('override-count').textContent = String(overrideKeys.length);
  document.getElementById('override-list').textContent = overrideKeys.length
    ? `环境变量覆盖：${overrideKeys.join('、')}`
    : '当前没有环境变量覆盖。';
}

function renderStatus(payload) {
  document.getElementById('db-size').textContent = formatBytes(payload.database_size_bytes);
  document.getElementById('snapshot-count').textContent = String(payload.summary?.snapshot_count ?? '-');
  document.getElementById('run-count').textContent = String(payload.summary?.run_count ?? '-');
  document.getElementById('goroutines').textContent = String(payload.runtime?.goroutines ?? '-');
  document.getElementById('collector-running').textContent = payload.collector?.running ? '运行中' : '空闲';
  document.getElementById('heap-alloc').textContent = formatBytes(payload.runtime?.heap_alloc);

  const runtimeList = [
    ['Database Path', payload.database_path],
    ['Working Directory', payload.working_directory],
    ['Collect Interval', `${payload.collector?.collect_interval_seconds ?? '-'} sec`],
    ['Collect On Start', payload.collector?.collect_on_start ? 'Yes' : 'No'],
    ['Go Version', payload.runtime?.go_version],
    ['GOMAXPROCS / CPU', `${payload.runtime?.gomaxprocs ?? '-'} / ${payload.runtime?.cpu_count ?? '-'}`],
    ['Memory Sys', formatBytes(payload.runtime?.memory_sys)],
    ['Heap Objects', String(payload.runtime?.heap_objects ?? '-')],
    ['GC Count', String(payload.runtime?.num_gc ?? '-')],
    ['Last GC', formatTime(payload.runtime?.last_gc_time)],
    ['Server Started', formatTime(payload.server_started_at)],
    ['Uptime', `${payload.runtime?.uptime_seconds ?? 0} sec`],
  ];

  document.getElementById('runtime-list').innerHTML = runtimeList.map(([key, value]) => `
    <div class="meta-row">
      <div class="meta-key">${key}</div>
      <div class="meta-value">${value ?? '-'}</div>
    </div>
  `).join('');

  const runs = payload.last_runs || [];
  document.getElementById('recent-runs').innerHTML = runs.length
    ? runs.map((run) => `
      <div class="run-row">
        <div class="run-key">${run.status}</div>
        <div class="run-value">${formatTime(run.started_at)} · ${run.fetched_count} / ${run.friend_count} friends</div>
      </div>
    `).join('')
    : `<div class="run-row"><div class="run-key">Recent Runs</div><div class="run-value">还没有采集记录。</div></div>`;
}

async function loadSettings() {
  const response = await fetch('/api/settings');
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || '加载配置失败');
  renderSettings(payload);
}

async function loadStatus() {
  const response = await fetch('/api/system/status');
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || '加载状态失败');
  renderStatus(payload);
}

function collectFormPayload() {
  return {
    listen_addr: document.getElementById('listen_addr').value.trim(),
    steam_api_key: document.getElementById('steam_api_key').value.trim(),
    steam_id: document.getElementById('steam_id').value.trim(),
    database_path: document.getElementById('database_path').value.trim(),
    collect_interval_seconds: Number(document.getElementById('collect_interval_seconds').value),
    collect_on_start: document.getElementById('collect_on_start').checked,
    auth: {
      enable: document.getElementById('auth_enable').checked,
      username: document.getElementById('auth_username').value.trim(),
      password: document.getElementById('auth_password').value.trim(),
    },
  };
}

async function saveSettings(event) {
  event.preventDefault();
  hideMessage(settingsMessage);
  const response = await fetch('/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(collectFormPayload()),
  });
  const payload = await response.json();
  if (!response.ok) {
    showMessage(settingsMessage, 'error', payload.error || '保存失败');
    return;
  }
  showMessage(settingsMessage, 'success', `配置已写入 ${payload.config_path}`);
  await loadSettings();
}

async function runImport() {
  hideMessage(importMessage);
  const fileInput = document.getElementById('import-file');
  const file = fileInput.files && fileInput.files[0];
  if (!file) {
    showMessage(importMessage, 'error', '请先选择要导入的文件。');
    return;
  }
  const formData = new FormData();
  formData.append('file', file);
  formData.append('format', document.getElementById('import-format').value);

  const response = await fetch('/api/data/import', {
    method: 'POST',
    body: formData,
  });
  const payload = await response.json();
  if (!response.ok) {
    showMessage(importMessage, 'error', payload.error || '导入失败');
    return;
  }
  showMessage(importMessage, 'success', `导入完成：${payload.runs} runs，${payload.snapshots} snapshots。`);
  await loadStatus();
}

async function bootstrap() {
  try {
    await Promise.all([loadSettings(), loadStatus()]);
  } catch (error) {
    showMessage(settingsMessage, 'error', error.message);
  }
}

form.addEventListener('submit', saveSettings);
document.getElementById('reload-settings-button').addEventListener('click', loadSettings);
document.getElementById('refresh-status-button').addEventListener('click', loadStatus);
document.getElementById('import-button').addEventListener('click', runImport);
document.querySelectorAll('[data-export-format]').forEach((button) => {
  button.addEventListener('click', () => {
    const format = button.getAttribute('data-export-format');
    window.location.href = `/api/data/export?format=${encodeURIComponent(format)}`;
  });
});

bootstrap();
