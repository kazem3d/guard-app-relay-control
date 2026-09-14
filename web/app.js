// app.js — talks to the JSON API using the Basic-auth header value stashed
// in sessionStorage by login.html. Every request goes through api(); a 401
// from anywhere clears the stored credential and bounces back to the login
// page, which is what gives this stateless Basic-auth scheme a real logout.

const AUTH_KEY = 'rr_auth';

function authHeader() {
  return sessionStorage.getItem(AUTH_KEY);
}

function goToLogin() {
  sessionStorage.removeItem(AUTH_KEY);
  location.href = 'login.html';
}

async function api(path, opts = {}) {
  const auth = authHeader();
  if (!auth) {
    goToLogin();
    throw new Error('not authenticated');
  }
  const headers = Object.assign({ 'Authorization': auth }, opts.headers || {});
  if (opts.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  const resp = await fetch(path, Object.assign({}, opts, { headers }));
  if (resp.status === 401) {
    goToLogin();
    throw new Error('unauthorized');
  }
  return resp;
}

document.getElementById('logout-btn').addEventListener('click', goToLogin);

// ---- must-change gate --------------------------------------------------

async function checkMustChange() {
  // A 428 from any authenticated GET signals the forced password-change
  // state; probing /api/relays is as good as anything else for this.
  const resp = await api('/api/relays');
  if (resp.status === 428) {
    document.getElementById('must-change-panel').hidden = false;
    document.getElementById('app').hidden = true;
    return true;
  }
  document.getElementById('must-change-panel').hidden = true;
  document.getElementById('app').hidden = false;
  return false;
}

document.getElementById('mc-submit').addEventListener('click', async () => {
  const current = document.getElementById('mc-current').value;
  const next = document.getElementById('mc-new').value;
  const statusEl = document.getElementById('mc-status');
  statusEl.hidden = true;

  const resp = await api('/api/auth/password', {
    method: 'PUT',
    body: JSON.stringify({ current_password: current, new_password: next }),
  });
  const body = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    statusEl.textContent = body.error || 'Failed to set password.';
    statusEl.className = 'status-msg error';
    statusEl.hidden = false;
    return;
  }
  // Refresh the stored Authorization header to match the new password.
  sessionStorage.setItem(AUTH_KEY, 'Basic ' + btoa(decodeAuthUser() + ':' + next));
  boot();
});

function decodeAuthUser() {
  const raw = atob(authHeader().slice(6)); // "user:pass"
  return raw.slice(0, raw.indexOf(':'));
}

// ---- tabs ---------------------------------------------------------------

document.querySelectorAll('.tabs button').forEach((btn) => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tabs button').forEach((b) => b.classList.remove('active'));
    document.querySelectorAll('.tab').forEach((t) => (t.hidden = true));
    btn.classList.add('active');
    document.getElementById('tab-' + btn.dataset.tab).hidden = false;
  });
});

// ---- device tab -----------------------------------------------------

async function loadDevice() {
  const resp = await api('/api/device');
  if (!resp.ok) return;
  const d = await resp.json();
  const rows = [
    ['Hostname', d.hostname],
    ['IP', d.ip],
    ['MAC', d.mac],
    ['Firmware', d.firmware],
    ['Model', d.model],
    ['Uptime', d.uptime],
    ['CPU temperature', d.cpu_temp_celsius != null ? d.cpu_temp_celsius.toFixed(1) + '°C' : '—'],
  ];
  const grid = document.getElementById('device-grid');
  grid.innerHTML = '';
  for (const [label, value] of rows) {
    const dt = document.createElement('dt');
    dt.textContent = label;
    const dd = document.createElement('dd');
    dd.textContent = value || '—';
    grid.append(dt, dd);
  }
}

// ---- network tab ----------------------------------------------------

async function loadNetwork() {
  const resp = await api('/api/network');
  if (!resp.ok) return;
  const n = await resp.json();
  document.querySelectorAll('input[name=net-mode]').forEach((r) => (r.checked = r.value === n.mode));
  document.getElementById('net-hostname').value = n.hostname || '';
  document.getElementById('net-ip').value = n.address || '';
  document.getElementById('net-netmask').value = n.netmask || '';
  document.getElementById('net-gateway').value = n.gateway || '';
  document.getElementById('net-dns').value = (n.dns || []).join(', ');
}

document.getElementById('net-save').addEventListener('click', async () => {
  const statusEl = document.getElementById('net-status');
  statusEl.hidden = true;
  const mode = document.querySelector('input[name=net-mode]:checked').value;
  const payload = {
    mode,
    hostname: document.getElementById('net-hostname').value,
    address: document.getElementById('net-ip').value,
    netmask: document.getElementById('net-netmask').value,
    gateway: document.getElementById('net-gateway').value,
    dns: document.getElementById('net-dns').value.split(',').map((s) => s.trim()).filter(Boolean),
  };
  const resp = await api('/api/network', { method: 'PUT', body: JSON.stringify(payload) });
  const body = await resp.json().catch(() => ({}));
  statusEl.hidden = false;
  if (!resp.ok) {
    statusEl.textContent = body.error || 'Failed to save network settings.';
    statusEl.className = 'status-msg error';
    return;
  }
  statusEl.textContent = body.note || 'Applying new network settings…';
  statusEl.className = 'status-msg ok';
  // Give the device a moment, then try to confirm at whatever address is
  // currently reachable (this page keeps working if the IP didn't change,
  // e.g. DHCP renewal). If the IP actually changed, this origin can no
  // longer reach the device, so the confirm below is the fallback: navigate
  // to the new address, log in, and click it within the 60s rollback window.
  setTimeout(() => { api('/api/network/confirm', { method: 'POST' }).catch(() => {}); }, 5000);
});

