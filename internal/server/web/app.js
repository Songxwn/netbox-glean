const state = {
  page: 'dashboard',
  targets: [],
  jobs: [],
  settings: null,
  stats: null,
  version: null,
  editingId: null,
  search: '',
  openJobId: null,
  pollTimer: null,
};

const $ = (id) => document.getElementById(id);

const esc = (value) => String(value ?? '').replace(/[&<>"']/g, (ch) => ({
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
}[ch]));

const protocolLabels = { snmp: 'SNMP', ipmi: 'IPMI', redfish: 'Redfish' };
const modeLabels = { collect: '采集预览', sync: '采集并同步' };
const triggerLabels = { manual: '手动', schedule: '定时' };
const statusLabels = { queued: '排队', running: '运行中', success: '成功', error: '失败' };

async function api(path, options = {}) {
  const headers = Object.assign({}, options.headers || {});
  let body = options.body;
  if (body !== undefined && body !== null && typeof body !== 'string') {
    body = JSON.stringify(body);
    headers['Content-Type'] = 'application/json';
  }
  const response = await fetch(path, {
    method: options.method || 'GET',
    headers,
    body,
    credentials: 'same-origin',
  });
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const data = await response.json();
      if (data && data.error) message = data.error;
    } catch (_) { /* ignore */ }
    throw new Error(message);
  }
  if (response.status === 204) return null;
  return response.json();
}

function toast(message, type = 'info') {
  const node = document.createElement('div');
  node.className = `toast ${type}`;
  node.textContent = message;
  $('toasts').appendChild(node);
  setTimeout(() => node.remove(), type === 'error' ? 8000 : 4500);
}

function setVal(id, value) {
  const el = $(id);
  if (el) el.value = value ?? '';
}

function setChecked(id, value) {
  const el = $(id);
  if (el) el.checked = Boolean(value);
}

function val(id) {
  const el = $(id);
  return el ? el.value.trim() : '';
}

function checked(id) {
  const el = $(id);
  return el ? el.checked : false;
}

