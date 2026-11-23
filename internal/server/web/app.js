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

function protocolLabel(protocol) {
  const labels = { snmp: 'SNMP', ipmi: 'IPMI', redfish: 'Redfish', ipscan: t('protocol.ipscan'), scan: t('protocol.scan'), cloud: t('protocol.cloud') };
  return labels[protocol] || protocol;
}

function modeLabel(mode) {
  const labels = { collect: t('mode.collect'), sync: t('mode.sync'), scan: t('mode.scan') };
  return labels[mode] || mode;
}

function cloudProviderLabel(provider) {
  const labels = {
    vcenter: t('provider.vcenter'),
    aliyun: t('provider.aliyun'),
    tencent: t('provider.tencent'),
    huawei: t('provider.huawei'),
    aws: t('provider.aws'),
    gcp: t('provider.gcp'),
    volcengine: t('provider.volcengine'),
  };
  return labels[provider] || provider;
}

function cloudPrefixKindLabel(kind) {
  const labels = { vpc: 'VPC', subnet: t('kind.subnet'), switch: t('kind.switch'), manual: t('kind.manual') };
  return labels[kind] || kind;
}

function triggerLabel(trigger) {
  const labels = { manual: t('trigger.manual'), schedule: t('trigger.schedule'), scan: t('trigger.scan') };
  return labels[trigger] || t('trigger.manual');
}

function statusLabel(status) {
  const labels = { queued: t('status.queued'), running: t('status.running'), success: t('status.success'), error: t('status.error') };
  return labels[status] || status;
}

function scanStatusLabel(status) {
  const labels = { added: t('scanStatus.added'), existing: t('scanStatus.existing'), skipped: t('scanStatus.skipped'), failed: t('scanStatus.failed') };
  return labels[status] || status;
}

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
  return date.toLocaleString(currentLang() === 'zh' ? 'zh-CN' : 'en-US', { hour12: false });
}

