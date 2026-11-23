const state = {
  page: 'dashboard',
  targets: [],
  ipTargets: [],
  cloudTargets: [],
  jobs: [],
  settings: null,
  stats: null,
  version: null,
  editingId: null,
  editingIpId: null,
  editingCloudId: null,
  search: '',
  ipSearch: '',
  cloudSearch: '',
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

const protocolLabels = { snmp: 'SNMP', ipmi: 'IPMI', redfish: 'Redfish', ipscan: 'IP 采集', scan: '设备扫描', cloud: '云资产' };
const modeLabels = { collect: '采集预览', sync: '采集并同步', scan: '批量扫描' };
const cloudProviderLabels = {
  vcenter: 'VMware vCenter',
  aliyun: '阿里云',
  tencent: '腾讯云',
  huawei: '华为云',
  aws: 'AWS',
  gcp: 'Google Cloud',
  volcengine: '火山引擎',
};
const cloudPrefixKindLabels = { vpc: 'VPC', subnet: '子网', switch: '交换机', manual: '手动' };
const triggerLabels = { manual: '手动', schedule: '定时', scan: '扫描' };
const statusLabels = { queued: '排队', running: '运行中', success: '成功', error: '失败' };
const scanStatusLabels = { added: '新增', existing: '已存在', skipped: '跳过', failed: '失败' };

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

function ipTargetTypeCell(target) {
  const parts = [];
  if (target.address) parts.push(`<span class="badge badge-protocol">SNMP</span>`);
  if (String(target.subnets || '').trim()) parts.push(`<span class="badge badge-success">ICMP</span>`);
  return parts.join(' ') || '<span class="muted">—</span>';
}

function ipTargetSubnetsCell(target) {
  const subnets = String(target.subnets || '').split(/[,\s;]+/).filter(Boolean);
  if (subnets.length === 0) return '<span class="muted">仅 ARP/NDP</span>';
  return `<span class="mono">${esc(subnets.join(', '))}</span>`;
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
    targets: ['采集目标', '管理设备地址、凭据、NetBox 归属与批量扫描'],
    iptargets: ['IP 采集', '独立读取 ARP/NDP 表或扫描网段，把 IP 写入 NetBox IPAM'],
    cloudtargets: ['云资产采集', '从 vCenter 与公有云读取 VPC/子网网段、实例 IP 和弹性公网 IP'],
    jobs: ['任务记录', '查看采集日志、硬件摘要、扫描结果与 NetBox 同步结果'],
    settings: ['设置', '配置 NetBox 连接、默认归属和同步行为'],
  };
  const [title, subtitle] = titles[page] || ['netbox-glean', ''];
  $('page-title').textContent = title;
  $('page-subtitle').textContent = subtitle;
  if (page === 'jobs') renderJobs();
  if (page === 'targets') renderTargets();
  if (page === 'iptargets') renderIPTargets();
  if (page === 'cloudtargets') renderCloudTargets();
  if (page === 'dashboard') renderDashboard();
}