function fmtTime(value) {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function fmtDuration(job) {
  if (!job || !job.started_at) return '—';
  if (!job.finished_at) return job.status === 'running' ? '进行中' : '—';
  const ms = new Date(job.finished_at) - new Date(job.started_at);
  if (Number.isNaN(ms) || ms < 0) return '—';
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60000).toFixed(1)} min`;
}

function minutesLabel(minutes) {
  const value = Number(minutes) || 60;
  if (value % 1440 === 0) return `${value / 1440} 天`;
  if (value % 60 === 0) return `${value / 60} 小时`;
  return `${value} 分钟`;
}

function scheduleCell(target) {
  if (!target.schedule_enabled) return '<span class="muted">关闭</span>';
  const last = target.last_run_at
    ? `<div class="muted">上次 ${esc(fmtTime(target.last_run_at))}</div>`
    : '<div class="muted">等待首次执行</div>';
  return `<span class="badge badge-success">每 ${minutesLabel(target.schedule_interval_minutes)}</span>${last}`;
}

function discoveryCell(target) {
  if (!target.discover_ips) return '<span class="muted">关闭</span>';
  const subnets = String(target.scan_subnets || '').split(/[,\s;]+/).filter(Boolean).length;
  const scan = subnets
    ? `<div class="muted">扫描 ${subnets} 个网段</div>`
    : '<div class="muted">仅 ARP/NDP</div>';
  return `<span class="badge badge-success">开启</span>${scan}`;
}

function statusBadge(status) {
  return `<span class="badge badge-${esc(status)}">${esc(statusLabels[status] || status)}</span>`;
}

function protocolBadge(protocol) {
  return `<span class="badge badge-protocol">${esc(protocolLabels[protocol] || protocol)}</span>`;
}

function setPage(page) {
  state.page = page;
  document.querySelectorAll('.nav-item').forEach((btn) => {
    btn.classList.toggle('active', btn.dataset.page === page);
  });
  document.querySelectorAll('.page').forEach((node) => node.classList.add('hidden'));
  const section = $(`page-${page}`);
  if (section) section.classList.remove('hidden');
  const titles = {
    dashboard: ['概览', '通过 SNMP / IPMI / Redfish 采集设备硬件信息并写入 NetBox'],
    targets: ['采集目标', '管理设备地址、凭据与 NetBox 归属'],
    jobs: ['任务记录', '查看采集日志、硬件摘要与 NetBox 同步结果'],
    settings: ['设置', '配置 NetBox 连接、默认归属和同步行为'],
  };
  const [title, subtitle] = titles[page] || ['netbox-glean', ''];
  $('page-title').textContent = title;
  $('page-subtitle').textContent = subtitle;
  if (page === 'jobs') renderJobs();
  if (page === 'targets') renderTargets();
  if (page === 'dashboard') renderDashboard();
}

async function boot() {
  try {
    const [version, settings, targets, jobs, stats] = await Promise.all([
      api('/api/version'),
      api('/api/settings'),
      api('/api/targets'),
      api('/api/jobs?limit=200'),
      api('/api/stats'),
    ]);
    state.version = version;
    state.settings = settings;
    state.targets = targets;
    state.jobs = jobs;
    state.stats = stats;
    fillSettings(settings);
    renderAll();
    startPolling();
  } catch (err) {
    toast(`加载失败: ${err.message}`, 'error');
  }
}

function startPolling() {
  if (state.pollTimer) clearInterval(state.pollTimer);
  state.pollTimer = setInterval(async () => {
    if (document.hidden) return;
    try {
      state.pollCount = (state.pollCount || 0) + 1;
      const wantTargets = state.pollCount % 5 === 0;
      const requests = [api('/api/jobs?limit=200'), api('/api/stats')];
      if (wantTargets) requests.push(api('/api/targets'));
      const [jobs, stats, targets] = await Promise.all(requests);
      state.jobs = jobs;
      state.stats = stats;
      if (targets) {
        state.targets = targets;
        renderTargets();
      }
      renderDashboard();
      renderJobs();
      if (state.openJobId && !$('drawer-job').classList.contains('hidden')) {
        await refreshOpenJob();
      }
    } catch (_) { /* transient */ }
  }, 4000);
}

function renderAll() {
  const version = state.version ? (state.version.version || 'dev') : 'dev';
  $('version-label').textContent = version === 'dev' || version.startsWith('dev-') ? version : `v${version}`;
  renderDashboard();
  renderTargets();
  renderJobs();
}

function renderDashboard() {
  const stats = state.stats || {};
  const jobs = stats.jobs || {};
  $('stat-targets').textContent = stats.targets ?? state.targets.length;
  $('stat-targets-hint').textContent = `启用 ${stats.targets_enabled ?? state.targets.filter((t) => t.enabled).length} 个`;
  $('stat-running').textContent = jobs.running ?? 0;
  $('stat-queued').textContent = `排队 ${jobs.queued ?? 0} 个`;
  $('stat-success').textContent = jobs.success ?? 0;
  $('stat-error').textContent = jobs.error ?? 0;

  const byProtocol = stats.by_protocol || {};
  const rows = ['snmp', 'ipmi', 'redfish'].map((protocol) => {
    const count = byProtocol[protocol] || 0;
    const total = Math.max(1, ...['snmp', 'ipmi', 'redfish'].map((p) => byProtocol[p] || 0));
    return `<div class="protocol-row">
      <span class="badge badge-protocol">${protocolLabels[protocol]}</span>
      <span class="bar"><span style="width:${Math.round((count / total) * 100)}%"></span></span>
      <strong>${count}</strong>
    </div>`;
  }).join('');
  $('protocol-list').innerHTML = rows;

  const settings = state.settings || {};
  const stateText = stats.netbox_configured
    ? `NetBox: ${esc(settings.netbox_url || '已配置')}${settings.site ? ` · site <span class="mono">${esc(settings.site)}</span>` : ' · 未设置 site'}`
    : 'NetBox 尚未配置，请前往“设置”填写 URL 与 Token。';
  $('netbox-state').innerHTML = stateText;

  const recent = state.jobs.slice(0, 8);
  $('recent-empty').classList.toggle('hidden', recent.length > 0);
  $('recent-jobs').innerHTML = recent.map((job) => `
    <tr data-job-id="${esc(job.id)}" style="cursor:pointer">
      <td>${esc(job.target_name)}<div class="muted mono">${esc(job.id)}</div></td>
      <td>${esc(modeLabels[job.mode] || job.mode)}${job.trigger === 'schedule' ? ' · 定时' : ''}</td>
      <td>${statusBadge(job.status)}</td>
      <td class="muted">${esc(fmtTime(job.created_at))}</td>
    </tr>
  `).join('');
}

function renderTargets() {
  const search = state.search.toLowerCase();
  const targets = state.targets.filter((t) => {
    if (!search) return true;
    return `${t.name} ${t.address} ${t.device_name || ''}`.toLowerCase().includes(search);
  });
  const empty = $('targets-empty');
  empty.classList.toggle('hidden', targets.length > 0);
  empty.textContent = state.targets.length === 0
    ? '还没有采集目标。点击“新增目标”开始，需要先在设置中配置 NetBox。'
    : '没有匹配的目标。';

  $('targets-body').innerHTML = targets.map((target) => `
    <tr>
      <td>
        <strong>${esc(target.name)}</strong>
        <div class="muted">${esc(target.description || '')}</div>
      </td>
      <td>${protocolBadge(target.protocol)}</td>
      <td class="mono">${esc(target.address)}:${esc(target.port)}</td>
      <td>${target.site ? `<span class="mono">${esc(target.site)}</span>` : '<span class="muted">默认</span>'}</td>
      <td>${target.device_name ? esc(target.device_name) : '<span class="muted">自动</span>'}</td>
      <td>${scheduleCell(target)}</td>
      <td>${discoveryCell(target)}</td>
      <td>${target.enabled ? '<span class="badge badge-success">启用</span>' : '<span class="badge badge-queued">停用</span>'}</td>
      <td>
        <div class="actions">
          <button class="btn small" data-action="collect" data-id="${esc(target.id)}">采集</button>
          <button class="btn small primary" data-action="sync" data-id="${esc(target.id)}">同步</button>
          <button class="btn small" data-action="edit" data-id="${esc(target.id)}">编辑</button>
          <button class="btn small danger" data-action="delete" data-id="${esc(target.id)}">删除</button>
        </div>
      </td>
    </tr>
  `).join('');
}

function renderJobs() {
  $('jobs-summary').textContent = `共 ${state.jobs.length} 条任务记录，保留在内存中，重启后清空。`;
  $('jobs-empty').classList.toggle('hidden', state.jobs.length > 0);
  $('jobs-body').innerHTML = state.jobs.map((job) => `
    <tr>
      <td><strong>${esc(job.target_name)}</strong><div class="muted mono">${esc(job.id)}</div></td>
      <td>${protocolBadge(job.protocol)}</td>
      <td>${esc(modeLabels[job.mode] || job.mode)}${job.trigger === 'schedule' ? ' · 定时' : ''}</td>
      <td>${statusBadge(job.status)}${job.error ? `<div class="muted">${esc(job.error.slice(0, 80))}</div>` : ''}</td>
      <td>${job.interfaces}</td>
      <td>${job.components}</td>
      <td class="muted">${esc(fmtTime(job.started_at || job.created_at))}</td>
      <td class="muted">${esc(fmtDuration(job))}</td>
      <td class="right"><button class="btn small" data-job="${esc(job.id)}">查看</button></td>
    </tr>
  `).join('');
}

function fillSettings(settings) {
  setVal('set-netbox-url', settings.netbox_url);
  setVal('set-netbox-token', '');
  $('set-netbox-token').placeholder = settings.netbox_token_set
    ? '已保存 Token，留空表示不修改'
    : '粘贴 v1 Token 或 v2 nbt_ Token';
  setVal('set-token-version', settings.token_version || 'auto');
  setChecked('set-netbox-insecure', settings.netbox_insecure);
  setVal('set-site', settings.site);
  setVal('set-site-name', settings.site_name);
  setVal('set-device-role', settings.device_role);
  setVal('set-platform', settings.platform);
  setVal('set-u-height', settings.u_height || 1);
  setVal('set-description', settings.description);
  setChecked('set-sync-interfaces', settings.sync_interfaces);
  setChecked('set-sync-inventory', settings.sync_inventory);
  setChecked('set-sync-cables', settings.sync_cables);
  setChecked('set-sync-ip-addresses', settings.sync_ip_addresses);
  setChecked('set-sync-unmatched-ips', settings.sync_unmatched_ips);
  setChecked('set-prune-interfaces', settings.prune_interfaces);
  setChecked('set-custom-fields', settings.custom_fields);
  setChecked('set-assign-ip', settings.assign_primary_ip);
  setVal('set-primary-interface', settings.primary_interface || 'mgmt0');
}

async function reloadTargets() {
  state.targets = await api('/api/targets');
  renderTargets();
  await reloadStats();
}

async function reloadStats() {
  state.stats = await api('/api/stats');
  renderDashboard();
}

async function collectTarget(target, sync) {
  const verb = sync ? '采集并同步' : '采集';
  if (sync && !confirm(`确定要采集并同步 ${target.name} 到 NetBox 吗？`)) return;
  try {
    await api(`/api/targets/${target.id}/collect`, { method: 'POST', body: { sync } });
    toast(`已提交${verb}任务: ${target.name}`, 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(`${verb}失败: ${err.message}`, 'error');
  }
}

async function refreshJobs() {
  state.jobs = await api('/api/jobs?limit=200');
  state.stats = await api('/api/stats');
  renderDashboard();
  renderJobs();
}

function openTargetModal(target) {
  state.editingId = target ? target.id : null;
  $('target-modal-title').textContent = target ? `编辑目标 · ${target.name}` : '新增目标';
  const snmp = (target && target.snmp) || { version: 'v2c', v3_auth_protocol: 'SHA256', v3_priv_protocol: 'AES' };
  const ipmi = (target && target.ipmi) || { interface: 'lanplus' };
  const redfish = (target && target.redfish) || {};
  setVal('t-name', target ? target.name : '');
  setVal('t-protocol', target ? target.protocol : 'snmp');
  setVal('t-address', target ? target.address : '');
  setVal('t-port', target ? target.port : 161);
  setVal('t-site', target ? target.site : '');
  setVal('t-role', target ? target.device_role : '');
  setVal('t-platform', target ? target.platform : '');
  setVal('t-device-name', target ? target.device_name : '');
  setVal('t-description', target ? target.description : '');
  setChecked('t-enabled', target ? target.enabled : true);
  setChecked('t-schedule-enabled', target ? Boolean(target.schedule_enabled) : false);
  setVal('t-schedule-interval', target && target.schedule_interval_minutes ? target.schedule_interval_minutes : 60);
  setChecked('t-discover-ips', target ? Boolean(target.discover_ips) : false);
  setVal('t-scan-subnets', target ? target.scan_subnets : '');

  setVal('s-version', snmp.version || 'v2c');
  setVal('s-community', '');
  setVal('s-v3-user', snmp.v3_username || '');
  setVal('s-v3-auth-proto', snmp.v3_auth_protocol || 'SHA256');
  setVal('s-v3-auth-pass', '');
  setVal('s-v3-priv-proto', snmp.v3_priv_protocol || 'AES');
  setVal('s-v3-priv-pass', '');

  setVal('i-user', ipmi.username || '');
  setVal('i-pass', '');
  setVal('i-interface', ipmi.interface || 'lanplus');

  setVal('r-user', redfish.username || '');
  setVal('r-pass', '');
  setVal('r-system', redfish.system_id || '');
  setChecked('r-insecure', redfish.insecure);

  toggleProtocolFields(target ? target.protocol : 'snmp');
  $('modal-target').classList.remove('hidden');
  $('t-name').focus();
}

function toggleProtocolFields(protocol) {
  document.querySelectorAll('.proto').forEach((node) => {
    node.classList.add('hidden');
  });
  document.querySelectorAll(`.proto-${protocol}`).forEach((node) => {
    node.classList.remove('hidden');
  });
}

function closeTargetModal() {
  $('modal-target').classList.add('hidden');
  state.editingId = null;
}

async function saveTarget(event) {
  event.preventDefault();
  const target = {
    name: val('t-name'),
    protocol: val('t-protocol'),
    address: val('t-address'),
    port: Number.parseInt(val('t-port'), 10) || 0,
    enabled: checked('t-enabled'),
    site: val('t-site'),
    device_role: val('t-role'),
    platform: val('t-platform'),
    device_name: val('t-device-name'),
    description: val('t-description'),
    schedule_enabled: checked('t-schedule-enabled'),
    schedule_interval_minutes: Number.parseInt(val('t-schedule-interval'), 10) || 60,
    discover_ips: checked('t-discover-ips'),
    scan_subnets: val('t-scan-subnets'),
    snmp: {
      version: val('s-version'),
      community: val('s-community'),
      v3_username: val('s-v3-user'),
      v3_auth_protocol: val('s-v3-auth-proto'),
      v3_auth_password: val('s-v3-auth-pass'),
      v3_priv_protocol: val('s-v3-priv-proto'),
      v3_priv_password: val('s-v3-priv-pass'),
    },
    ipmi: {
      username: val('i-user'),
      password: val('i-pass'),
      interface: val('i-interface'),
    },
    redfish: {
      username: val('r-user'),
      password: val('r-pass'),
      insecure: checked('r-insecure'),
      system_id: val('r-system'),
    },
  };
  try {
    if (state.editingId) {
      await api(`/api/targets/${state.editingId}`, { method: 'PUT', body: target });
      toast('目标已更新', 'success');
    } else {
      await api('/api/targets', { method: 'POST', body: target });
      toast('目标已创建', 'success');
    }
    closeTargetModal();
    await reloadTargets();
  } catch (err) {
    toast(`保存失败: ${err.message}`, 'error');
  }
}

async function saveSettings(event) {
  event.preventDefault();
  const settings = {
    netbox_url: val('set-netbox-url'),
    netbox_token: val('set-netbox-token'),
    token_version: val('set-token-version'),
    netbox_insecure: checked('set-netbox-insecure'),
    site: val('set-site'),
    site_name: val('set-site-name'),
    device_role: val('set-device-role'),
    platform: val('set-platform'),
    u_height: Number.parseInt(val('set-u-height'), 10) || 1,
    sync_interfaces: checked('set-sync-interfaces'),
    sync_inventory: checked('set-sync-inventory'),
    sync_cables: checked('set-sync-cables'),
    sync_ip_addresses: checked('set-sync-ip-addresses'),
    sync_unmatched_ips: checked('set-sync-unmatched-ips'),
    prune_interfaces: checked('set-prune-interfaces'),
    custom_fields: checked('set-custom-fields'),
    assign_primary_ip: checked('set-assign-ip'),
    primary_interface: val('set-primary-interface') || 'mgmt0',
    description: val('set-description'),
  };
  try {
    state.settings = await api('/api/settings', { method: 'PUT', body: settings });
    fillSettings(state.settings);
    toast('设置已保存', 'success');
    renderDashboard();
  } catch (err) {
    toast(`保存设置失败: ${err.message}`, 'error');
  }
}

async function testNetbox() {
  const payload = {
    netbox_url: val('set-netbox-url'),
    netbox_token: val('set-netbox-token'),
    token_version: val('set-token-version'),
    netbox_insecure: checked('set-netbox-insecure'),
  };
  try {
    const result = await api('/api/settings/test', { method: 'POST', body: payload });
    toast(`NetBox ${result.netbox_version || '已连接'} · 认证 ${result.auth_mode === '2' ? 'v2 Bearer' : 'v1 Token'}`, 'success');
  } catch (err) {
    toast(`连接失败: ${err.message}`, 'error');
  }
}

async function collectAll(sync) {
  if (sync && !confirm('确定要采集所有启用的目标并同步到 NetBox 吗？')) return;
  try {
    const result = await api('/api/collect', { method: 'POST', body: { sync } });
    toast(`已提交 ${result.count} 个任务`, 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(`提交失败: ${err.message}`, 'error');
  }
}

async function openJob(id) {
  state.openJobId = id;
  $('drawer-job').classList.remove('hidden');
  $('job-body').innerHTML = '<p class="muted">加载中…</p>';
  await refreshOpenJob();
}

async function refreshOpenJob() {
  if (!state.openJobId) return;
  try {
    const job = await api(`/api/jobs/${state.openJobId}`);
    renderJobDetail(job);
  } catch (err) {
    $('job-body').innerHTML = `<p class="badge badge-error">${esc(err.message)}</p>`;
  }
}

function renderJobDetail(job) {
  $('job-title').textContent = job.target_name;
  $('job-subtitle').textContent = `${modeLabels[job.mode] || job.mode} · ${fmtTime(job.created_at)}`;

  const result = job.result || null;
  const sync = job.sync || null;
  const identity = result ? (result.identity || {}) : {};

  const kv = `
    <dl class="kv">
      <dt>任务 ID</dt><dd class="mono">${esc(job.id)}</dd>
      <dt>协议</dt><dd>${protocolBadge(job.protocol)}</dd>
      <dt>模式</dt><dd>${esc(modeLabels[job.mode] || job.mode)}</dd>
      <dt>触发</dt><dd>${esc(triggerLabels[job.trigger] || '手动')}</dd>
      <dt>创建 / 开始 / 结束</dt><dd>${esc(fmtTime(job.created_at))} / ${esc(fmtTime(job.started_at))} / ${esc(fmtTime(job.finished_at))}</dd>
      <dt>耗时</dt><dd>${esc(fmtDuration(job))}</dd>
      ${job.error ? `<dt>错误</dt><dd class="badge-error">${esc(job.error)}</dd>` : ''}
    </dl>`;

  const identityBlock = result ? `
    <h3 class="section-title">设备信息</h3>
    <dl class="kv">
      <dt>名称</dt><dd>${esc(identity.name || '—')}</dd>
      <dt>厂商 / 型号</dt><dd>${esc(identity.manufacturer || '—')} / ${esc(identity.model || '—')}</dd>
      <dt>序列号 / 资产标签</dt><dd>${esc(identity.serial || '—')} / ${esc(identity.asset_tag || '—')}</dd>
      <dt>电源状态</dt><dd>${esc(identity.power_state || '—')}</dd>
      <dt>CPU</dt><dd>${esc(identity.cpu_model || '—')}${identity.cpu_cores ? ` · ${identity.cpu_cores} 核` : ''}</dd>
      <dt>内存</dt><dd>${identity.memory_gb ? `${identity.memory_gb} GB` : '—'}</dd>
      <dt>BIOS / BMC</dt><dd>${esc(identity.bios_version || '—')} / ${esc(identity.bmc_version || '—')}</dd>
      <dt>接口 / 组件</dt><dd>${(result.interfaces || []).length} / ${(result.components || []).length}</dd>
    </dl>` : '<p class="muted">尚无采集结果。</p>';

  const neighborList = result && Array.isArray(result.neighbors) ? result.neighbors : [];
  const neighborBlock = neighborList.length ? `
    <h3 class="section-title">LLDP 邻居</h3>
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>本端接口</th><th>远端设备</th><th>远端端口</th><th>端口描述</th></tr></thead>
        <tbody>${neighborList.map((n) => `<tr>
          <td class="mono">${esc(n.local_interface || '—')}</td>
          <td>${esc(n.remote_system || '—')}</td>
          <td class="mono">${esc(n.remote_port || '—')}</td>
          <td class="muted">${esc(n.remote_port_desc || '—')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>` : '';

  const ipList = result && Array.isArray(result.ip_addresses) ? result.ip_addresses : [];
  const ipBlock = ipList.length ? `
    <h3 class="section-title">发现的 IP 地址</h3>
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>地址</th><th>MAC</th><th>接口</th><th>来源</th></tr></thead>
        <tbody>${ipList.slice(0, 200).map((entry) => `<tr>
          <td class="mono">${esc(entry.address || '—')}</td>
          <td class="mono">${esc(entry.mac || '—')}</td>
          <td class="mono">${esc(entry.interface || '—')}</td>
          <td class="muted">${esc(entry.source || '—')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>
    ${ipList.length > 200 ? `<p class="muted">仅显示前 200 条，共 ${ipList.length} 条。</p>` : ''}` : '';

  const warnings = [];
  if (result && Array.isArray(result.warnings)) warnings.push(...result.warnings);
  if (sync && Array.isArray(sync.warnings)) warnings.push(...sync.warnings);
  const warningBlock = warnings.length
    ? `<h3 class="section-title">警告</h3><ul class="list warn-list">${warnings.map((w) => `<li>${esc(w)}</li>`).join('')}</ul>`
    : '';

  const syncBlock = sync ? `
    <h3 class="section-title">NetBox 同步结果</h3>
    <dl class="kv">
      <dt>设备</dt><dd>${esc(sync.device_name || '—')} (#${sync.device_id}) ${sync.device_url ? `· <a href="${esc(sync.device_url)}" target="_blank" rel="noreferrer">打开</a>` : ''}</dd>
      <dt>接口</dt><dd>新建 ${sync.interfaces_created} / 更新 ${sync.interfaces_updated}</dd>
      <dt>LLDP 连线</dt><dd>新建 ${sync.cables_created || 0} / 跳过 ${sync.cables_skipped || 0}</dd>
      <dt>发现的 IP</dt><dd>新建 ${sync.ip_created || 0} / 分配 ${sync.ip_assigned || 0} / 跳过 ${sync.ip_skipped || 0}</dd>
      <dt>硬件清单</dt><dd>新建 ${sync.inventory_created} / 更新 ${sync.inventory_updated}</dd>
      ${sync.ip_address ? `<dt>主 IP</dt><dd class="mono">${esc(sync.ip_address)}</dd>` : ''}
    </dl>
    ${Array.isArray(sync.actions) && sync.actions.length ? `<ul class="list">${sync.actions.map((a) => `<li>${esc(a)}</li>`).join('')}</ul>` : ''}
  ` : '';

  const log = Array.isArray(job.log) && job.log.length
    ? job.log.join('\n')
    : '(no log)';

  $('job-body').innerHTML = `
    <div class="job-status-head">${statusBadge(job.status)}${job.error ? '<span class="badge badge-error">查看错误</span>' : ''}</div>
    ${kv}
    ${syncBlock}
    ${identityBlock}
    ${neighborBlock}
    ${ipBlock}
    ${warningBlock}
    <h3 class="section-title">任务日志</h3>
    <pre class="log-box mono">${esc(log)}</pre>
    <details>
      <summary class="muted" style="cursor:pointer;margin-top:14px">原始采集结果 JSON</summary>
      <pre class="json-box mono">${esc(JSON.stringify(result, null, 2) || 'null')}</pre>
    </details>
  `;
}

function bindEvents() {
  document.querySelectorAll('.nav-item').forEach((btn) => {
    btn.addEventListener('click', () => setPage(btn.dataset.page));
  });
  document.querySelectorAll('[data-goto]').forEach((btn) => {
    btn.addEventListener('click', () => setPage(btn.dataset.goto));
  });

  $('btn-collect-all').addEventListener('click', () => collectAll(false));
  $('btn-sync-all').addEventListener('click', () => collectAll(true));
  $('btn-add-target').addEventListener('click', () => openTargetModal(null));
  $('btn-test-netbox').addEventListener('click', testNetbox);
  $('btn-clear-jobs').addEventListener('click', async () => {
    try {
      const result = await api('/api/jobs', { method: 'DELETE' });
      toast(`已清理 ${result.removed} 条任务记录`, 'success');
      await refreshJobs();
    } catch (err) {
      toast(`清理失败: ${err.message}`, 'error');
    }
  });

  $('target-search').addEventListener('input', (event) => {
    state.search = event.target.value.trim();
    renderTargets();
  });

  $('t-protocol').addEventListener('change', (event) => {
    const defaults = { snmp: 161, ipmi: 623, redfish: 443 };
    toggleProtocolFields(event.target.value);
    setVal('t-port', defaults[event.target.value] || '');
  });

  $('target-form').addEventListener('submit', saveTarget);
  $('settings-form').addEventListener('submit', saveSettings);

  $('targets-body').addEventListener('click', async (event) => {
    const button = event.target.closest('button[data-action]');
    if (!button) return;
    const target = state.targets.find((item) => item.id === button.dataset.id);
    if (!target) return;
    switch (button.dataset.action) {
      case 'collect':
        await collectTarget(target, false);
        break;
      case 'sync':
        await collectTarget(target, true);
        break;
      case 'edit':
        openTargetModal(target);
        break;
      case 'delete':
        if (!confirm(`确定删除目标 ${target.name} 吗？`)) return;
        try {
          await api(`/api/targets/${target.id}`, { method: 'DELETE' });
          toast('目标已删除', 'success');
          await reloadTargets();
        } catch (err) {
          toast(`删除失败: ${err.message}`, 'error');
        }
        break;
      default:
        break;
    }
  });

  $('jobs-body').addEventListener('click', (event) => {
    const button = event.target.closest('button[data-job]');
    if (button) openJob(button.dataset.job);
  });

  $('recent-jobs').addEventListener('click', (event) => {
    const row = event.target.closest('tr[data-job-id]');
    if (row) openJob(row.dataset.jobId);
  });

  document.querySelectorAll('[data-close-modal]').forEach((node) => {
    node.addEventListener('click', closeTargetModal);
  });
  document.querySelectorAll('[data-close-drawer]').forEach((node) => {
    node.addEventListener('click', () => {
      $('drawer-job').classList.add('hidden');
      state.openJobId = null;
    });
  });

  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    if (!$('modal-target').classList.contains('hidden')) closeTargetModal();
    if (!$('drawer-job').classList.contains('hidden')) {
      $('drawer-job').classList.add('hidden');
      state.openJobId = null;
    }
  });
}

document.addEventListener('DOMContentLoaded', () => {
  bindEvents();
  boot();
});