function fmtDuration(job) {
  if (!job || !job.started_at) return '—';
  if (!job.finished_at) return job.status === 'running' ? t('duration.running') : '—';
  const ms = new Date(job.finished_at) - new Date(job.started_at);
  if (Number.isNaN(ms) || ms < 0) return '—';
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60000).toFixed(1)} min`;
}

function minutesLabel(minutes) {
  const value = Number(minutes) || 60;
  if (value % 1440 === 0) return t('duration.days', { n: value / 1440 });
  if (value % 60 === 0) return t('duration.hours', { n: value / 60 });
  return t('duration.minutes', { n: value });
}

function scheduleCell(target) {
  if (!target.schedule_enabled) return `<span class="muted">${t('schedule.off')}</span>`;
  const last = target.last_run_at
    ? `<div class="muted">${t('schedule.last', { time: esc(fmtTime(target.last_run_at)) })}</div>`
    : `<div class="muted">${t('schedule.waiting')}</div>`;
  return `<span class="badge badge-success">${t('schedule.every', { interval: esc(minutesLabel(target.schedule_interval_minutes)) })}</span>${last}`;
}

function ipTargetTypeCell(target) {
  const parts = [];
  if (target.address) parts.push(`<span class="badge badge-protocol">SNMP</span>`);
  if (String(target.subnets || '').trim()) parts.push(`<span class="badge badge-success">ICMP</span>`);
  return parts.join(' ') || '<span class="muted">—</span>';
}

function ipTargetSubnetsCell(target) {
  const subnets = String(target.subnets || '').split(/[,\s;]+/).filter(Boolean);
  if (subnets.length === 0) return `<span class="muted">${t('ip.arpOnly')}</span>`;
  return `<span class="mono">${esc(subnets.join(', '))}</span>`;
}

function statusBadge(status) {
  return `<span class="badge badge-${esc(status)}">${esc(statusLabel(status))}</span>`;
}

function protocolBadge(protocol) {
  return `<span class="badge badge-protocol">${esc(protocolLabel(protocol))}</span>`;
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
    dashboard: [t('page.dashboard.title'), t('page.dashboard.subtitle')],
    targets: [t('page.targets.title'), t('page.targets.subtitle')],
    iptargets: [t('page.iptargets.title'), t('page.iptargets.subtitle')],
    cloudtargets: [t('page.cloudtargets.title'), t('page.cloudtargets.subtitle')],
    jobs: [t('page.jobs.title'), t('page.jobs.subtitle')],
    settings: [t('page.settings.title'), t('page.settings.subtitle')],
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
    toast(t('toast.loadFailed', { msg: err.message }), 'error');
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
  $('stat-targets-hint').textContent = t('stat.targetsHint', {
    enabled: stats.targets_enabled ?? state.targets.filter((x) => x.enabled).length,
    ip: stats.ip_targets ?? state.ipTargets.length,
    cloud: stats.cloud_targets ?? state.cloudTargets.length,
  });
  $('stat-running').textContent = jobs.running ?? 0;
  $('stat-queued').textContent = t('stat.queuedHint', { n: jobs.queued ?? 0 });
  $('stat-success').textContent = jobs.success ?? 0;
  $('stat-error').textContent = jobs.error ?? 0;

  const byProtocol = stats.by_protocol || {};
  const rows = ['snmp', 'ipmi', 'redfish'].map((protocol) => {
    const count = byProtocol[protocol] || 0;
    const total = Math.max(1, ...['snmp', 'ipmi', 'redfish'].map((p) => byProtocol[p] || 0));
    return `<div class="protocol-row">
      <span class="badge badge-protocol">${protocolLabel(protocol)}</span>
      <span class="bar"><span style="width:${Math.round((count / total) * 100)}%"></span></span>
      <strong>${count}</strong>
    </div>`;
  }).join('');
  $('protocol-list').innerHTML = rows;

  const settings = state.settings || {};
  const stateText = stats.netbox_configured
    ? `NetBox: ${esc(settings.netbox_url || t('netbox.configuredLabel'))}${settings.site ? ` · site <span class="mono">${esc(settings.site)}</span>` : t('netbox.noSite')}`
    : t('netbox.notConfigured');
  $('netbox-state').innerHTML = stateText;

  const recent = state.jobs.slice(0, 8);
  $('recent-empty').classList.toggle('hidden', recent.length > 0);
  $('recent-jobs').innerHTML = recent.map((job) => `
    <tr data-job-id="${esc(job.id)}" style="cursor:pointer">
      <td>${esc(job.target_name)}<div class="muted mono">${esc(job.id)}</div></td>
      <td>${esc(modeLabel(job.mode))}${job.trigger === 'schedule' ? t('trigger.scheduledSuffix') : ''}</td>
      <td>${statusBadge(job.status)}</td>
      <td class="muted">${esc(fmtTime(job.created_at))}</td>
    </tr>
  `).join('');
}

function renderTargets() {
  const search = state.search.toLowerCase();
  const targets = state.targets.filter((target) => {
    if (!search) return true;
    return `${target.name} ${target.address} ${target.device_name || ''}`.toLowerCase().includes(search);
  });
  const empty = $('targets-empty');
  empty.classList.toggle('hidden', targets.length > 0);
  empty.textContent = state.targets.length === 0 ? t('targets.empty') : t('targets.noMatch');

  $('targets-body').innerHTML = targets.map((target) => `
    <tr>
      <td>
        <strong>${esc(target.name)}</strong>
        <div class="muted">${esc(target.description || '')}</div>
      </td>
      <td>${protocolBadge(target.protocol)}</td>
      <td class="mono">${esc(target.address)}:${esc(target.port)}</td>
      <td>${target.site ? `<span class="mono">${esc(target.site)}</span>` : `<span class="muted">${t('cell.default')}</span>`}</td>
      <td>${target.device_name ? esc(target.device_name) : `<span class="muted">${t('cell.auto')}</span>`}</td>
      <td>${scheduleCell(target)}</td>
      <td>${target.enabled ? `<span class="badge badge-success">${t('badge.enabled')}</span>` : `<span class="badge badge-queued">${t('badge.disabled')}</span>`}</td>
      <td>
        <div class="actions">
          <button class="btn small" data-action="collect" data-id="${esc(target.id)}">${t('action.collect')}</button>
          <button class="btn small primary" data-action="sync" data-id="${esc(target.id)}">${t('action.sync')}</button>
          <button class="btn small" data-action="edit" data-id="${esc(target.id)}">${t('action.edit')}</button>
          <button class="btn small danger" data-action="delete" data-id="${esc(target.id)}">${t('action.delete')}</button>
        </div>
      </td>
    </tr>
  `).join('');
}

function renderIPTargets() {
  const search = state.ipSearch.toLowerCase();
  const targets = state.ipTargets.filter((target) => {
    if (!search) return true;
    return `${target.name} ${target.subnets || ''} ${target.address || ''}`.toLowerCase().includes(search);
  });
  const empty = $('ip-targets-empty');
  empty.classList.toggle('hidden', targets.length > 0);
  empty.textContent = state.ipTargets.length === 0 ? t('ip.empty') : t('targets.noMatch');

  $('ip-targets-body').innerHTML = targets.map((target) => `
    <tr>
      <td>
        <strong>${esc(target.name)}</strong>
        <div class="muted mono">${target.address ? `${esc(target.address)}:${esc(target.port || 161)}` : ''}</div>
      </td>
      <td>${ipTargetTypeCell(target)}</td>
      <td>${ipTargetSubnetsCell(target)}</td>
      <td>${scheduleCell(target)}</td>
      <td>${target.enabled ? `<span class="badge badge-success">${t('badge.enabled')}</span>` : `<span class="badge badge-queued">${t('badge.disabled')}</span>`}</td>
      <td>
        <div class="actions">
          <button class="btn small" data-ip-action="collect" data-id="${esc(target.id)}">${t('action.collect')}</button>
          <button class="btn small primary" data-ip-action="sync" data-id="${esc(target.id)}">${t('action.sync')}</button>
          <button class="btn small" data-ip-action="edit" data-id="${esc(target.id)}">${t('action.edit')}</button>
          <button class="btn small danger" data-ip-action="delete" data-id="${esc(target.id)}">${t('action.delete')}</button>
        </div>
      </td>
    </tr>
  `).join('');
}

function renderCloudTargets() {
  const search = state.cloudSearch.toLowerCase();
  const targets = state.cloudTargets.filter((target) => {
    if (!search) return true;
    return `${target.name} ${target.provider || ''} ${target.region || ''} ${target.endpoint || ''} ${target.project || ''}`.toLowerCase().includes(search);
  });
  const empty = $('cloud-targets-empty');
  empty.classList.toggle('hidden', targets.length > 0);
  empty.textContent = state.cloudTargets.length === 0 ? t('cloud.empty') : t('cloud.noMatch');

  $('cloud-targets-body').innerHTML = targets.map((target) => `
    <tr>
      <td>
        <strong>${esc(target.name)}</strong>
        <div class="muted mono">${esc(cloudAccountLabel(target))}</div>
      </td>
      <td><span class="badge badge-protocol">${esc(cloudProviderLabel(target.provider))}</span></td>
      <td>${target.region ? `<span class="mono">${esc(target.region)}</span>` : '<span class="muted">—</span>'}</td>
      <td>${cloudTargetCollectCell(target)}</td>
      <td>${scheduleCell(target)}</td>
      <td>${target.enabled ? `<span class="badge badge-success">${t('badge.enabled')}</span>` : `<span class="badge badge-queued">${t('badge.disabled')}</span>`}</td>
      <td>
        <div class="actions">
          <button class="btn small" data-cloud-action="collect" data-id="${esc(target.id)}">${t('action.collect')}</button>
          <button class="btn small primary" data-cloud-action="sync" data-id="${esc(target.id)}">${t('action.sync')}</button>
          <button class="btn small" data-cloud-action="edit" data-id="${esc(target.id)}">${t('action.edit')}</button>
          <button class="btn small danger" data-cloud-action="delete" data-id="${esc(target.id)}">${t('action.delete')}</button>
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
  return parts.join(' · ') || t('cloud.defaultEndpoint');
}

