// vpn_ui.js — UI для Stop/Start VPN и проверки firewall.
//
// ВАЖНО: vpn_ui.js НЕ полагается на state.vpn_enabled из app.js,
// потому что app.js это поле не загружает. Вместо этого — запрашиваем
// /api/vpn/status самостоятельно.

(function () {
    'use strict';

    let lastEnabled = null;

    // --- Запрос статуса VPN ---

    async function fetchVPNStatus() {
        try {
            const r = await api('/api/vpn/status');
            if (r && typeof r.enabled === 'boolean') {
                return r.enabled;
            }
        } catch (_) { /* ignore */ }
        return null;
    }

    // --- Отрисовка карточки ---

    function renderVPNStatus(enabled) {
        const dot = document.getElementById('vpn-status-dot');
        const title = document.getElementById('vpn-status-title');
        const sub = document.getElementById('vpn-status-sub');
        const toggleBtn = document.getElementById('btn-vpn-toggle');

        if (!dot || !title || !sub || !toggleBtn) return;

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

    // --- Обновление с сервера ---

    async function refreshVPNStatus() {
        const enabled = await fetchVPNStatus();
        if (enabled === null) return;
        lastEnabled = enabled;
        renderVPNStatus(enabled);
    }

    // --- Обработчик Stop/Start ---

    async function handleVPNToggle() {
        const btn = document.getElementById('btn-vpn-toggle');
        const wasEnabled = !!lastEnabled;

        if (btn) {
            btn.disabled = true;
            btn.textContent = wasEnabled ? '⏸ Останавливаю...' : '▶ Запускаю...';
        }

        try {
            const endpoint = wasEnabled ? '/api/vpn/stop' : '/api/vpn/start';
            const r = await api(endpoint, 'POST');
            if (r && r.error) {
                alert('Ошибка: ' + r.error);
            }
            // Запросить актуальный статус через 1 секунду.
            setTimeout(refreshVPNStatus, 1000);
        } catch (e) {
            alert('Ошибка сети: ' + e.message);
        } finally {
            if (btn) btn.disabled = false;
            // Обновить статус сразу.
            setTimeout(refreshVPNStatus, 1500);
        }
    }

    // --- Проверка firewall ---

    async function handleFirewallCheck() {
        const btn1 = document.getElementById('btn-firewall-check');
        const btn2 = document.getElementById('btn-settings-firewall-check');
        const status1 = document.getElementById('apply-status');
        const status2 = document.getElementById('firewall-check-status');

        if (btn1) btn1.disabled = true;
        if (btn2) btn2.disabled = true;
        if (status1) status1.textContent = 'Проверяю правила...';
        if (status2) status2.textContent = 'Проверяю правила...';

        try {
            const r = await api('/api/firewall/check', 'POST');
            let msg = 'OK';
            if (r && r.error) {
                msg = 'Ошибка: ' + r.error;
            } else if (r && r.message) {
                msg = r.message;
            }
            if (status1) {
                status1.textContent = msg;
                setTimeout(() => { status1.textContent = ''; }, 5000);
            }
            if (status2) {
                status2.textContent = msg;
                setTimeout(() => { status2.textContent = ''; }, 5000);
            }
        } catch (e) {
            const msg = 'Ошибка сети: ' + e.message;
            if (status1) status1.textContent = msg;
            if (status2) status2.textContent = msg;
        } finally {
            if (btn1) btn1.disabled = false;
            if (btn2) btn2.disabled = false;
        }
    }

    // --- Инициализация ---

    function init() {
        const toggle = document.getElementById('btn-vpn-toggle');
        if (toggle) toggle.addEventListener('click', handleVPNToggle);

        const check1 = document.getElementById('btn-firewall-check');
        if (check1) check1.addEventListener('click', handleFirewallCheck);

        const check2 = document.getElementById('btn-settings-firewall-check');
        if (check2) check2.addEventListener('click', handleFirewallCheck);

        // Первый запрос через 500 мс.
        setTimeout(refreshVPNStatus, 500);

        // Периодический опрос каждые 5 секунд.
        setInterval(refreshVPNStatus, 5000);
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