document.getElementById('net-confirm').addEventListener('click', async () => {
  const statusEl = document.getElementById('net-status');
  statusEl.hidden = false;
  statusEl.textContent = 'Confirming…';
  statusEl.className = 'status-msg';
  try {
    const resp = await api('/api/network/confirm', { method: 'POST' });
    statusEl.textContent = resp.ok ? 'Network settings confirmed.' : 'Failed to confirm network settings.';
    statusEl.className = resp.ok ? 'status-msg ok' : 'status-msg error';
  } catch (err) {
    statusEl.textContent = 'Failed to confirm network settings.';
    statusEl.className = 'status-msg error';
  }
});

// ---- relays tab -----------------------------------------------------

let relayState = {}; // id -> latest state, kept fresh by SSE

function renderRelays(relays) {
  const list = document.getElementById('relay-list');
  list.innerHTML = '';
  for (const r of relays) {
    relayState[r.id] = r;
    const card = document.createElement('div');
    card.className = 'panel relay-card';
    card.dataset.relayId = r.id;
    card.innerHTML = `
      <div class="relay-title">
        <span class="dot ${r.on ? 'on' : ''}"></span>
        <span>Relay ${r.id}</span>
        <span class="countdown"></span>
      </div>
      <div class="field"><label>GPIO</label><input type="number" class="f-gpio" value="${r.gpio}"></div>
      <div class="field"><label>Name</label><input type="text" class="f-name" value="${r.name || ''}"></div>
      <div class="field"><label>Trigger time (sec)</label><input type="number" step="0.1" class="f-duration" value="${(r.duration_ms || 2000) / 1000}"></div>
      <button class="btn" data-action="test">TEST</button>
      <button class="btn primary" data-action="save">Save</button>
    `;
    card.querySelector('[data-action=test]').addEventListener('click', () => triggerRelay(r.id));
    card.querySelector('[data-action=save]').addEventListener('click', () => saveRelay(r.id, card));
    list.append(card);
  }
}

async function loadRelays() {
  const resp = await api('/api/relays');
  if (!resp.ok) return;
  renderRelays(await resp.json());
}

async function triggerRelay(id) {
  await api(`/api/relay/${id}/trigger`, { method: 'POST' });
}

async function saveRelay(id, card) {
  const gpio = parseInt(card.querySelector('.f-gpio').value, 10);
  const name = card.querySelector('.f-name').value;
  const durationMs = Math.round(parseFloat(card.querySelector('.f-duration').value) * 1000);
  const resp = await api('/api/relays');
  if (!resp.ok) return;
  const relays = await resp.json();
  const updated = relays.map((r) => r.id === id
    ? { ...r, gpio, name, duration_ms: durationMs }
    : { id: r.id, name: r.name, gpio: r.gpio, active_high: r.active_high, duration_ms: r.duration_ms, debounce_ms: r.debounce_ms, enabled: r.enabled });
  await api('/api/relays', { method: 'PUT', body: JSON.stringify(updated.map(normaliseRelay)) });
  loadRelays();
}

function normaliseRelay(r) {
  return {
    id: r.id,
    name: r.name,
    gpio: r.gpio,
    active_high: !!r.active_high,
    duration_ms: r.duration_ms,
    debounce_ms: r.debounce_ms || 0,
    enabled: r.enabled !== false,
  };
}

// ---- live updates via SSE -------------------------------------------

// The browser's native EventSource can't send a custom Authorization
// header, and this appliance deliberately has no cookie-based session (see
// login.html). So the live stream is read with plain fetch() instead, which
// does support custom headers, and its streamed body is parsed as SSE by
// hand. A local counter fallback keeps countdowns smooth between events.
async function connectEvents() {
  const auth = authHeader();
  if (!auth) return;

  let resp;
  try {
    resp = await api('/api/events');
  } catch {
    return; // boot() already redirected to login on a 401
  }
  if (!resp.ok || !resp.body) {
    // Streaming unsupported or the request failed outright: local ticking
    // of the countdowns already shown from loadRelays() still works.
    return;
  }

  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let idx;
      while ((idx = buf.indexOf('\n\n')) !== -1) {
        const chunk = buf.slice(0, idx);
        buf = buf.slice(idx + 2);
        const dataLine = chunk.split('\n').find((l) => l.startsWith('data: '));
        if (dataLine) applyRelayEvent(JSON.parse(dataLine.slice(6)));
      }
    }
  } catch {
    // Connection dropped (page navigation, network blip); nothing to
    // recover here since a fresh boot() reconnects on next load.
  }
}

function applyRelayEvent(state) {
  relayState[state.id] = state;
  const card = document.querySelector(`.relay-card[data-relay-id="${state.id}"]`);
  if (!card) return;
  card.querySelector('.dot').classList.toggle('on', state.on);
  card.querySelector('.countdown').textContent = state.remaining_ms > 0 ? (state.remaining_ms / 1000).toFixed(1) + 's' : '';
}

// Ticks the displayed countdowns down between SSE events so they read
// smoothly instead of only updating on state-change boundaries.
setInterval(() => {
  for (const [id, r] of Object.entries(relayState)) {
    if (!r.on || r.remaining_ms <= 0) continue;
    r.remaining_ms = Math.max(0, r.remaining_ms - 250);
    const card = document.querySelector(`.relay-card[data-relay-id="${id}"]`);
    if (card) card.querySelector('.countdown').textContent = (r.remaining_ms / 1000).toFixed(1) + 's';
  }
}, 250);

// ---- boot -------------------------------------------------------------

async function boot() {
  if (!authHeader()) {
    goToLogin();
    return;
  }
  const mustChange = await checkMustChange();
  if (mustChange) return;
  await Promise.all([loadDevice(), loadNetwork(), loadRelays()]);
  connectEvents();
}

boot();