function cloudTargetCollectCell(target) {
  const parts = [];
  if (target.collect_prefixes) parts.push(t('collect.prefixes'));
  if (target.collect_instances) parts.push(t('collect.instances'));
  if (target.collect_public_ips) parts.push(t('collect.public'));
  if (String(target.extra_subnets || '').trim()) parts.push(t('collect.extra'));
  return parts.length ? parts.map((p) => `<span class="badge badge-queued">${esc(p)}</span>`).join(' ') : '<span class="muted">—</span>';
}

function renderJobs() {
  $('jobs-summary').textContent = t('jobs.summary', { n: state.jobs.length });
  $('jobs-empty').classList.toggle('hidden', state.jobs.length > 0);
  $('jobs-body').innerHTML = state.jobs.map((job) => {
    const isScan = job.mode === 'scan';
    const isCloud = job.protocol === 'cloud';
    return `
    <tr>
      <td><strong>${esc(job.target_name)}</strong><div class="muted mono">${esc(job.id)}</div></td>
      <td>${protocolBadge(job.protocol)}</td>
      <td>${esc(modeLabel(job.mode))}${job.trigger === 'schedule' ? t('trigger.scheduledSuffix') : ''}</td>
      <td>${statusBadge(job.status)}${job.error ? `<div class="muted">${esc(job.error.slice(0, 80))}</div>` : ''}</td>
      <td>${isScan ? t('jobs.alive', { n: job.found || 0 }) : isCloud ? t('jobs.addresses', { n: job.found || 0 }) : job.interfaces}</td>
      <td>${isScan ? t('jobs.added', { n: job.added || 0 }) : isCloud ? t('jobs.prefixes', { n: job.added || 0 }) : job.components}</td>
      <td class="muted">${esc(fmtTime(job.started_at || job.created_at))}</td>
      <td class="muted">${esc(fmtDuration(job))}</td>
      <td class="right"><button class="btn small" data-job="${esc(job.id)}">${t('action.view')}</button></td>
    </tr>
  `;
  }).join('');
}