async function boot() {
  try {
    const [version, settings, targets, ipTargets, cloudTargets, jobs, stats] = await Promise.all([
      api('/api/version'),
      api('/api/settings'),
      api('/api/targets'),
      api('/api/ip-targets'),
      api('/api/cloud-targets'),
      api('/api/jobs?limit=200'),
      api('/api/stats'),
    ]);
    state.version = version;
    state.settings = settings;
    state.targets = targets;
    state.ipTargets = ipTargets;
    state.cloudTargets = cloudTargets;
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
      if (wantTargets) requests.push(api('/api/targets'), api('/api/ip-targets'), api('/api/cloud-targets'));
      const [jobs, stats, targets, ipTargets, cloudTargets] = await Promise.all(requests);
      state.jobs = jobs;
      state.stats = stats;
      if (targets) {
        state.targets = targets;
        renderTargets();
      }
      if (ipTargets) {
        state.ipTargets = ipTargets;
        renderIPTargets();
      }
      if (cloudTargets) {
        state.cloudTargets = cloudTargets;
        renderCloudTargets();
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
  renderIPTargets();
  renderCloudTargets();
  renderJobs();
}

function renderDashboard() {
  const stats = state.stats || {};
  const jobs = stats.jobs || {};
  $('stat-targets').textContent = stats.targets ?? state.targets.length;
  $('stat-targets-hint').textContent = `启用 ${stats.targets_enabled ?? state.targets.filter((t) => t.enabled).length} 个 · IP 采集 ${stats.ip_targets ?? state.ipTargets.length} 个 · 云资产 ${stats.cloud_targets ?? state.cloudTargets.length} 个`;
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

function renderIPTargets() {
  const search = state.ipSearch.toLowerCase();
  const targets = state.ipTargets.filter((t) => {
    if (!search) return true;
    return `${t.name} ${t.subnets || ''} ${t.address || ''}`.toLowerCase().includes(search);
  });
  const empty = $('ip-targets-empty');
  empty.classList.toggle('hidden', targets.length > 0);
  empty.textContent = state.ipTargets.length === 0
    ? '还没有 IP 采集目标。它可以读取 SNMP 设备的 ARP/NDP 表、扫描 IPv4/IPv6 网段，并把结果写入 NetBox IPAM。'
    : '没有匹配的目标。';

  $('ip-targets-body').innerHTML = targets.map((target) => `
    <tr>
      <td>
        <strong>${esc(target.name)}</strong>
        <div class="muted mono">${target.address ? `${esc(target.address)}:${esc(target.port || 161)}` : ''}</div>
      </td>
      <td>${ipTargetTypeCell(target)}</td>
      <td>${ipTargetSubnetsCell(target)}</td>
      <td>${scheduleCell(target)}</td>
      <td>${target.enabled ? '<span class="badge badge-success">启用</span>' : '<span class="badge badge-queued">停用</span>'}</td>
      <td>
        <div class="actions">
          <button class="btn small" data-ip-action="collect" data-id="${esc(target.id)}">采集</button>
          <button class="btn small primary" data-ip-action="sync" data-id="${esc(target.id)}">同步</button>
          <button class="btn small" data-ip-action="edit" data-id="${esc(target.id)}">编辑</button>
          <button class="btn small danger" data-ip-action="delete" data-id="${esc(target.id)}">删除</button>
        </div>
      </td>
    </tr>
  `).join('');
}

function renderCloudTargets() {
  const search = state.cloudSearch.toLowerCase();
  const targets = state.cloudTargets.filter((t) => {
    if (!search) return true;
    return `${t.name} ${t.provider || ''} ${t.region || ''} ${t.endpoint || ''} ${t.project || ''}`.toLowerCase().includes(search);
  });
  const empty = $('cloud-targets-empty');
  empty.classList.toggle('hidden', targets.length > 0);
  empty.textContent = state.cloudTargets.length === 0
    ? '还没有云资产采集目标。支持 VMware vCenter、阿里云（含国际站）、腾讯云、华为云、AWS、Google Cloud 与火山引擎，可采集 VPC/子网网段、实例 IP 和弹性公网 IP 并写入 NetBox IPAM。'
    : '没有匹配的云资产目标。';

  $('cloud-targets-body').innerHTML = targets.map((target) => `
    <tr>
      <td>
        <strong>${esc(target.name)}</strong>
        <div class="muted mono">${esc(cloudAccountLabel(target))}</div>
      </td>
      <td><span class="badge badge-protocol">${esc(cloudProviderLabels[target.provider] || target.provider)}</span></td>
      <td>${target.region ? `<span class="mono">${esc(target.region)}</span>` : '<span class="muted">—</span>'}</td>
      <td>${cloudTargetCollectCell(target)}</td>
      <td>${scheduleCell(target)}</td>
      <td>${target.enabled ? '<span class="badge badge-success">启用</span>' : '<span class="badge badge-queued">停用</span>'}</td>
      <td>
        <div class="actions">
          <button class="btn small" data-cloud-action="collect" data-id="${esc(target.id)}">采集</button>
          <button class="btn small primary" data-cloud-action="sync" data-id="${esc(target.id)}">同步</button>
          <button class="btn small" data-cloud-action="edit" data-id="${esc(target.id)}">编辑</button>
          <button class="btn small danger" data-cloud-action="delete" data-id="${esc(target.id)}">删除</button>
        </div>
      </td>
    </tr>
  `).join('');
}

function cloudAccountLabel(target) {
  const parts = [];
  if (target.project) parts.push(target.project);
  if (target.endpoint) parts.push(target.endpoint);
  if (!parts.length && target.access_key) parts.push(target.access_key);
  return parts.join(' · ') || '默认 Endpoint';
}

function cloudTargetCollectCell(target) {
  const parts = [];
  if (target.collect_prefixes) parts.push('网段');
  if (target.collect_instances) parts.push('实例 IP');
  if (target.collect_public_ips) parts.push('公网 IP');
  if (String(target.extra_subnets || '').trim()) parts.push('附加网段');
  return parts.length ? parts.map((p) => `<span class="badge badge-queued">${esc(p)}</span>`).join(' ') : '<span class="muted">—</span>';
}

function renderJobs() {
  $('jobs-summary').textContent = `共 ${state.jobs.length} 条任务记录，保留在内存中，重启后清空。`;
  $('jobs-empty').classList.toggle('hidden', state.jobs.length > 0);
  $('jobs-body').innerHTML = state.jobs.map((job) => {
    const isScan = job.mode === 'scan';
    const isCloud = job.protocol === 'cloud';
    return `
    <tr>
      <td><strong>${esc(job.target_name)}</strong><div class="muted mono">${esc(job.id)}</div></td>
      <td>${protocolBadge(job.protocol)}</td>
      <td>${esc(modeLabels[job.mode] || job.mode)}${job.trigger === 'schedule' ? ' · 定时' : ''}</td>
      <td>${statusBadge(job.status)}${job.error ? `<div class="muted">${esc(job.error.slice(0, 80))}</div>` : ''}</td>
      <td>${isScan ? `存活 ${job.found || 0}` : isCloud ? `地址 ${job.found || 0}` : job.interfaces}</td>
      <td>${isScan ? `新增 ${job.added || 0}` : isCloud ? `网段 ${job.added || 0}` : job.components}</td>
      <td class="muted">${esc(fmtTime(job.started_at || job.created_at))}</td>
      <td class="muted">${esc(fmtDuration(job))}</td>
      <td class="right"><button class="btn small" data-job="${esc(job.id)}">查看</button></td>
    </tr>
  `;
  }).join('');
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
  setChecked('set-sync-cloud-assets', settings.sync_cloud_assets);
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

async function reloadIPTargets() {
  state.ipTargets = await api('/api/ip-targets');
  renderIPTargets();
  await reloadStats();
}

async function collectIPTarget(target, sync) {
  const verb = sync ? '采集并同步' : '采集';
  if (sync && !confirm(`确定要采集 ${target.name} 并同步到 NetBox IPAM 吗？`)) return;
  try {
    await api(`/api/ip-targets/${target.id}/collect`, { method: 'POST', body: { sync } });
    toast(`已提交${verb}任务: ${target.name}`, 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(`${verb}失败: ${err.message}`, 'error');
  }
}

async function collectAllIPs(sync) {
  if (sync && !confirm('确定要采集所有启用的 IP 采集目标并同步到 NetBox 吗？')) return;
  try {
    const result = await api('/api/ip-collect', { method: 'POST', body: { sync } });
    toast(`已提交 ${result.count} 个 IP 采集任务`, 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(`提交失败: ${err.message}`, 'error');
  }
}

async function reloadCloudTargets() {
  state.cloudTargets = await api('/api/cloud-targets');
  renderCloudTargets();
  await reloadStats();
}

async function collectCloudTarget(target, sync) {
  const verb = sync ? '采集并同步' : '采集';
  if (sync && !confirm(`确定要采集 ${target.name} 并同步到 NetBox IPAM 吗？`)) return;
  try {
    await api(`/api/cloud-targets/${target.id}/collect`, { method: 'POST', body: { sync } });
    toast(`已提交${verb}任务: ${target.name}`, 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(`${verb}失败: ${err.message}`, 'error');
  }
}

async function collectAllCloud(sync) {
  if (sync && !confirm('确定要采集所有启用的云资产目标并同步到 NetBox 吗？')) return;
  try {
    const result = await api('/api/cloud-collect', { method: 'POST', body: { sync } });
    toast(`已提交 ${result.count} 个云资产采集任务`, 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(`提交失败: ${err.message}`, 'error');
  }
}

function openCloudTargetModal(target) {
  state.editingCloudId = target ? target.id : null;
  $('cloud-target-modal-title').textContent = target ? `编辑云账号 · ${target.name}` : '新增云账号';
  setVal('ct-name', target ? target.name : '');
  setVal('ct-provider', target ? target.provider : 'aliyun');
  setChecked('ct-enabled', target ? target.enabled : true);
  setVal('ct-endpoint', target ? target.endpoint : '');
  setVal('ct-region', target ? target.region : '');
  setVal('ct-project', target ? target.project : '');
  setVal('ct-access', target ? target.access_key : '');
  setVal('ct-secret', '');
  setVal('ct-credentials', '');
  setChecked('ct-insecure', target ? Boolean(target.insecure) : false);
  setChecked('ct-collect-prefixes', target ? target.collect_prefixes : true);
  setChecked('ct-collect-instances', target ? target.collect_instances : true);
  setChecked('ct-collect-public', target ? target.collect_public_ips : true);
  setVal('ct-extra-subnets', target ? target.extra_subnets : '');
  setVal('ct-site', target ? target.site : '');
  setChecked('ct-schedule-enabled', target ? Boolean(target.schedule_enabled) : false);
  setVal('ct-schedule-interval', target && target.schedule_interval_minutes ? target.schedule_interval_minutes : 60);
  toggleCloudProviderFields(target ? target.provider : 'aliyun');
  $('modal-cloudtarget').classList.remove('hidden');
  $('ct-name').focus();
}

function closeCloudTargetModal() {
  $('modal-cloudtarget').classList.add('hidden');
  state.editingCloudId = null;
}

// toggleCloudProviderFields adapts the credential fields to the selected
// provider. vCenter uses username/password, GCP uses a service account JSON
// and Huawei Cloud needs a project id in addition to the region.
function toggleCloudProviderFields(provider) {
  const show = (selector, visible) => {
    document.querySelectorAll(selector).forEach((node) => node.classList.toggle('hidden', !visible));
  };
  const isVCenter = provider === 'vcenter';
  const isGCP = provider === 'gcp';
  const isHuawei = provider === 'huawei';
  show('.cloud-region', !isVCenter);
  show('.cloud-project', isHuawei || isGCP);
  show('.cloud-access', !isGCP);
  show('.cloud-secret', !isGCP);
  show('.cloud-credentials', isGCP);
  show('.cloud-insecure', isVCenter);
  $('ct-endpoint-label').textContent = isVCenter ? 'API Endpoint（必填）' : 'API Endpoint（可选）';
  $('ct-project-label').textContent = isGCP ? 'Project ID' : 'Project ID（华为云）';
  $('ct-access-label').textContent = isVCenter ? '用户名' : 'AccessKey / SecretId';
  $('ct-secret-label').textContent = isVCenter ? '密码' : 'SecretKey / Secret';
}

async function saveCloudTarget(event) {
  event.preventDefault();
  const target = {
    name: val('ct-name'),
    provider: val('ct-provider'),
    enabled: checked('ct-enabled'),
    endpoint: val('ct-endpoint'),
    region: val('ct-region'),
    project: val('ct-project'),
    access_key: val('ct-access'),
    secret_key: val('ct-secret'),
    credentials: val('ct-credentials'),
    insecure: checked('ct-insecure'),
    collect_prefixes: checked('ct-collect-prefixes'),
    collect_instances: checked('ct-collect-instances'),
    collect_public_ips: checked('ct-collect-public'),
    extra_subnets: val('ct-extra-subnets'),
    site: val('ct-site'),
    schedule_enabled: checked('ct-schedule-enabled'),
    schedule_interval_minutes: Number.parseInt(val('ct-schedule-interval'), 10) || 60,
  };
  try {
    if (state.editingCloudId) {
      await api(`/api/cloud-targets/${state.editingCloudId}`, { method: 'PUT', body: target });
      toast('云资产目标已更新', 'success');
    } else {
      await api('/api/cloud-targets', { method: 'POST', body: target });
      toast('云资产目标已创建', 'success');
    }
    closeCloudTargetModal();
    await reloadCloudTargets();
  } catch (err) {
    toast(`保存失败: ${err.message}`, 'error');
  }
}

function openIPTargetModal(target) {
  state.editingIpId = target ? target.id : null;
  $('ip-target-modal-title').textContent = target ? `编辑 IP 采集 · ${target.name}` : '新增 IP 采集';
  const snmp = (target && target.snmp) || { version: 'v2c', v3_auth_protocol: 'SHA256', v3_priv_protocol: 'AES' };
  setVal('it-name', target ? target.name : '');
  setChecked('it-enabled', target ? target.enabled : true);
  setVal('it-subnets', target ? target.subnets : '');
  setVal('it-source-address', target ? target.address : '');
  setVal('it-source-port', target && target.port ? target.port : 161);
  setChecked('it-schedule-enabled', target ? Boolean(target.schedule_enabled) : false);
  setVal('it-schedule-interval', target && target.schedule_interval_minutes ? target.schedule_interval_minutes : 60);

  setVal('is-version', snmp.version || 'v2c');
  setVal('is-community', '');
  setVal('is-v3-user', snmp.v3_username || '');
  setVal('is-v3-auth-proto', snmp.v3_auth_protocol || 'SHA256');
  setVal('is-v3-auth-pass', '');
  setVal('is-v3-priv-proto', snmp.v3_priv_protocol || 'AES');
  setVal('is-v3-priv-pass', '');

  $('modal-iptarget').classList.remove('hidden');
  $('it-name').focus();
}

function closeIPTargetModal() {
  $('modal-iptarget').classList.add('hidden');
  state.editingIpId = null;
}

async function saveIPTarget(event) {
  event.preventDefault();
  const target = {
    name: val('it-name'),
    enabled: checked('it-enabled'),
    subnets: val('it-subnets'),
    address: val('it-source-address'),
    port: Number.parseInt(val('it-source-port'), 10) || 0,
    schedule_enabled: checked('it-schedule-enabled'),
    schedule_interval_minutes: Number.parseInt(val('it-schedule-interval'), 10) || 60,
    snmp: {
      version: val('is-version'),
      community: val('is-community'),
      v3_username: val('is-v3-user'),
      v3_auth_protocol: val('is-v3-auth-proto'),
      v3_auth_password: val('is-v3-auth-pass'),
      v3_priv_protocol: val('is-v3-priv-proto'),
      v3_priv_password: val('is-v3-priv-pass'),
    },
  };
  try {
    if (state.editingIpId) {
      await api(`/api/ip-targets/${state.editingIpId}`, { method: 'PUT', body: target });
      toast('IP 采集目标已更新', 'success');
    } else {
      await api('/api/ip-targets', { method: 'POST', body: target });
      toast('IP 采集目标已创建', 'success');
    }
    closeIPTargetModal();
    await reloadIPTargets();
  } catch (err) {
    toast(`保存失败: ${err.message}`, 'error');
  }
}

function toggleScanProtocolFields(protocol) {
  document.querySelectorAll('.scan-proto').forEach((node) => node.classList.add('hidden'));
  document.querySelectorAll(`.scan-proto-${protocol}`).forEach((node) => node.classList.remove('hidden'));
}

function openScanModal() {
  setVal('sc-subnets', '');
  setVal('sc-protocol', 'snmp');
  setVal('sc-port', 161);
  setVal('sc-site', '');
  setVal('sc-role', '');
  setVal('sc-platform', '');
  setVal('sc-name-prefix', '');
  setChecked('sc-enabled', true);
  setChecked('sc-sync', false);
  setVal('sc-s-version', 'v2c');
  setVal('sc-s-community', '');
  setVal('sc-s-v3-user', '');
  setVal('sc-s-v3-auth-proto', 'SHA256');
  setVal('sc-s-v3-auth-pass', '');
  setVal('sc-s-v3-priv-proto', 'AES');
  setVal('sc-s-v3-priv-pass', '');
  setVal('sc-i-user', '');
  setVal('sc-i-pass', '');
  setVal('sc-i-interface', 'lanplus');
  setVal('sc-r-user', '');
  setVal('sc-r-pass', '');
  setVal('sc-r-system', '');
  setChecked('sc-r-insecure', false);
  toggleScanProtocolFields('snmp');
  $('modal-scan').classList.remove('hidden');
  $('sc-subnets').focus();
}

function closeScanModal() {
  $('modal-scan').classList.add('hidden');
}

async function saveScan(event) {
  event.preventDefault();
  const request = {
    subnets: val('sc-subnets'),
    protocol: val('sc-protocol'),
    port: Number.parseInt(val('sc-port'), 10) || 0,
    site: val('sc-site'),
    device_role: val('sc-role'),
    platform: val('sc-platform'),
    name_prefix: val('sc-name-prefix'),
    enabled: checked('sc-enabled'),
    sync: checked('sc-sync'),
    snmp: {
      version: val('sc-s-version'),
      community: val('sc-s-community'),
      v3_username: val('sc-s-v3-user'),
      v3_auth_protocol: val('sc-s-v3-auth-proto'),
      v3_auth_password: val('sc-s-v3-auth-pass'),
      v3_priv_protocol: val('sc-s-v3-priv-proto'),
      v3_priv_password: val('sc-s-v3-priv-pass'),
    },
    ipmi: {
      username: val('sc-i-user'),
      password: val('sc-i-pass'),
      interface: val('sc-i-interface'),
    },
    redfish: {
      username: val('sc-r-user'),
      password: val('sc-r-pass'),
      insecure: checked('sc-r-insecure'),
      system_id: val('sc-r-system'),
    },
  };
  try {
    await api('/api/scan', { method: 'POST', body: request });
    toast('扫描任务已提交', 'success');
    closeScanModal();
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(`提交扫描失败: ${err.message}`, 'error');
  }
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
    sync_cloud_assets: checked('set-sync-cloud-assets'),
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
  const scan = job.scan || null;
  const cloud = job.cloud || null;
  const identity = result ? (result.identity || {}) : {};
  const isDeviceResult = Boolean(result) && job.protocol !== 'ipscan' && job.protocol !== 'cloud' && job.mode !== 'scan';

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

  const identityBlock = isDeviceResult ? `
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
    </dl>` : (job.mode === 'scan' || job.protocol === 'ipscan' || job.protocol === 'cloud' ? '' : '<p class="muted">尚无采集结果。</p>');

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
  if (cloud && Array.isArray(cloud.warnings)) warnings.push(...cloud.warnings);
  if (sync && Array.isArray(sync.warnings)) warnings.push(...sync.warnings);
  const warningBlock = warnings.length
    ? `<h3 class="section-title">警告</h3><ul class="list warn-list">${warnings.map((w) => `<li>${esc(w)}</li>`).join('')}</ul>`
    : '';

  const deviceSync = Boolean(sync && (sync.device_id || sync.device_name));
  const syncBlock = sync ? `
    <h3 class="section-title">NetBox 同步结果</h3>
    <dl class="kv">
      ${deviceSync ? `<dt>设备</dt><dd>${esc(sync.device_name || '—')} (#${sync.device_id}) ${sync.device_url ? `· <a href="${esc(sync.device_url)}" target="_blank" rel="noreferrer">打开</a>` : ''}</dd>` : ''}
      ${deviceSync ? `<dt>接口</dt><dd>新建 ${sync.interfaces_created} / 更新 ${sync.interfaces_updated}</dd>` : ''}
      ${deviceSync ? `<dt>LLDP 连线</dt><dd>新建 ${sync.cables_created || 0} / 跳过 ${sync.cables_skipped || 0}</dd>` : ''}
      <dt>IP 地址</dt><dd>新建 ${sync.ip_created || 0} / 分配 ${sync.ip_assigned || 0} / 跳过 ${sync.ip_skipped || 0}</dd>
      ${(sync.prefix_created || sync.prefix_skipped) ? `<dt>网段</dt><dd>新建 ${sync.prefix_created || 0} / 已存在 ${sync.prefix_skipped || 0}</dd>` : ''}
      ${deviceSync ? `<dt>硬件清单</dt><dd>新建 ${sync.inventory_created} / 更新 ${sync.inventory_updated}</dd>` : ''}
      ${sync.ip_address ? `<dt>主 IP</dt><dd class="mono">${esc(sync.ip_address)}</dd>` : ''}
    </dl>
    ${Array.isArray(sync.actions) && sync.actions.length ? `<ul class="list">${sync.actions.map((a) => `<li>${esc(a)}</li>`).join('')}</ul>` : ''}
  ` : '';

  const scanStatsBlock = scan ? `
    <h3 class="section-title">扫描结果</h3>
    <dl class="kv">
      <dt>网段 / 协议</dt><dd class="mono">${esc(scan.subnets || '—')} · ${esc(protocolLabels[scan.protocol] || scan.protocol || '—')}</dd>
      <dt>扫描 / 存活</dt><dd>${scan.scanned} / ${scan.alive}</dd>
      <dt>新增 / 已存在</dt><dd>${scan.added} / ${scan.existing}</dd>
      <dt>跳过 / 失败</dt><dd>${scan.skipped} / ${scan.failed}</dd>
    </dl>
    ${Array.isArray(scan.warnings) && scan.warnings.length ? `<ul class="list warn-list">${scan.warnings.map((w) => `<li>${esc(w)}</li>`).join('')}</ul>` : ''}
    ${Array.isArray(scan.devices) && scan.devices.length ? `
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>地址</th><th>名称</th><th>厂商 / 型号</th><th>状态</th><th>备注</th></tr></thead>
        <tbody>${scan.devices.map((d) => `<tr>
          <td class="mono">${esc(d.address || '—')}</td>
          <td>${esc(d.name || '—')}</td>
          <td>${esc(d.manufacturer || '—')} / ${esc(d.model || '—')}</td>
          <td><span class="badge badge-${esc(d.status === 'added' ? 'success' : d.status === 'failed' ? 'error' : 'queued')}">${esc(scanStatusLabels[d.status] || d.status)}</span></td>
          <td class="muted">${esc(d.error || '')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>` : ''}
  ` : '';

  const prefixList = cloud && Array.isArray(cloud.prefixes) ? cloud.prefixes : [];
  const addressList = cloud && Array.isArray(cloud.addresses) ? cloud.addresses : [];
  const cloudBlock = cloud ? `
    <h3 class="section-title">云资产采集结果</h3>
    <dl class="kv">
      <dt>云平台</dt><dd>${esc(cloudProviderLabels[cloud.provider] || cloud.provider || '—')}</dd>
      <dt>VPC / 子网</dt><dd>${cloud.vpcs || 0} / ${cloud.subnets || 0}</dd>
      <dt>实例 / 公网 IP</dt><dd>${cloud.instances || 0} / ${cloud.public_ips || 0}</dd>
      <dt>网段 / 地址</dt><dd>${prefixList.length} / ${addressList.length}</dd>
    </dl>
    ${prefixList.length ? `
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>网段</th><th>类型</th><th>名称</th><th>VPC</th><th>区域</th></tr></thead>
        <tbody>${prefixList.slice(0, 300).map((p) => `<tr>
          <td class="mono">${esc(p.cidr || '—')}</td>
          <td>${esc(cloudPrefixKindLabels[p.kind] || p.kind || '—')}</td>
          <td>${esc(p.name || '—')}</td>
          <td class="mono">${esc(p.vpc || '—')}</td>
          <td class="muted">${esc(p.region || '—')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>
    ${prefixList.length > 300 ? `<p class="muted">仅显示前 300 个网段，共 ${prefixList.length} 个。</p>` : ''}` : ''}
    ${addressList.length ? `
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>地址</th><th>类型</th><th>名称 / 实例</th><th>接口 / MAC</th><th>区域</th></tr></thead>
        <tbody>${addressList.slice(0, 300).map((a) => `<tr>
          <td class="mono">${esc(a.address || '—')}</td>
          <td>${a.public ? '<span class="badge badge-success">公网</span>' : '<span class="badge badge-queued">实例</span>'}</td>
          <td>${esc(a.name || '—')}${a.instance_id ? `<div class="muted mono">${esc(a.instance_id)}</div>` : ''}</td>
          <td class="mono">${esc(a.interface || '—')}${a.mac ? ` / ${esc(a.mac)}` : ''}</td>
          <td class="muted">${esc(a.region || '—')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>
    ${addressList.length > 300 ? `<p class="muted">仅显示前 300 个地址，共 ${addressList.length} 个。</p>` : ''}` : ''}
  ` : '';

  const log = Array.isArray(job.log) && job.log.length
    ? job.log.join('\n')
    : '(no log)';

  $('job-body').innerHTML = `
    <div class="job-status-head">${statusBadge(job.status)}${job.error ? '<span class="badge badge-error">查看错误</span>' : ''}</div>
    ${kv}
    ${syncBlock}
    ${scanStatsBlock}
    ${cloudBlock}
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
  $('btn-scan').addEventListener('click', openScanModal);
  $('btn-scan-targets').addEventListener('click', openScanModal);
  $('btn-add-ip-target').addEventListener('click', () => openIPTargetModal(null));
  $('btn-ip-collect-all').addEventListener('click', () => collectAllIPs(false));
  $('btn-ip-sync-all').addEventListener('click', () => collectAllIPs(true));
  $('btn-add-cloud-target').addEventListener('click', () => openCloudTargetModal(null));
  $('btn-cloud-collect-all').addEventListener('click', () => collectAllCloud(false));
  $('btn-cloud-sync-all').addEventListener('click', () => collectAllCloud(true));
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

  $('ip-target-search').addEventListener('input', (event) => {
    state.ipSearch = event.target.value.trim();
    renderIPTargets();
  });

  $('cloud-target-search').addEventListener('input', (event) => {
    state.cloudSearch = event.target.value.trim();
    renderCloudTargets();
  });

  $('t-protocol').addEventListener('change', (event) => {
    const defaults = { snmp: 161, ipmi: 623, redfish: 443 };
    toggleProtocolFields(event.target.value);
    setVal('t-port', defaults[event.target.value] || '');
  });

  $('sc-protocol').addEventListener('change', (event) => {
    const defaults = { snmp: 161, ipmi: 623, redfish: 443 };
    toggleScanProtocolFields(event.target.value);
    setVal('sc-port', defaults[event.target.value] || '');
  });

  $('ct-provider').addEventListener('change', (event) => {
    toggleCloudProviderFields(event.target.value);
  });

  $('target-form').addEventListener('submit', saveTarget);
  $('ip-target-form').addEventListener('submit', saveIPTarget);
  $('cloud-target-form').addEventListener('submit', saveCloudTarget);
  $('scan-form').addEventListener('submit', saveScan);
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

  $('ip-targets-body').addEventListener('click', async (event) => {
    const button = event.target.closest('button[data-ip-action]');
    if (!button) return;
    const target = state.ipTargets.find((item) => item.id === button.dataset.id);
    if (!target) return;
    switch (button.dataset.ipAction) {
      case 'collect':
        await collectIPTarget(target, false);
        break;
      case 'sync':
        await collectIPTarget(target, true);
        break;
      case 'edit':
        openIPTargetModal(target);
        break;
      case 'delete':
        if (!confirm(`确定删除 IP 采集目标 ${target.name} 吗？`)) return;
        try {
          await api(`/api/ip-targets/${target.id}`, { method: 'DELETE' });
          toast('IP 采集目标已删除', 'success');
          await reloadIPTargets();
        } catch (err) {
          toast(`删除失败: ${err.message}`, 'error');
        }
        break;
      default:
        break;
    }
  });

  $('cloud-targets-body').addEventListener('click', async (event) => {
    const button = event.target.closest('button[data-cloud-action]');
    if (!button) return;
    const target = state.cloudTargets.find((item) => item.id === button.dataset.id);
    if (!target) return;
    switch (button.dataset.cloudAction) {
      case 'collect':
        await collectCloudTarget(target, false);
        break;
      case 'sync':
        await collectCloudTarget(target, true);
        break;
      case 'edit':
        openCloudTargetModal(target);
        break;
      case 'delete':
        if (!confirm(`确定删除云资产目标 ${target.name} 吗？`)) return;
        try {
          await api(`/api/cloud-targets/${target.id}`, { method: 'DELETE' });
          toast('云资产目标已删除', 'success');
          await reloadCloudTargets();
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
    node.addEventListener('click', () => {
      closeTargetModal();
      closeIPTargetModal();
      closeCloudTargetModal();
      closeScanModal();
    });
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
    if (!$('modal-iptarget').classList.contains('hidden')) closeIPTargetModal();
    if (!$('modal-cloudtarget').classList.contains('hidden')) closeCloudTargetModal();
    if (!$('modal-scan').classList.contains('hidden')) closeScanModal();
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