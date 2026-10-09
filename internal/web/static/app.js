let state = {
    proxies: [], enabled: [], groups: {}, custom_rules: [],
    custom_srs: [], subscription: '', dns: null, devices: [], theme: 'dark',
    settings: null,
};
let catalogCache = [];
let monitorTimer = null;
let logsTimer = null;
let logsAuto = true;
let currentTab = 'servers';
let expandedRules = {};
let expandedSRS = {};
let expandedGroups = {};

// История delay по каждому серверу (для графика)
let serverDelayHistory = {};
let serverStats = {};
let sniHidden = {};

const TAB_META = {
    servers:    { title: 'Серверы',          sub: 'Управление VPN-серверами' },
    rules:      { title: 'Правила обхода',    sub: 'Все правила в одном месте' },
    priorities: { title: 'Приоритеты',        sub: 'Порядок серверов в группах' },
    devices:    { title: 'Устройства',        sub: 'Маршрутизация по IP' },
    dns:        { title: 'DNS',               sub: 'DNS-серверы и резолвинг' },
    sync:       { title: 'Синхронизация',     sub: 'Автообновление списков' },
    settings:   { title: 'Настройки',         sub: 'Логи, рестарт, экспорт' },
    monitor:    { title: 'Мониторинг',        sub: 'Состояние outbounds' },
    logs:       { title: 'Журнал',            sub: 'Лог Sing-box в оперативке' },
};

async function api(path, method = 'GET', body = null) {
    const opts = { method, headers: { 'Content-Type': 'application/json' } };
    if (body) opts.body = JSON.stringify(body);
    const r = await fetch(path, opts);
    return r.json();
}

