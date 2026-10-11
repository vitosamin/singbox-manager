// vpn_ui.js — UI для Stop/Start VPN и проверки firewall.
//
// Использует глобальные функции из app.js: api, state, applyTheme.
// Подключается после app.js в index.html.

(function () {
    'use strict';

    // --- Рендер карточки статуса ---

    function renderVPNStatus() {
        const card = document.getElementById('vpn-status-card');
        const dot = document.getElementById('vpn-status-dot');
        const title = document.getElementById('vpn-status-title');
        const sub = document.getElementById('vpn-status-sub');
        const toggleBtn = document.getElementById('btn-vpn-toggle');

        if (!card || !dot || !title || !sub || !toggleBtn) return;

        const enabled = !!(state && state.vpn_enabled);

        if (enabled) {
            dot.classList.remove('inactive');
            dot.classList.add('active');
            title.textContent = 'VPN активен';
            sub.textContent = 'sing-box 1.14.2';
            toggleBtn.textContent = '⏸ Остановить VPN';
            toggleBtn.classList.remove('btn-danger');
        } else {
            dot.classList.remove('active');
            dot.classList.add('inactive');
            title.textContent = 'VPN выключен';
            sub.textContent = 'правила сняты';
            toggleBtn.textContent = '▶ Запустить VPN';
            toggleBtn.classList.add('btn-danger');
        }
    }

    // --- Обработчик Stop/Start ---

    async function handleVPNToggle() {
        const enabled = !!(state && state.vpn_enabled);
        const endpoint = enabled ? '/api/vpn/stop' : '/api/vpn/start';

        const btn = document.getElementById('btn-vpn-toggle');
        if (btn) {
            btn.disabled = true;
            btn.textContent = enabled ? '⏸ Останавливаю...' : '▶ Запускаю...';
        }

        try {
            const r = await api(endpoint, 'POST');
            if (r && r.error) {
                alert('Ошибка: ' + r.error);
            } else {
                // Обновляем локальный state.
                if (state) state.vpn_enabled = !!r.enabled;
                renderVPNStatus();
                // Заодно перечитаем state с сервера, чтобы не было рассинхрона.
                await refreshStateQuiet();
            }
        } catch (e) {
            alert('Ошибка сети: ' + e.message);
        } finally {
            if (btn) btn.disabled = false;
            renderVPNStatus();
        }
    }

    // --- Проверка firewall ---

    async function handleFirewallCheck() {
        const btn = document.getElementById('btn-firewall-check');
        const status1 = document.getElementById('apply-status');
        const status2 = document.getElementById('firewall-check-status');

        if (btn) btn.disabled = true;
        if (status1) status1.textContent = 'Проверяю правила...';
        if (status2) status2.textContent = 'Проверяю правила...';

        try {
            const r = await api('/api/firewall/check', 'POST');
            if (r && r.error) {
                if (status1) status1.textContent = '';
                if (status2) status2.textContent = 'Ошибка: ' + r.error;
                return;
            }
            const msg = r.message || 'OK';
            if (status1) {
                status1.textContent = msg;
                setTimeout(() => { status1.textContent = ''; }, 5000);
            }
            if (status2) {
                status2.textContent = msg;
                setTimeout(() => { status2.textContent = ''; }, 5000);
            }
        } catch (e) {
            if (status1) status1.textContent = '';
            if (status2) status2.textContent = 'Ошибка сети: ' + e.message;
        } finally {
            if (btn) btn.disabled = false;
        }
    }

    // --- Тихая перечитка state ---

    async function refreshStateQuiet() {
        try {
            const fresh = await api('/api/state');
            if (fresh && !fresh.error) {
                if (typeof state !== 'undefined') {
                    Object.assign(state, fresh);
                }
                renderVPNStatus();
            }
        } catch (_) { /* ignore */ }
    }

    // --- Инициализация ---

    function init() {
        // Кнопка Stop/Start.
        const toggle = document.getElementById('btn-vpn-toggle');
        if (toggle) toggle.addEventListener('click', handleVPNToggle);

        // Кнопка «Проверить» в шапке.
        const check1 = document.getElementById('btn-firewall-check');
        if (check1) check1.addEventListener('click', handleFirewallCheck);

        // Кнопка «Проверить правила» в настройках.
        const check2 = document.getElementById('btn-settings-firewall-check');
        if (check2) check2.addEventListener('click', handleFirewallCheck);

        // Первый рендер (с задержкой, чтобы state успел подгрузиться).
        setTimeout(renderVPNStatus, 500);

        // Слушаем изменения state каждые 3 сек — на случай,
        // если пользователь выключил VPN с другого устройства.
        setInterval(() => {
            if (typeof state !== 'undefined' && state.vpn_enabled !== undefined) {
                renderVPNStatus();
            }
        }, 3000);
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