function fillSettings(settings) {
  setVal('set-netbox-url', settings.netbox_url);
  setVal('set-netbox-token', '');
  $('set-netbox-token').placeholder = settings.netbox_token_set
    ? t('token.placeholderSaved')
    : t('token.placeholderPaste');
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
  if (sync && !confirm(t('confirm.collectSync', { name: target.name }))) return;
  try {
    await api(`/api/targets/${target.id}/collect`, { method: 'POST', body: { sync } });
    toast(t(sync ? 'toast.submittedSync' : 'toast.submittedCollect', { name: target.name }), 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(t(sync ? 'toast.syncFailed' : 'toast.collectFailed', { msg: err.message }), 'error');
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
  if (sync && !confirm(t('confirm.collectSyncIpam', { name: target.name }))) return;
  try {
    await api(`/api/ip-targets/${target.id}/collect`, { method: 'POST', body: { sync } });
    toast(t(sync ? 'toast.submittedSync' : 'toast.submittedCollect', { name: target.name }), 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(t(sync ? 'toast.syncFailed' : 'toast.collectFailed', { msg: err.message }), 'error');
  }
}

async function collectAllIPs(sync) {
  if (sync && !confirm(t('confirm.collectAllIp'))) return;
  try {
    const result = await api('/api/ip-collect', { method: 'POST', body: { sync } });
    toast(t('toast.submittedIpCount', { n: result.count }), 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(t('toast.submitFailed', { msg: err.message }), 'error');
  }
}

async function reloadCloudTargets() {
  state.cloudTargets = await api('/api/cloud-targets');
  renderCloudTargets();
  await reloadStats();
}

async function collectCloudTarget(target, sync) {
  if (sync && !confirm(t('confirm.collectSyncIpam', { name: target.name }))) return;
  try {
    await api(`/api/cloud-targets/${target.id}/collect`, { method: 'POST', body: { sync } });
    toast(t(sync ? 'toast.submittedSync' : 'toast.submittedCollect', { name: target.name }), 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(t(sync ? 'toast.syncFailed' : 'toast.collectFailed', { msg: err.message }), 'error');
  }
}

async function collectAllCloud(sync) {
  if (sync && !confirm(t('confirm.collectAllCloud'))) return;
  try {
    const result = await api('/api/cloud-collect', { method: 'POST', body: { sync } });
    toast(t('toast.submittedCloudCount', { n: result.count }), 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(t('toast.submitFailed', { msg: err.message }), 'error');
  }
}

function openCloudTargetModal(target) {
  state.editingCloudId = target ? target.id : null;
  $('cloud-target-modal-title').textContent = target ? t('modal.editCloud', { name: target.name }) : t('modal.addCloud');
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
  $('ct-endpoint-label').textContent = isVCenter ? t('ct.endpointRequired') : t('ct.endpointOptional');
  $('ct-project-label').textContent = isGCP ? 'Project ID' : t('ct.projectHuawei');
  $('ct-access-label').textContent = isVCenter ? t('label.username') : t('ct.accessKey');
  $('ct-secret-label').textContent = isVCenter ? t('label.password') : t('ct.secretKey');
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
      toast(t('toast.cloudUpdated'), 'success');
    } else {
      await api('/api/cloud-targets', { method: 'POST', body: target });
      toast(t('toast.cloudCreated'), 'success');
    }
    closeCloudTargetModal();
    await reloadCloudTargets();
  } catch (err) {
    toast(t('toast.saveFailed', { msg: err.message }), 'error');
  }
}

function openIPTargetModal(target) {
  state.editingIpId = target ? target.id : null;
  $('ip-target-modal-title').textContent = target ? t('modal.editIp', { name: target.name }) : t('modal.addIp');
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
      toast(t('toast.ipUpdated'), 'success');
    } else {
      await api('/api/ip-targets', { method: 'POST', body: target });
      toast(t('toast.ipCreated'), 'success');
    }
    closeIPTargetModal();
    await reloadIPTargets();
  } catch (err) {
    toast(t('toast.saveFailed', { msg: err.message }), 'error');
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
    toast(t('toast.scanSubmitted'), 'success');
    closeScanModal();
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(t('toast.scanSubmitFailed', { msg: err.message }), 'error');
  }
}

function openTargetModal(target) {
  state.editingId = target ? target.id : null;
  $('target-modal-title').textContent = target ? t('modal.editTarget', { name: target.name }) : t('modal.addTarget');
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
      toast(t('toast.targetUpdated'), 'success');
    } else {
      await api('/api/targets', { method: 'POST', body: target });
      toast(t('toast.targetCreated'), 'success');
    }
    closeTargetModal();
    await reloadTargets();
  } catch (err) {
    toast(t('toast.saveFailed', { msg: err.message }), 'error');
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
    toast(t('toast.settingsSaved'), 'success');
    renderDashboard();
  } catch (err) {
    toast(t('toast.settingsSaveFailed', { msg: err.message }), 'error');
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
    toast(t('toast.netboxOk', {
      version: result.netbox_version || t('netbox.connected'),
      auth: result.auth_mode === '2' ? 'v2 Bearer' : 'v1 Token',
    }), 'success');
  } catch (err) {
    toast(t('toast.connectFailed', { msg: err.message }), 'error');
  }
}