function switchTab(tab) {
    currentTab = tab;
    document.querySelectorAll('.nav-tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
    document.querySelectorAll('.tab-page').forEach(p => p.classList.toggle('active', p.dataset.page === tab));
    const meta = TAB_META[tab] || { title: tab, sub: '' };
    document.getElementById('page-title').textContent = meta.title;
    document.getElementById('page-sub').textContent = meta.sub;
    window.scrollTo({ top: 0, behavior: 'smooth' });
    if (tab === 'monitor') refreshMonitor();
    if (tab === 'logs') refreshLogs();
}

function applyTheme(theme) {
    let actual = theme;
    if (theme === 'auto') {
        actual = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    document.documentElement.setAttribute('data-theme', actual);
    document.querySelectorAll('[data-theme-btn]').forEach(b => {
        b.classList.toggle('active', b.dataset.themeBtn === theme);
    });
}

function initSidebarCollapse() {
    const sidebar = document.getElementById('sidebar');
    const toggle = document.getElementById('sidebar-toggle');
    if (!sidebar || !toggle) return;
    toggle.addEventListener('click', () => {
        sidebar.classList.toggle('collapsed');
        localStorage.setItem('sb_collapsed', sidebar.classList.contains('collapsed') ? '1' : '0');
    });
    if (localStorage.getItem('sb_collapsed') === '1') sidebar.classList.add('collapsed');
}

function initMobileMenu() {
    const sidebar = document.getElementById('sidebar');
    const btn = document.getElementById('mobile-menu-btn');
    if (!sidebar || !btn) return;
    let overlay = document.querySelector('.mobile-overlay');
    if (!overlay) {
        overlay = document.createElement('div');
        overlay.className = 'mobile-overlay';
        document.body.appendChild(overlay);
    }
    btn.addEventListener('click', () => {
        sidebar.classList.toggle('mobile-open');
        overlay.classList.toggle('active');
    });
    overlay.addEventListener('click', () => {
        sidebar.classList.remove('mobile-open');
        overlay.classList.remove('active');
    });
    document.querySelectorAll('.nav-tab').forEach(t => {
        t.addEventListener('click', () => {
            if (window.innerWidth <= 768) {
                sidebar.classList.remove('mobile-open');
                overlay.classList.remove('active');
            }
        });
    });
    window.addEventListener('resize', () => {
        if (window.innerWidth > 768) {
            sidebar.classList.remove('mobile-open');
            overlay.classList.remove('active');
        }
    });
}

async function loadState() {
    const s = await api('/api/state');
    state.proxies      = s.proxies || [];
    state.enabled      = s.enabled || [];
    state.groups       = s.groups  || {};
    state.custom_rules = s.custom_rules || [];
    state.custom_srs   = s.custom_srs || [];
    state.subscription = s.subscription || '';
    state.dns          = s.dns;
    state.devices      = s.devices || [];
    state.theme        = s.theme || 'dark';
    state.settings     = s.settings || null;

    applyTheme(state.theme);
    renderServers();
    await loadCatalog();
    renderGroups();
    renderDevices();
    renderDNS();
    renderCustomRules();
    renderCustomSRS();
    renderSettings();
    updateBadges();
    switchTab(currentTab);

    if (monitorTimer) clearInterval(monitorTimer);
    refreshMonitor();
    monitorTimer = setInterval(refreshMonitor, 5000);

    if (logsTimer) clearInterval(logsTimer);
    refreshLogs();
    logsTimer = setInterval(() => { if (logsAuto) refreshLogs(); }, 3000);
}

function updateBadges() {
    const el1 = document.getElementById('badge-servers');
    const el2 = document.getElementById('badge-devices');
    const el3 = document.getElementById('badge-rules');
    if (el1) el1.textContent = state.proxies.length;
    if (el2) el2.textContent = state.devices.length;
    if (el3) el3.textContent = state.enabled.length + state.custom_rules.length + state.custom_srs.length;
}

const CATALOG_ICONS = {
    'youtube': '▶️', 'tiktok': '🎵', 'hdrezka': '🎬', 'anime': '🌸', 'porn': '🔞',
    'telegram': '✈️', 'discord': '💬', 'twitter': '🐦', 'meta': '📘',
    'google_ai': '🤖', 'google_meet': '📹', 'google_play': '📱',
    'roblox': '🎮', 'russia_inside': '🇷🇺', 'russia_outside': '🌍',
    'geoblock': '🌐', 'block': '🚫', 'hodca': '📦',
    'cloudflare': '☁️', 'cloudfront': '🌤️', 'digitalocean': '💧',
    'hetzner': '🟥', 'ovh': '🔵', 'news': '📰',
};

async function loadCatalog() {
    catalogCache = await api('/api/catalog');
    renderCatalog();
}

function renderCatalog() {
    const container = document.getElementById('catalog');
    if (!container) return;
    container.innerHTML = '';
    const byCategory = {};
    for (const item of catalogCache) {
        if (!byCategory[item.category]) byCategory[item.category] = [];
        byCategory[item.category].push(item);
    }
    for (const [category, items] of Object.entries(byCategory)) {
        const catEl = document.createElement('div');
        catEl.className = 'catalog-category';
        catEl.textContent = category;
        container.appendChild(catEl);
        for (const item of items) {
            const enabled = state.enabled.includes(item.id);
            const el = document.createElement('div');
            el.className = 'catalog-item' + (enabled ? ' enabled' : '');
            const icon = CATALOG_ICONS[item.id] || '📦';
            el.innerHTML = `
                <div class="catalog-row">
                    <div class="catalog-icon">${icon}</div>
                    <div class="catalog-info">
                        <div class="catalog-name">${escapeHtml(item.name)}</div>
                        <div class="catalog-desc">${escapeHtml(item.description)}</div>
                    </div>
                    <label class="toggle">
                        <input type="checkbox" ${enabled ? 'checked' : ''}>
                        <span class="toggle-slider"></span>
                    </label>
                </div>
                ${enabled ? `<div class="catalog-servers"><div class="catalog-servers-label">Серверы в группе:</div>${renderServerChips(item.id)}</div>` : ''}
            `;
            el.querySelector('input[type="checkbox"]').addEventListener('change', (e) => {
                e.stopPropagation();
                toggleList(item.id, e.target.checked);
            });
            el.querySelectorAll('.server-chip').forEach(chip => {
                chip.addEventListener('click', (e) => {
                    e.stopPropagation();
                    toggleServerInGroup(item.id, chip.dataset.tag);
                });
            });
            container.appendChild(el);
        }
    }
}

function renderServerChips(groupId) {
    const grp = state.groups[groupId] || { proxies: [] };
    const groupTags = (grp.proxies || []).map(p => ({ tag: p.tag, enabled: p.enabled }));
    return state.proxies.map(p => {
        const entry = groupTags.find(x => x.tag === p.tag);
        const on = entry ? entry.enabled : false;
        return `<span class="server-chip ${on ? 'on' : ''}" data-tag="${escapeHtml(p.tag)}">${escapeHtml(p.name || p.tag)}</span>`;
    }).join('');
}

async function toggleServerInGroup(groupId, tag) {
    const grp = state.groups[groupId] || { proxies: [] };
    grp.proxies = grp.proxies || [];
    let entry = grp.proxies.find(p => p.tag === tag);
    if (!entry) {
        entry = { tag, enabled: true };
        grp.proxies.push(entry);
    } else {
        entry.enabled = !entry.enabled;
    }
    state.groups[groupId] = grp;
    renderCatalog();
    renderGroups();
    try { await api('/api/groups', 'POST', { groups: state.groups }); } catch (e) {}
}

async function toggleList(id, enabled) {
    const r = await api('/api/lists/toggle', 'POST', { id, enabled });
    state.enabled = r.enabled;
    const s = await api('/api/state');
    state.groups = s.groups || {};
    renderCatalog();
    renderGroups();
    updateBadges();
}

// --- Серверы ---
function renderServers() {
    const container = document.getElementById('server-cards');
    if (!container) return;
    container.innerHTML = '';
    for (const p of state.proxies) {
        const el = document.createElement('div');
        el.className = 'srv-card';
        el.dataset.tag = p.tag;
        const flag = extractFlag(p.name || '');
        const displayName = (p.name || p.tag).replace(/^[\u{1F1E6}-\u{1F1FF}]{2}\s*/u, '');
        const tagsHtml = buildTagsHtml(p);
        const sniHiddenNow = sniHidden[p.tag];
        const sniDisplay = p.sni ? (sniHiddenNow ? '•••••••••••' : escapeHtml(p.sni)) : '—';
        const history = serverDelayHistory[p.tag] || [];
        const stats = serverStats[p.tag] || { avg: 0 };
        const lastDelay = history.length ? history[history.length - 1] : 0;
        const histo = renderHistogram(history, 20);
        const sparkline = renderSparkline([]);

        el.innerHTML = `
            <div class="srv-head">
                <div class="srv-title">
                    <span class="srv-flag">${flag}</span>
                    <span class="srv-name">${escapeHtml(displayName)}</span>
                </div>
                <div class="srv-delay-top" data-delay-top>${lastDelay > 0 ? lastDelay + 'ms' : '—'}</div>
            </div>
            <div class="srv-tags">${tagsHtml}</div>
            <div class="srv-info">
                <div class="srv-info-row">
                    <div class="srv-info-label">СЕРВЕР</div>
                    <div class="srv-info-value mono">
                        ${escapeHtml(p.server || '')}
                        <span class="srv-reveal" data-action="reveal-server">${sniHiddenNow ? '👁' : '🙈'}</span>
                    </div>
                </div>
                <div class="srv-info-row"><div class="srv-info-label">ПОРТ</div><div class="srv-info-value mono">:${p.port}</div></div>
                <div class="srv-info-row"><div class="srv-info-label">SNI</div><div class="srv-info-value mono">${sniDisplay}</div></div>
                <div class="srv-info-row"><div class="srv-info-label">DELAY</div><div class="srv-info-value mono"><span data-delay>${lastDelay > 0 ? lastDelay + ' ms' : '—'}</span><span class="srv-info-sub">avg ${stats.avg > 0 ? stats.avg + 'ms' : '—'}</span></div></div>
            </div>
            <div class="srv-actions">
                <button class="srv-btn" data-action="test">🧪 Тест</button>
                <button class="srv-btn srv-btn-danger" data-action="remove">🗑 Удалить</button>
            </div>
            <div class="srv-charts">
                <div class="srv-chart-box">
                    <div class="srv-chart-title">ТРАФИК</div>
                    <div class="srv-sparkline">${sparkline}</div>
                    <div class="srv-chart-meta">↓ 0 Бит/с · ↑ 0 Бит/с</div>
                </div>
                <div class="srv-chart-box">
                    <div class="srv-chart-title">DELAY (5 МИН)</div>
                    <div class="srv-histo">${histo}</div>
                </div>
            </div>
        `;
        el.querySelector('[data-action="test"]').addEventListener('click', () => testProxy(p.tag, el));
        el.querySelector('[data-action="remove"]').addEventListener('click', () => removeProxy(p.tag));
        el.querySelector('[data-action="reveal-server"]').addEventListener('click', (e) => {
            e.stopPropagation();
            sniHidden[p.tag] = !sniHidden[p.tag];
            renderServers();
        });
        container.appendChild(el);
    }
    updateBadges();
}

function extractFlag(name) {
    const m = name.match(/^([\u{1F1E6}-\u{1F1FF}]{2})/u);
    return m ? m[1] : '🌐';
}

function buildTagsHtml(p) {
    const tags = [];
    tags.push(`<span class="srv-tag srv-tag-proto">${escapeHtml((p.protocol || '').toUpperCase())}</span>`);
    if (p.security === 'reality') tags.push(`<span class="srv-tag srv-tag-reality">REALITY</span>`);
    else if (p.security === 'tls') tags.push(`<span class="srv-tag srv-tag-tls">TLS</span>`);
    if (p.network && p.network !== 'tcp') tags.push(`<span class="srv-tag srv-tag-net">${escapeHtml(p.network.toUpperCase())}</span>`);
    else tags.push(`<span class="srv-tag srv-tag-net">TCP</span>`);
    return tags.join('');
}

function renderHistogram(values, maxBars) {
    const bars = [];
    const slice = values.slice(-maxBars);
    while (slice.length < maxBars) slice.unshift(null);
    for (const v of slice) {
        if (v === null) bars.push(`<div class="srv-hist-bar empty"></div>`);
        else if (v === 0) bars.push(`<div class="srv-hist-bar fail"></div>`);
        else if (v < 200) bars.push(`<div class="srv-hist-bar good"></div>`);
        else if (v < 500) bars.push(`<div class="srv-hist-bar mid"></div>`);
        else bars.push(`<div class="srv-hist-bar bad"></div>`);
    }
    return bars.join('');
}

function renderSparkline(values) {
    const bars = [];
    for (let i = 0; i < 20; i++) bars.push(`<div class="srv-spark-bar"></div>`);
    return bars.join('');
}

async function testProxy(tag, card) {
    const delayEl = card.querySelector('[data-delay]');
    delayEl.textContent = '...';
    try {
        const r = await api('/api/proxies/test', 'POST', { tag });
        if (r.error || r.message) { delayEl.textContent = '✗'; addDelayToHistory(tag, 0); }
        else { const d = r.delay || 0; delayEl.textContent = d + ' ms'; addDelayToHistory(tag, d); }
        setTimeout(renderServers, 100);
    } catch (e) { delayEl.textContent = '✗'; addDelayToHistory(tag, 0); }
}

function addDelayToHistory(tag, delay) {
    if (!serverDelayHistory[tag]) serverDelayHistory[tag] = [];
    serverDelayHistory[tag].push(delay);
    if (serverDelayHistory[tag].length > 20) serverDelayHistory[tag].shift();
    const hist = serverDelayHistory[tag].filter(x => x > 0);
    if (hist.length > 0) {
        const sum = hist.reduce((a, b) => a + b, 0);
        serverStats[tag] = { avg: Math.round(sum / hist.length), min: Math.min(...hist), max: Math.max(...hist), fails: serverDelayHistory[tag].filter(x => x === 0).length };
    } else {
        serverStats[tag] = { avg: 0, min: 0, max: 0, fails: serverDelayHistory[tag].length };
    }
}

async function removeProxy(tag) {
    if (!confirm('Удалить сервер ' + tag + '?')) return;
    const r = await api('/api/proxies/remove', 'POST', { tag });
    if (r.error) { alert('Ошибка: ' + r.error); return; }
    delete serverDelayHistory[tag];
    delete serverStats[tag];
    await loadState();
}

const btnAddProxy = document.getElementById('btn-add-proxy');
if (btnAddProxy) btnAddProxy.addEventListener('click', () => {
    document.getElementById('modal-add').style.display = 'flex';
    document.getElementById('modal-raw').value = '';
    document.getElementById('modal-status').textContent = '';
});
const btnModalCancel = document.getElementById('modal-cancel');
if (btnModalCancel) btnModalCancel.addEventListener('click', () => {
    document.getElementById('modal-add').style.display = 'none';
});
const btnModalSave = document.getElementById('modal-save');
if (btnModalSave) btnModalSave.addEventListener('click', async () => {
    const raw = document.getElementById('modal-raw').value.trim();
    if (!raw) return;
    const status = document.getElementById('modal-status');
    status.textContent = 'Добавляем...';
    status.className = 'status';
    const r = await api('/api/proxies/add', 'POST', { raw });
    if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
    else { status.textContent = 'Добавлено!'; status.className = 'status ok';
        setTimeout(async () => { document.getElementById('modal-add').style.display = 'none'; await loadState(); }, 500); }
});

function outboundDisplayName(tag) {
    if (!tag) return '';
    if (tag === 'direct') return 'direct (напрямую)';
    if (tag === 'block') return 'block (блокировка)';
    if (tag.startsWith('proxy-')) {
        const inner = tag.slice(6);
        let p = state.proxies.find(x => x.tag === inner);
        if (p) return p.name || p.tag;
        if (inner === 'default') return 'default (общая группа)';
        const cat = catalogCache.find(c => c.id === inner);
        if (cat) return cat.name;
        const cr = state.custom_rules.find(r => r.id === inner);
        if (cr) return cr.name || cr.id;
        const cs = state.custom_srs.find(s => s.id === inner);
        if (cs) return cs.name || cs.id;
        const g = state.groups[inner];
        if (g && g.label) return g.label;
        const m = inner.match(/^(\d+)$/);
        if (m) {
            const idx = parseInt(m[1]) - 1;
            if (idx >= 0 && idx < state.proxies.length) return state.proxies[idx].name || state.proxies[idx].tag;
        }
        return inner;
    }
    return tag;
}

function allOutboundOptions() {
    const opts = [];
    opts.push({ value: '', label: '— группа по умолчанию —' });
    opts.push({ value: 'direct', label: '🚫 direct (напрямую)' });
    opts.push({ value: 'block', label: '⛔ block (блокировка)' });
    opts.push({ value: 'proxy-default', label: '⭐ default (общая группа)' });
    for (const id of state.enabled) {
        const cat = catalogCache.find(c => c.id === id);
        opts.push({ value: 'proxy-' + id, label: '📦 ' + (cat ? cat.name : id) });
    }
    for (const [name, g] of Object.entries(state.groups)) {
        if (g.manual) opts.push({ value: 'proxy-' + name, label: '🔧 ' + (g.label || name) });
    }
    return opts;
}

async function refreshMonitor() {
    try {
        const r = await api('/api/monitor');
        if (r.error || !r.proxies) return;
        for (const p of state.proxies) {
            const s = r.proxies[p.tag];
            if (!s) continue;
            addDelayToHistory(p.tag, s.alive ? s.delay : 0);
            const card = document.querySelector(`.srv-card[data-tag="${p.tag}"]`);
            if (card) {
                const delayTop = card.querySelector('[data-delay-top]');
                const delayEl = card.querySelector('[data-delay]');
                const histoEl = card.querySelector('.srv-histo');
                const stats = serverStats[p.tag] || { avg: 0 };
                if (s.alive) {
                    if (delayTop) delayTop.textContent = s.delay + 'ms';
                    if (delayEl) delayEl.innerHTML = `<span>${s.delay} ms</span><span class="srv-info-sub">avg ${stats.avg}ms</span>`;
                } else {
                    if (delayTop) delayTop.textContent = '—';
                    if (delayEl) delayEl.textContent = '—';
                }
                if (histoEl) histoEl.innerHTML = renderHistogram(serverDelayHistory[p.tag] || [], 20);
            }
        }
        if (currentTab !== 'monitor') return;
        const container = document.getElementById('monitor');
        container.innerHTML = '';
        const groupDefs = [];
        groupDefs.push({ tag: 'proxy-default', name: 'default (остальное)' });
        for (const id of state.enabled) { const cat = catalogCache.find(c => c.id === id); groupDefs.push({ tag: 'proxy-' + id, name: cat ? cat.name : id }); }
        for (const cr of state.custom_rules) groupDefs.push({ tag: 'proxy-' + cr.id, name: (cr.name || cr.id) + ' — своё правило' });
        for (const cs of state.custom_srs) groupDefs.push({ tag: 'proxy-' + cs.id, name: (cs.name || cs.id) + ' — свой .srs' });
        for (const [name, g] of Object.entries(state.groups)) if (g.manual) groupDefs.push({ tag: 'proxy-' + name, name: '🔧 ' + (g.label || name) });

        for (const def of groupDefs) {
            const s = r.proxies[def.tag];
            if (!s) continue;
            const expanded = expandedGroups[def.tag];
            const card = document.createElement('div');
            card.className = 'monitor-group';
            const currentServerName = s.now ? outboundDisplayName(s.now) : '—';
            const currentAlive = s.now && r.proxies[s.now] ? r.proxies[s.now].alive : false;
            const currentDelay = s.now && r.proxies[s.now] ? r.proxies[s.now].delay : 0;
            const delayClass = currentDelay < 200 ? 'good' : currentDelay < 500 ? 'mid' : 'bad';
            card.innerHTML = `
                <div class="monitor-group-head">
                    <span class="monitor-group-expand">${expanded ? '▼' : '▶'}</span>
                    <div class="monitor-group-info">
                        <div class="monitor-group-name">${escapeHtml(def.name)}</div>
                        <div class="monitor-group-now">Сейчас: <b>${escapeHtml(currentServerName)}</b></div>
                    </div>
                    <div class="monitor-delay ${delayClass}">${currentAlive ? currentDelay + ' ms' : '—'}</div>
                </div>
            `;
            if (expanded) {
                const body = document.createElement('div');
                body.className = 'monitor-group-body';
                const allTags = s.all || [];
                for (const tag of allTags) {
                    const ps = r.proxies[tag];
                    if (!ps) continue;
                    const isCurrent = tag === s.now;
                    const row = document.createElement('div');
                    row.className = 'monitor-server-row' + (isCurrent ? ' current' : '');
                    let rowDelayClass = 'bad';
                    if (ps.delay < 200) rowDelayClass = 'good';
                    else if (ps.delay < 500) rowDelayClass = 'mid';
                    row.innerHTML = `
                        <div class="monitor-server-icon">${ps.alive ? '🟢' : '🔴'}</div>
                        <div class="monitor-server-name">${escapeHtml(outboundDisplayName(tag))}${isCurrent ? ' <span class="monitor-current-badge">текущий</span>' : ''}</div>
                        <div class="monitor-delay ${rowDelayClass}">${ps.alive ? ps.delay + ' ms' : '—'}</div>
                    `;
                    body.appendChild(row);
                }
                card.appendChild(body);
            }
            card.querySelector('.monitor-group-head').addEventListener('click', () => {
                expandedGroups[def.tag] = !expandedGroups[def.tag];
                refreshMonitor();
            });
            container.appendChild(card);
        }
    } catch (e) {}
}

// --- Groups ---
function renderGroups() {
    const container = document.getElementById('groups');
    if (!container) return;
    container.innerHTML = '';
    const displayNames = { 'default': 'default (остальное)' };
    for (const id of state.enabled) { const item = catalogCache.find(c => c.id === id); displayNames[id] = item ? item.name : id; }
    for (const r of state.custom_rules) if (r.id) displayNames[r.id] = r.name || r.id;
    for (const sr of state.custom_srs) if (sr.id) displayNames[sr.id] = sr.name || sr.id;
    for (const [name, g] of Object.entries(state.groups)) if (g.manual) displayNames[name] = '🔧 ' + (g.label || name);

    const allGroups = ['default', ...state.enabled,
        ...state.custom_rules.map(r => r.id).filter(Boolean),
        ...state.custom_srs.map(s => s.id).filter(Boolean),
        ...Object.keys(state.groups).filter(n => state.groups[n].manual)];

    const allTags = state.proxies.map(p => p.tag);

    for (const groupName of allGroups) {
        const grp = state.groups[groupName] || { proxies: [] };
        const groupTags = (grp.proxies || []).map(x => x.tag);
        for (const t of allTags) if (!groupTags.includes(t)) { grp.proxies = grp.proxies || []; grp.proxies.push({ tag: t, enabled: false }); }
        state.groups[groupName] = grp;

        const card = document.createElement('div');
        card.className = 'group-card';
        const title = displayNames[groupName] || groupName;
        const enabledCount = (grp.proxies || []).filter(p => p.enabled).length;
        const isManual = grp.manual;
        card.innerHTML = `
            <div class="group-header">
                <span>${escapeHtml(title)}</span>
                <span class="group-header-count">${enabledCount} вкл.
                    ${isManual ? '<button class="group-remove-btn" title="Удалить группу" data-name="' + escapeHtml(groupName) + '">✕</button>' : ''}
                </span>
            </div>
        `;
        const body = document.createElement('div');
        body.className = 'group-body';
        (grp.proxies || []).forEach((gp, idx) => {
            const p = state.proxies.find(x => x.tag === gp.tag);
            const displayName = p ? (p.name || p.tag) : gp.tag;
            const row = document.createElement('div');
            row.className = 'group-item' + (gp.enabled ? '' : ' disabled');
            row.innerHTML = `
                <input type="checkbox" ${gp.enabled ? 'checked' : ''}>
                <span class="group-item-name">${escapeHtml(displayName)}</span>
                <div class="group-item-actions">
                    <button class="btn-up" ${idx === 0 ? 'disabled' : ''}>↑</button>
                    <button class="btn-down" ${idx === grp.proxies.length - 1 ? 'disabled' : ''}>↓</button>
                </div>
            `;
            row.querySelector('input').addEventListener('change', async (e) => {
                gp.enabled = e.target.checked;
                renderGroups();
                renderCatalog();
                try { await api('/api/groups', 'POST', { groups: state.groups }); } catch (err) {}
            });
            row.querySelector('.btn-up').addEventListener('click', () => moveItem(grp.proxies, idx, -1, groupName));
            row.querySelector('.btn-down').addEventListener('click', () => moveItem(grp.proxies, idx, +1, groupName));
            body.appendChild(row);
        });
        card.appendChild(body);
        if (isManual) {
            card.querySelector('.group-remove-btn').addEventListener('click', async (e) => {
                e.stopPropagation();
                if (!confirm('Удалить группу "' + (grp.label || groupName) + '"?')) return;
                const r = await api('/api/groups/remove', 'POST', { name: groupName });
                if (r.error) { alert('Ошибка: ' + r.error); return; }
                await loadState();
            });
        }
        container.appendChild(card);
    }
}

async function moveItem(arr, idx, dir, groupName) {
    const newIdx = idx + dir;
    if (newIdx < 0 || newIdx >= arr.length) return;
    [arr[idx], arr[newIdx]] = [arr[newIdx], arr[idx]];
    state.groups[groupName].proxies = arr;
    renderGroups();
    try { await api('/api/groups', 'POST', { groups: state.groups }); } catch (e) {}
}

async function saveGroups() {
    const btn = document.getElementById('btn-save-groups');
    const status = document.getElementById('groups-status');
    btn.disabled = true; status.textContent = 'Сохраняем...';
    try {
        const r = await api('/api/groups', 'POST', { groups: state.groups });
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else { status.textContent = 'Сохранено'; status.className = 'status ok'; }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

async function addManualGroup() {
    const name = prompt('Имя группы (латиница, без пробелов):');
    if (!name) return;
    const label = prompt('Человеческое название:', name) || name;
    const r = await api('/api/groups/add', 'POST', { name, label });
    if (r.error) { alert('Ошибка: ' + r.error); return; }
    await loadState();
}

// --- Devices ---
function renderDevices() {
    const container = document.getElementById('devices-list');
    if (!container) return;
    container.innerHTML = '';
    const outboundOpts = allOutboundOptions();
    state.devices.forEach((d, i) => {
        const card = document.createElement('div');
        card.className = 'device-card';
        const opts = outboundOpts.map(o => `<option value="${o.value}" ${d.outbound === o.value ? 'selected' : ''}>${escapeHtml(o.label)}</option>`).join('');
        card.innerHTML = `
            <div class="device-field"><div class="device-field-label">IP адрес</div><input type="text" value="${escapeHtml(d.ip || '')}" placeholder="192.168.1.100" data-field="ip"></div>
            <div class="device-field"><div class="device-field-label">Маршрут</div><select data-field="outbound">${opts}</select></div>
            <div class="device-field"><div class="device-field-label">Описание</div><input type="text" value="${escapeHtml(d.remark || '')}" placeholder="Телефон" data-field="remark"></div>
            <button class="custom-card-remove" title="Удалить">✕</button>
        `;
        card.querySelector('[data-field="ip"]').addEventListener('input', e => d.ip = e.target.value);
        card.querySelector('[data-field="outbound"]').addEventListener('change', e => d.outbound = e.target.value);
        card.querySelector('[data-field="remark"]').addEventListener('input', e => d.remark = e.target.value);
        card.querySelector('.custom-card-remove').addEventListener('click', () => { state.devices.splice(i, 1); renderDevices(); updateBadges(); });
        container.appendChild(card);
    });
    updateBadges();
}

function addDevice() {
    state.devices.push({ id: 'dev-' + Date.now(), ip: '', outbound: 'proxy-default', enabled: true, remark: '' });
    renderDevices();
}

async function saveDevices() {
    const btn = document.getElementById('btn-save-devices');
    const status = document.getElementById('devices-status');
    btn.disabled = true; status.textContent = 'Сохраняем...';
    try {
        const r = await api('/api/devices', 'POST', { devices: state.devices });
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else { status.textContent = 'Сохранено'; status.className = 'status ok'; }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

// --- DNS ---
function renderDNS() {
    const container = document.getElementById('dns-list');
    if (!container) return;
    container.innerHTML = '';
    if (!state.dns || !state.dns.servers) { container.innerHTML = '<div class="section-hint">DNS не настроен.</div>'; return; }
    state.dns.servers.forEach((s, i) => {
        const card = document.createElement('div');
        card.className = 'dns-card';
        const isDefault = s.tag === state.dns.default_server;
        card.innerHTML = `
            <div class="dns-card-head">
                <input type="text" placeholder="Тег" value="${escapeHtml(s.tag)}" data-field="tag">
                ${isDefault ? '<span class="dns-default-badge">default</span>' : ''}
                <button class="dns-card-remove" data-action="remove">✕</button>
            </div>
            <div class="dns-fields">
                <div class="dns-field"><div class="dns-field-label">Тип</div>
                    <select data-field="type">
                        <option value="udp" ${s.type === 'udp' ? 'selected' : ''}>UDP</option>
                        <option value="tcp" ${s.type === 'tcp' ? 'selected' : ''}>TCP</option>
                        <option value="tls" ${s.type === 'tls' ? 'selected' : ''}>DoT</option>
                        <option value="https" ${s.type === 'https' ? 'selected' : ''}>DoH</option>
                        <option value="quic" ${s.type === 'quic' ? 'selected' : ''}>DoQ</option>
                    </select>
                </div>
                <div class="dns-field"><div class="dns-field-label">Адрес</div><input type="text" value="${escapeHtml(s.server)}" data-field="server"></div>
                <div class="dns-field"><div class="dns-field-label">Порт</div><input type="number" value="${s.port || ''}" data-field="port"></div>
            </div>
            <div style="display:flex;gap:18px;margin-top:10px;flex-wrap:wrap;">
                <label style="display:flex;align-items:center;gap:6px;font-size:12px;color:var(--text-dim);cursor:pointer;"><input type="checkbox" ${s.enabled ? 'checked' : ''} data-field="enabled"> Включён</label>
                <label style="display:flex;align-items:center;gap:6px;font-size:12px;color:var(--text-dim);cursor:pointer;"><input type="checkbox" ${s.via_vpn ? 'checked' : ''} data-field="via_vpn"> Через VPN</label>
                <label style="display:flex;align-items:center;gap:6px;font-size:12px;color:var(--text-dim);cursor:pointer;"><input type="radio" name="default-dns" ${isDefault ? 'checked' : ''} data-action="set-default"> По умолчанию</label>
            </div>
            <div class="dns-field" style="margin-top:8px;"><div class="dns-field-label">Описание</div><input type="text" value="${escapeHtml(s.remark || '')}" data-field="remark"></div>
        `;
        card.querySelectorAll('[data-field]').forEach(el => {
            const field = el.dataset.field;
            const handler = () => {
                if (el.type === 'checkbox') s[field] = el.checked;
                else if (el.type === 'number') s[field] = parseInt(el.value) || 0;
                else s[field] = el.value;
            };
            el.addEventListener('change', handler);
            el.addEventListener('input', handler);
        });
        card.querySelector('[data-action="remove"]').addEventListener('click', () => {
            state.dns.servers.splice(i, 1);
            if (s.tag === state.dns.default_server && state.dns.servers.length > 0) state.dns.default_server = state.dns.servers[0].tag;
            renderDNS();
        });
        card.querySelector('[data-action="set-default"]').addEventListener('change', () => { state.dns.default_server = s.tag; renderDNS(); });
        container.appendChild(card);
    });
}

function addDNS() {
    state.dns.servers.push({ tag: 'dns-' + Date.now().toString().slice(-6), type: 'udp', server: '', port: 0, enabled: true, via_vpn: false, remark: '' });
    renderDNS();
}

async function saveDNS() {
    const btn = document.getElementById('btn-save-dns');
    const status = document.getElementById('dns-status');
    btn.disabled = true; status.textContent = 'Сохраняем...';
    try {
        const r = await api('/api/dns', 'POST', state.dns);
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else { status.textContent = 'Сохранено'; status.className = 'status ok'; }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

// --- Custom rules ---
function renderCustomRules() {
    const container = document.getElementById('custom-rules-list');
    if (!container) return;
    container.innerHTML = '';
    const outboundOpts = allOutboundOptions();
    state.custom_rules.forEach((r, i) => {
        const expanded = expandedRules[r.id] === true;
        const domainsCount = (r.domains || []).length;
        const cidrsCount = (r.ip_cidrs || []).length;
        const enabled = r.enabled !== false;
        const routeLabel = r.outbound ? outboundDisplayName(r.outbound) : 'группа по умолчанию';
        const card = document.createElement('div');
        card.className = 'rule-card' + (expanded ? ' expanded' : '') + (enabled ? '' : ' disabled');
        const summary = `
            <div class="rule-card-head">
                <span class="rule-card-expand">${expanded ? '▼' : '▶'}</span>
                <span class="rule-card-icon">📝</span>
                <div class="rule-card-info">
                    <div class="rule-card-title">${escapeHtml(r.name || 'Без названия')}</div>
                    <div class="rule-card-sub">${domainsCount} доменов · ${cidrsCount} CIDR · → ${escapeHtml(routeLabel)}</div>
                </div>
                <label class="toggle" onclick="event.stopPropagation();">
                    <input type="checkbox" ${enabled ? 'checked' : ''} class="rule-enabled">
                    <span class="toggle-slider"></span>
                </label>
                <button class="rule-card-delete" title="Удалить">✕</button>
            </div>
        `;
        let bodyHtml = '';
        if (expanded) {
            const optsHtml = outboundOpts.map(o => `<option value="${o.value}" ${r.outbound === o.value ? 'selected' : ''}>${escapeHtml(o.label)}</option>`).join('');
            bodyHtml = `
                <div class="rule-card-body">
                    <div class="custom-field"><div class="custom-field-label">Название правила</div><input type="text" value="${escapeHtml(r.name || '')}" data-field="name" placeholder="Название"></div>
                    <div class="custom-field"><div class="custom-field-label">Маршрут <b>— куда уходит трафик</b></div><select data-field="outbound">${optsHtml}</select></div>
                    <div class="custom-field"><div class="custom-field-label">Домены (по одному в строке)</div><textarea data-field="domains">${escapeHtml((r.domains || []).join('\n'))}</textarea></div>
                    <div class="custom-field"><div class="custom-field-label">CIDR (по одному в строке)</div><textarea data-field="ip_cidrs">${escapeHtml((r.ip_cidrs || []).join('\n'))}</textarea></div>
                </div>
            `;
        }
        card.innerHTML = summary + bodyHtml;
        card.querySelector('.rule-card-head').addEventListener('click', () => { expandedRules[r.id] = !expanded; renderCustomRules(); });
        card.querySelector('.rule-card-delete').addEventListener('click', (e) => { e.stopPropagation(); state.custom_rules.splice(i, 1); renderCustomRules(); updateBadges(); });
        card.querySelector('.rule-enabled').addEventListener('change', (e) => { e.stopPropagation(); r.enabled = e.target.checked; card.classList.toggle('disabled', !r.enabled); });
        if (expanded) {
            card.querySelector('[data-field="name"]').addEventListener('input', e => r.name = e.target.value);
            card.querySelector('[data-field="outbound"]').addEventListener('change', e => r.outbound = e.target.value);
            card.querySelector('[data-field="domains"]').addEventListener('input', e => { r.domains = e.target.value.split('\n').map(s => s.trim()).filter(Boolean); });
            card.querySelector('[data-field="ip_cidrs"]').addEventListener('input', e => { r.ip_cidrs = e.target.value.split('\n').map(s => s.trim()).filter(Boolean); });
        }
        container.appendChild(card);
    });
    updateBadges();
}

function addCustomRule() {
    const id = 'custom-' + Date.now();
    state.custom_rules.push({ id, name: '', domains: [], ip_cidrs: [], outbound: '', enabled: true });
    expandedRules[id] = true;
    renderCustomRules();
}

async function saveCustomRules() {
    const btn = document.getElementById('btn-save-rules');
    const status = document.getElementById('rules-status');
    btn.disabled = true; status.textContent = 'Сохраняем...';
    try {
        const r = await api('/api/custom-rules', 'POST', { rules: state.custom_rules });
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else {
            status.textContent = 'Сохранено'; status.className = 'status ok';
            const s = await api('/api/state');
            state.groups = s.groups || {};
            state.custom_rules = s.custom_rules || [];
            renderGroups(); renderCatalog(); updateBadges();
        }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

// --- Custom SRS ---
function renderCustomSRS() {
    const container = document.getElementById('custom-srs-list');
    if (!container) return;
    container.innerHTML = '';
    const outboundOpts = allOutboundOptions();
    state.custom_srs.forEach((s, i) => {
        const expanded = expandedSRS[s.id] === true;
        const enabled = s.enabled !== false;
        const routeLabel = s.outbound ? outboundDisplayName(s.outbound) : 'группа по умолчанию';
        const card = document.createElement('div');
        card.className = 'rule-card' + (expanded ? ' expanded' : '') + (enabled ? '' : ' disabled');
        const summary = `
            <div class="rule-card-head">
                <span class="rule-card-expand">${expanded ? '▼' : '▶'}</span>
                <span class="rule-card-icon">🌍</span>
                <div class="rule-card-info">
                    <div class="rule-card-title">${escapeHtml(s.name || 'Без названия')}</div>
                    <div class="rule-card-sub">${escapeHtml(s.url || '—')} · → ${escapeHtml(routeLabel)}</div>
                </div>
                <label class="toggle" onclick="event.stopPropagation();">
                    <input type="checkbox" ${enabled ? 'checked' : ''} class="rule-enabled">
                    <span class="toggle-slider"></span>
                </label>
                <button class="rule-card-delete" title="Удалить">✕</button>
            </div>
        `;
        let bodyHtml = '';
        if (expanded) {
            const optsHtml = outboundOpts.map(o => `<option value="${o.value}" ${s.outbound === o.value ? 'selected' : ''}>${escapeHtml(o.label)}</option>`).join('');
            bodyHtml = `
                <div class="rule-card-body">
                    <div class="custom-field"><div class="custom-field-label">Название</div><input type="text" value="${escapeHtml(s.name || '')}" data-field="name"></div>
                    <div class="custom-field"><div class="custom-field-label">URL на .srs</div><input type="text" value="${escapeHtml(s.url || '')}" data-field="url" placeholder="https://example.com/list.srs"></div>
                    <div class="custom-field"><div class="custom-field-label">Маршрут <b>— куда уходит трафик</b></div><select data-field="outbound">${optsHtml}</select></div>
                </div>
            `;
        }
        card.innerHTML = summary + bodyHtml;
        card.querySelector('.rule-card-head').addEventListener('click', () => { expandedSRS[s.id] = !expanded; renderCustomSRS(); });
        card.querySelector('.rule-card-delete').addEventListener('click', (e) => { e.stopPropagation(); state.custom_srs.splice(i, 1); renderCustomSRS(); updateBadges(); });
        card.querySelector('.rule-enabled').addEventListener('change', (e) => { e.stopPropagation(); s.enabled = e.target.checked; card.classList.toggle('disabled', !s.enabled); });
        if (expanded) {
            card.querySelector('[data-field="name"]').addEventListener('input', e => s.name = e.target.value);
            card.querySelector('[data-field="url"]').addEventListener('input', e => s.url = e.target.value);
            card.querySelector('[data-field="outbound"]').addEventListener('change', e => s.outbound = e.target.value);
        }
        container.appendChild(card);
    });
    updateBadges();
}

function addCustomSRS() {
    const id = 'srs-' + Date.now();
    state.custom_srs.push({ id, name: '', url: '', outbound: '', enabled: true });
    expandedSRS[id] = true;
    renderCustomSRS();
}

async function saveCustomSRS() {
    const btn = document.getElementById('btn-save-srs');
    const status = document.getElementById('srs-status');
    btn.disabled = true; status.textContent = 'Сохраняем...';
    try {
        const r = await api('/api/custom-srs', 'POST', { items: state.custom_srs });
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else {
            status.textContent = 'Сохранено'; status.className = 'status ok';
            const s = await api('/api/state');
            state.groups = s.groups || {};
            state.custom_srs = s.custom_srs || [];
            renderGroups(); renderCatalog(); updateBadges();
        }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

// --- Sync / Settings ---
function renderSync() {
    if (!state.settings) return;
    const el1 = document.getElementById('sync-enabled');
    const el2 = document.getElementById('sync-interval');
    if (el1) el1.checked = !!state.settings.auto_sync_enabled;
    if (el2) el2.value = state.settings.auto_sync_interval || 24;
}

async function saveSync() {
    const btn = document.getElementById('btn-save-sync');
    const status = document.getElementById('sync-status');
    btn.disabled = true; status.textContent = 'Сохраняем...';
    try {
        state.settings.auto_sync_enabled = document.getElementById('sync-enabled').checked;
        state.settings.auto_sync_interval = parseInt(document.getElementById('sync-interval').value) || 24;
        const r = await api('/api/settings', 'POST', state.settings);
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else { status.textContent = 'Сохранено'; status.className = 'status ok'; }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

async function syncNow() {
    const btn = document.getElementById('btn-sync-now');
    const status = document.getElementById('sync-status');
    btn.disabled = true; status.textContent = 'Скачиваем...';
    try {
        const r = await api('/api/apply', 'POST');
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else { status.textContent = `Обновлено списков: ${r.lists}`; status.className = 'status ok'; }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

function renderSettings() {
    if (!state.settings) return;
    const el1 = document.getElementById('log-enabled');
    const el2 = document.getElementById('log-ring-size');
    const el3 = document.getElementById('restart-on-apply');
    if (el1) el1.checked = !!state.settings.log_enabled;
    if (el2) el2.value = state.settings.log_ring_size || 2000;
    if (el3) el3.checked = !!state.settings.restart_on_apply;
    renderSync();
}

async function saveSettings() {
    const btn = document.getElementById('btn-save-settings');
    const status = document.getElementById('settings-status');
    btn.disabled = true; status.textContent = 'Сохраняем...';
    try {
        state.settings.log_enabled = document.getElementById('log-enabled').checked;
        state.settings.log_ring_size = parseInt(document.getElementById('log-ring-size').value) || 2000;
        state.settings.restart_on_apply = document.getElementById('restart-on-apply').checked;
        const r = await api('/api/settings', 'POST', state.settings);
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else { status.textContent = 'Сохранено'; status.className = 'status ok'; }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

function exportState() { window.location.href = '/api/export'; }

async function importState(file) {
    const text = await file.text();
    const r = await fetch('/api/import', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: text });
    const j = await r.json();
    if (j.error) alert('Ошибка импорта: ' + j.error);
    else { alert('Импорт успешен'); await loadState(); }
}

async function refreshLogs() {
    try {
        const r = await api('/api/logs');
        const content = document.getElementById('logs-content');
        if (!content) return;
        if (r.text) { content.textContent = r.text; content.scrollTop = content.scrollHeight; }
        else content.textContent = 'Журнал пуст.';
    } catch (e) {}
}

const btnLogsAuto = document.getElementById('btn-logs-auto');
if (btnLogsAuto) btnLogsAuto.addEventListener('click', () => {
    logsAuto = !logsAuto;
    btnLogsAuto.textContent = logsAuto ? '⏸ Остановить автообновление' : '▶ Включить автообновление';
});

const btnLogsClear = document.getElementById('btn-logs-clear');
if (btnLogsClear) btnLogsClear.addEventListener('click', async () => {
    if (!confirm('Очистить журнал?')) return;
    await api('/api/logs', 'DELETE');
    document.getElementById('logs-content').textContent = 'Журнал очищен.';
});

async function apply() {
    const btn = document.getElementById('btn-apply');
    const status = document.getElementById('apply-status');
    btn.disabled = true; status.textContent = 'Сохраняем настройки...'; status.className = 'status';
    try {
        await api('/api/groups', 'POST', { groups: state.groups });
        if (state.custom_rules) await api('/api/custom-rules', 'POST', { rules: state.custom_rules });
        if (state.custom_srs)   await api('/api/custom-srs',   'POST', { items: state.custom_srs });
        if (state.devices)      await api('/api/devices',      'POST', { devices: state.devices });
        if (state.dns)          await api('/api/dns',          'POST', state.dns);
        if (state.settings)     await api('/api/settings',     'POST', state.settings);
        status.textContent = 'Применяем...';
        const r = await api('/api/apply', 'POST');
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else { status.textContent = `Готово! Списков: ${r.lists}, серверов: ${r.proxies}`; status.className = 'status ok'; setTimeout(refreshMonitor, 6000); }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

async function restart() {
    const btn = document.getElementById('btn-restart');
    const status = document.getElementById('apply-status');
    btn.disabled = true; status.textContent = 'Рестарт...'; status.className = 'status';
    try {
        const r = await api('/api/restart', 'POST');
        if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
        else { status.textContent = 'Sing-box перезапущен'; status.className = 'status ok'; setTimeout(refreshMonitor, 4000); }
    } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
    finally { btn.disabled = false; }
}

function escapeHtml(s) {
    if (s === undefined || s === null) return '';
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// --- Auth: кнопка «Выйти» ---
function addAuthButtons() {
    const topActions = document.querySelector('.topbar-actions');
    if (!topActions || document.getElementById('btn-logout')) return;
    const logout = document.createElement('button');
    logout.id = 'btn-logout';
    logout.className = 'btn';
    logout.title = 'Выйти';
    logout.textContent = '🚪 Выйти';
    logout.addEventListener('click', async () => {
        await fetch('/api/auth/logout', { method: 'POST' });
        window.location.href = '/';
    });
    topActions.appendChild(logout);
}

// --- Смена пароля ---
function initPasswordChange() {
    const btnChangePw = document.getElementById('btn-change-password');
    if (!btnChangePw) return;
    btnChangePw.addEventListener('click', async () => {
        const oldPw = document.getElementById('pw-old').value;
        const newPw = document.getElementById('pw-new').value;
        const newPw2 = document.getElementById('pw-new2').value;
        const status = document.getElementById('pw-status');
        if (!oldPw || !newPw) { status.textContent = 'Заполни все поля'; status.className = 'status err'; return; }
        if (newPw !== newPw2) { status.textContent = 'Новые пароли не совпадают'; status.className = 'status err'; return; }
        if (newPw.length < 4) { status.textContent = 'Пароль должен быть не короче 4 символов'; status.className = 'status err'; return; }
        btnChangePw.disabled = true; status.textContent = 'Меняем...'; status.className = 'status';
        try {
            const r = await api('/api/auth/change-password', 'POST', { old_password: oldPw, new_password: newPw });
            if (r.error) { status.textContent = 'Ошибка: ' + r.error; status.className = 'status err'; }
            else {
                status.textContent = 'Пароль изменён! Перенаправление...';
                status.className = 'status ok';
                setTimeout(() => { window.location.href = '/'; }, 1500);
            }
        } catch (e) { status.textContent = 'Ошибка: ' + e.message; status.className = 'status err'; }
        finally { btnChangePw.disabled = false; }
    });
}

// --- Handlers ---
const handlers = {
    'btn-save-groups': saveGroups,
    'btn-apply': apply,
    'btn-restart': restart,
    'btn-add-rule': addCustomRule,
    'btn-save-rules': saveCustomRules,
    'btn-add-srs': addCustomSRS,
    'btn-save-srs': saveCustomSRS,
    'btn-add-dns': addDNS,
    'btn-save-dns': saveDNS,
    'btn-add-device': addDevice,
    'btn-save-devices': saveDevices,
    'btn-save-settings': saveSettings,
    'btn-save-sync': saveSync,
    'btn-sync-now': syncNow,
    'btn-export': exportState,
    'btn-add-group': addManualGroup,
};
for (const [id, fn] of Object.entries(handlers)) {
    const el = document.getElementById(id);
    if (el) el.addEventListener('click', fn);
}

const fileImport = document.getElementById('file-import');
if (fileImport) fileImport.addEventListener('change', (e) => { if (e.target.files[0]) importState(e.target.files[0]); });

// --- Инициализация ---
document.querySelectorAll('[data-theme-btn]').forEach(btn => {
    btn.addEventListener('click', async () => {
        const t = btn.dataset.themeBtn;
        state.theme = t;
        applyTheme(t);
        await api('/api/theme', 'POST', { theme: t });
    });
});

window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (state.theme === 'auto') applyTheme('auto');
});

document.querySelectorAll('.nav-tab').forEach(t => {
    t.addEventListener('click', () => switchTab(t.dataset.tab));
});

// Перехват 401 — сессия истекла
const _origFetch = window.fetch;
window.fetch = async function(...args) {
    const r = await _origFetch.apply(this, args);
    if (r.status === 401 && !String(args[0]).includes('/api/auth/')) {
        window.location.href = '/';
    }
    return r;
};

initSidebarCollapse();
initMobileMenu();
addAuthButtons();
initPasswordChange();

loadState();