async function collectAll(sync) {
  if (sync && !confirm(t('confirm.collectAll'))) return;
  try {
    const result = await api('/api/collect', { method: 'POST', body: { sync } });
    toast(t('toast.submittedCount', { n: result.count }), 'success');
    await refreshJobs();
    setPage('jobs');
  } catch (err) {
    toast(t('toast.submitFailed', { msg: err.message }), 'error');
  }
}

async function openJob(id) {
  state.openJobId = id;
  $('drawer-job').classList.remove('hidden');
  $('job-body').innerHTML = `<p class="muted">${t('job.loading')}</p>`;
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
  $('job-subtitle').textContent = `${modeLabel(job.mode)} · ${fmtTime(job.created_at)}`;

  const result = job.result || null;
  const sync = job.sync || null;
  const scan = job.scan || null;
  const cloud = job.cloud || null;
  const identity = result ? (result.identity || {}) : {};
  const isDeviceResult = Boolean(result) && job.protocol !== 'ipscan' && job.protocol !== 'cloud' && job.mode !== 'scan';

  const kv = `
    <dl class="kv">
      <dt>${t('kv.jobId')}</dt><dd class="mono">${esc(job.id)}</dd>
      <dt>${t('kv.protocol')}</dt><dd>${protocolBadge(job.protocol)}</dd>
      <dt>${t('kv.mode')}</dt><dd>${esc(modeLabel(job.mode))}</dd>
      <dt>${t('kv.trigger')}</dt><dd>${esc(triggerLabel(job.trigger))}</dd>
      <dt>${t('kv.times')}</dt><dd>${esc(fmtTime(job.created_at))} / ${esc(fmtTime(job.started_at))} / ${esc(fmtTime(job.finished_at))}</dd>
      <dt>${t('kv.duration')}</dt><dd>${esc(fmtDuration(job))}</dd>
      ${job.error ? `<dt>${t('kv.error')}</dt><dd class="badge-error">${esc(job.error)}</dd>` : ''}
    </dl>`;

  const identityBlock = isDeviceResult ? `
    <h3 class="section-title">${t('job.deviceInfo')}</h3>
    <dl class="kv">
      <dt>${t('kv.name')}</dt><dd>${esc(identity.name || '—')}</dd>
      <dt>${t('kv.vendorModel')}</dt><dd>${esc(identity.manufacturer || '—')} / ${esc(identity.model || '—')}</dd>
      <dt>${t('kv.serialAsset')}</dt><dd>${esc(identity.serial || '—')} / ${esc(identity.asset_tag || '—')}</dd>
      <dt>${t('kv.power')}</dt><dd>${esc(identity.power_state || '—')}</dd>
      <dt>CPU</dt><dd>${esc(identity.cpu_model || '—')}${identity.cpu_cores ? ` · ${t('job.cores', { n: identity.cpu_cores })}` : ''}</dd>
      <dt>${t('kv.memory')}</dt><dd>${identity.memory_gb ? `${identity.memory_gb} GB` : '—'}</dd>
      <dt>BIOS / BMC</dt><dd>${esc(identity.bios_version || '—')} / ${esc(identity.bmc_version || '—')}</dd>
      <dt>${t('kv.interfacesComponents')}</dt><dd>${(result.interfaces || []).length} / ${(result.components || []).length}</dd>
    </dl>` : (job.mode === 'scan' || job.protocol === 'ipscan' || job.protocol === 'cloud' ? '' : `<p class="muted">${t('job.noResult')}</p>`);

  const neighborList = result && Array.isArray(result.neighbors) ? result.neighbors : [];
  const neighborBlock = neighborList.length ? `
    <h3 class="section-title">${t('job.lldp')}</h3>
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>${t('th.localInterface')}</th><th>${t('th.remoteSystem')}</th><th>${t('th.remotePort')}</th><th>${t('th.portDesc')}</th></tr></thead>
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
    <h3 class="section-title">${t('job.foundIps')}</h3>
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>${t('th.address')}</th><th>${t('th.mac')}</th><th>${t('kv.interfaces')}</th><th>${t('th.source')}</th></tr></thead>
        <tbody>${ipList.slice(0, 200).map((entry) => `<tr>
          <td class="mono">${esc(entry.address || '—')}</td>
          <td class="mono">${esc(entry.mac || '—')}</td>
          <td class="mono">${esc(entry.interface || '—')}</td>
          <td class="muted">${esc(entry.source || '—')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>
    ${ipList.length > 200 ? `<p class="muted">${t('job.showFirst200', { n: ipList.length })}</p>` : ''}` : '';

  const warnings = [];
  if (result && Array.isArray(result.warnings)) warnings.push(...result.warnings);
  if (cloud && Array.isArray(cloud.warnings)) warnings.push(...cloud.warnings);
  if (sync && Array.isArray(sync.warnings)) warnings.push(...sync.warnings);
  const warningBlock = warnings.length
    ? `<h3 class="section-title">${t('job.warnings')}</h3><ul class="list warn-list">${warnings.map((w) => `<li>${esc(w)}</li>`).join('')}</ul>`
    : '';

  const deviceSync = Boolean(sync && (sync.device_id || sync.device_name));
  const syncBlock = sync ? `
    <h3 class="section-title">${t('job.syncResult')}</h3>
    <dl class="kv">
      ${deviceSync ? `<dt>${t('kv.device')}</dt><dd>${esc(sync.device_name || '—')} (#${sync.device_id}) ${sync.device_url ? `· <a href="${esc(sync.device_url)}" target="_blank" rel="noreferrer">${t('job.open')}</a>` : ''}</dd>` : ''}
      ${deviceSync ? `<dt>${t('kv.interfaces')}</dt><dd>${t('sync.createdUpdated', { created: sync.interfaces_created, updated: sync.interfaces_updated })}</dd>` : ''}
      ${deviceSync ? `<dt>${t('kv.cables')}</dt><dd>${t('sync.createdSkipped', { created: sync.cables_created || 0, skipped: sync.cables_skipped || 0 })}</dd>` : ''}
      <dt>${t('kv.ipAddresses')}</dt><dd>${t('sync.ipStats', { created: sync.ip_created || 0, assigned: sync.ip_assigned || 0, skipped: sync.ip_skipped || 0 })}</dd>
      ${(sync.prefix_created || sync.prefix_skipped) ? `<dt>${t('kv.prefixes')}</dt><dd>${t('sync.prefixStats', { created: sync.prefix_created || 0, existing: sync.prefix_skipped || 0 })}</dd>` : ''}
      ${deviceSync ? `<dt>${t('kv.inventory')}</dt><dd>${t('sync.createdUpdated', { created: sync.inventory_created, updated: sync.inventory_updated })}</dd>` : ''}
      ${sync.ip_address ? `<dt>${t('kv.primaryIp')}</dt><dd class="mono">${esc(sync.ip_address)}</dd>` : ''}
    </dl>
    ${Array.isArray(sync.actions) && sync.actions.length ? `<ul class="list">${sync.actions.map((a) => `<li>${esc(a)}</li>`).join('')}</ul>` : ''}
  ` : '';

  const scanStatsBlock = scan ? `
    <h3 class="section-title">${t('job.scanResult')}</h3>
    <dl class="kv">
      <dt>${t('kv.scanSubnetsProtocol')}</dt><dd class="mono">${esc(scan.subnets || '—')} · ${esc(protocolLabel(scan.protocol) || '—')}</dd>
      <dt>${t('kv.scannedAlive')}</dt><dd>${scan.scanned} / ${scan.alive}</dd>
      <dt>${t('kv.addedExisting')}</dt><dd>${scan.added} / ${scan.existing}</dd>
      <dt>${t('kv.skippedFailed')}</dt><dd>${scan.skipped} / ${scan.failed}</dd>
    </dl>
    ${Array.isArray(scan.warnings) && scan.warnings.length ? `<ul class="list warn-list">${scan.warnings.map((w) => `<li>${esc(w)}</li>`).join('')}</ul>` : ''}
    ${Array.isArray(scan.devices) && scan.devices.length ? `
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>${t('th.address')}</th><th>${t('th.name')}</th><th>${t('kv.vendorModel')}</th><th>${t('th.status')}</th><th>${t('th.remarks')}</th></tr></thead>
        <tbody>${scan.devices.map((d) => `<tr>
          <td class="mono">${esc(d.address || '—')}</td>
          <td>${esc(d.name || '—')}</td>
          <td>${esc(d.manufacturer || '—')} / ${esc(d.model || '—')}</td>
          <td><span class="badge badge-${esc(d.status === 'added' ? 'success' : d.status === 'failed' ? 'error' : 'queued')}">${esc(scanStatusLabel(d.status))}</span></td>
          <td class="muted">${esc(d.error || '')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>` : ''}
  ` : '';

  const prefixList = cloud && Array.isArray(cloud.prefixes) ? cloud.prefixes : [];
  const addressList = cloud && Array.isArray(cloud.addresses) ? cloud.addresses : [];
  const cloudBlock = cloud ? `
    <h3 class="section-title">${t('job.cloudResult')}</h3>
    <dl class="kv">
      <dt>${t('kv.provider')}</dt><dd>${esc(cloudProviderLabel(cloud.provider) || '—')}</dd>
      <dt>${t('kv.vpcSubnets')}</dt><dd>${cloud.vpcs || 0} / ${cloud.subnets || 0}</dd>
      <dt>${t('kv.instancesPublic')}</dt><dd>${cloud.instances || 0} / ${cloud.public_ips || 0}</dd>
      <dt>${t('kv.prefixesAddresses')}</dt><dd>${prefixList.length} / ${addressList.length}</dd>
    </dl>
    ${prefixList.length ? `
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>${t('th.prefix')}</th><th>${t('th.kind')}</th><th>${t('th.name')}</th><th>${t('th.vpc')}</th><th>${t('th.region')}</th></tr></thead>
        <tbody>${prefixList.slice(0, 300).map((p) => `<tr>
          <td class="mono">${esc(p.cidr || '—')}</td>
          <td>${esc(cloudPrefixKindLabel(p.kind) || '—')}</td>
          <td>${esc(p.name || '—')}</td>
          <td class="mono">${esc(p.vpc || '—')}</td>
          <td class="muted">${esc(p.region || '—')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>
    ${prefixList.length > 300 ? `<p class="muted">${t('job.showFirst300Prefixes', { n: prefixList.length })}</p>` : ''}` : ''}
    ${addressList.length ? `
    <div class="table-wrap">
      <table class="table">
        <thead><tr><th>${t('th.address')}</th><th>${t('th.type')}</th><th>${t('th.nameInstance')}</th><th>${t('th.interfaceMac')}</th><th>${t('th.region')}</th></tr></thead>
        <tbody>${addressList.slice(0, 300).map((a) => `<tr>
          <td class="mono">${esc(a.address || '—')}</td>
          <td>${a.public ? `<span class="badge badge-success">${t('badge.public')}</span>` : `<span class="badge badge-queued">${t('badge.instance')}</span>`}</td>
          <td>${esc(a.name || '—')}${a.instance_id ? `<div class="muted mono">${esc(a.instance_id)}</div>` : ''}</td>
          <td class="mono">${esc(a.interface || '—')}${a.mac ? ` / ${esc(a.mac)}` : ''}</td>
          <td class="muted">${esc(a.region || '—')}</td>
        </tr>`).join('')}</tbody>
      </table>
    </div>
    ${addressList.length > 300 ? `<p class="muted">${t('job.showFirst300Addresses', { n: addressList.length })}</p>` : ''}` : ''}
  ` : '';

  const log = Array.isArray(job.log) && job.log.length
    ? job.log.join('\n')
    : '(no log)';

  $('job-body').innerHTML = `
    <div class="job-status-head">${statusBadge(job.status)}${job.error ? `<span class="badge badge-error">${t('job.viewError')}</span>` : ''}</div>
    ${kv}
    ${syncBlock}
    ${scanStatsBlock}
    ${cloudBlock}
    ${identityBlock}
    ${neighborBlock}
    ${ipBlock}
    ${warningBlock}
    <h3 class="section-title">${t('job.log')}</h3>
    <pre class="log-box mono">${esc(log)}</pre>
    <details>
      <summary class="muted" style="cursor:pointer;margin-top:14px">${t('job.rawJson')}</summary>
      <pre class="json-box mono">${esc(JSON.stringify(result, null, 2) || 'null')}</pre>
    </details>
  `;
}

// refreshLanguage re-renders everything after a language switch.
function refreshLanguage() {
  applyStaticI18n();
  renderAll();
  setPage(state.page);
  if (state.settings) {
    $('set-netbox-token').placeholder = state.settings.netbox_token_set
      ? t('token.placeholderSaved')
      : t('token.placeholderPaste');
  }
  refreshModalTitles();
  if (!$('modal-cloudtarget').classList.contains('hidden')) {
    toggleCloudProviderFields(val('ct-provider'));
  }
  if (state.openJobId && !$('drawer-job').classList.contains('hidden')) {
    refreshOpenJob();
  }
}

function refreshModalTitles() {
  if (!$('modal-target').classList.contains('hidden')) {
    const target = state.targets.find((item) => item.id === state.editingId);
    $('target-modal-title').textContent = target ? t('modal.editTarget', { name: target.name }) : t('modal.addTarget');
  }
  if (!$('modal-iptarget').classList.contains('hidden')) {
    const target = state.ipTargets.find((item) => item.id === state.editingIpId);
    $('ip-target-modal-title').textContent = target ? t('modal.editIp', { name: target.name }) : t('modal.addIp');
  }
  if (!$('modal-cloudtarget').classList.contains('hidden')) {
    const target = state.cloudTargets.find((item) => item.id === state.editingCloudId);
    $('cloud-target-modal-title').textContent = target ? t('modal.editCloud', { name: target.name }) : t('modal.addCloud');
  }
}

function bindEvents() {
  document.querySelectorAll('.nav-item').forEach((btn) => {
    btn.addEventListener('click', () => setPage(btn.dataset.page));
  });
  document.querySelectorAll('[data-goto]').forEach((btn) => {
    btn.addEventListener('click', () => setPage(btn.dataset.goto));
  });

  $('btn-lang').addEventListener('click', () => setLang(currentLang() === 'zh' ? 'en' : 'zh'));
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
      toast(t('toast.cleared', { n: result.removed }), 'success');
      await refreshJobs();
    } catch (err) {
      toast(t('toast.clearFailed', { msg: err.message }), 'error');
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
        if (!confirm(t('confirm.deleteTarget', { name: target.name }))) return;
        try {
          await api(`/api/targets/${target.id}`, { method: 'DELETE' });
          toast(t('toast.targetDeleted'), 'success');
          await reloadTargets();
        } catch (err) {
          toast(t('toast.deleteFailed', { msg: err.message }), 'error');
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
        if (!confirm(t('confirm.deleteIp', { name: target.name }))) return;
        try {
          await api(`/api/ip-targets/${target.id}`, { method: 'DELETE' });
          toast(t('toast.ipDeleted'), 'success');
          await reloadIPTargets();
        } catch (err) {
          toast(t('toast.deleteFailed', { msg: err.message }), 'error');
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
        if (!confirm(t('confirm.deleteCloud', { name: target.name }))) return;
        try {
          await api(`/api/cloud-targets/${target.id}`, { method: 'DELETE' });
          toast(t('toast.cloudDeleted'), 'success');
          await reloadCloudTargets();
        } catch (err) {
          toast(t('toast.deleteFailed', { msg: err.message }), 'error');
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
  applyStaticI18n();
  boot();
});
