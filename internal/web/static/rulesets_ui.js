// rulesets_ui.js — индикатор состояния кэша .srs + кнопки обновления.

(function () {
    'use strict';

    let refreshInProgress = false;

    async function renderRulesetsStatus() {
        const container = document.getElementById('rulesets-status-list');
        if (!container) return;

        try {
            const data = await api('/api/rulesets/status');
            if (data.error) {
                container.innerHTML = '<div style="color:#e74c3c">Ошибка: ' + escapeHtml(data.error) + '</div>';
                return;
            }

            if (!data.items || data.items.length === 0) {
                container.innerHTML = '<div style="color:var(--text-dim)">Нет включённых списков</div>';
                return;
            }

            let html = '';
            for (const it of data.items) {
                const dot = statusDot(it.status);
                const age = formatAge(it.age_seconds);
                const customBadge = it.custom ? ' <span class="rulesets-custom-badge">свой</span>' : '';

                html += '<div class="rulesets-row">' +
                    '<span class="rulesets-dot">' + dot + '</span>' +
                    '<span class="rulesets-id">' + escapeHtml(it.id) + customBadge + '</span>' +
                    '<span class="rulesets-age">' + age + '</span>' +
                    '<button class="btn rulesets-btn-refresh" data-id="' + escapeHtml(it.id) + '">Скачать</button>' +
                    '</div>';
            }
            container.innerHTML = html;

            container.querySelectorAll('.rulesets-btn-refresh').forEach(btn => {
                btn.addEventListener('click', () => refreshOne(btn.dataset.id, btn));
            });

        } catch (e) {
            container.innerHTML = '<div style="color:#e74c3c">Ошибка сети: ' + escapeHtml(e.message) + '</div>';
        }
    }

    function statusDot(status) {
        if (status === 'green') return '🟢';
        if (status === 'yellow') return '🟡';
        return '🔴';
    }

    function formatAge(seconds) {
        if (!seconds || seconds <= 0) return '—';
        if (seconds < 60) return seconds + ' сек';
        if (seconds < 3600) return Math.floor(seconds / 60) + ' мин';
        if (seconds < 86400) return Math.floor(seconds / 3600) + ' ч';
        return Math.floor(seconds / 86400) + ' дн';
    }

    async function refreshOne(id, btn) {
        if (refreshInProgress) return;
        refreshInProgress = true;
        const oldText = btn ? btn.textContent : '';
        if (btn) {
            btn.disabled = true;
            btn.textContent = '...';
        }

        try {
            const r = await api('/api/rulesets/refresh', 'POST', { id: id });
            if (r.error) {
                showMsg('Ошибка: ' + r.error, true);
            } else if (r.errors && r.errors.length > 0) {
                showMsg('Предупреждения: ' + r.errors.join('; '), true);
            } else {
                showMsg('Обновлён: ' + id, false);
            }
            await renderRulesetsStatus();
        } catch (e) {
            showMsg('Ошибка сети: ' + e.message, true);
        } finally {
            refreshInProgress = false;
            if (btn) {
                btn.disabled = false;
                btn.textContent = oldText;
            }
        }
    }

    async function refreshAll() {
        if (refreshInProgress) return;
        const btn = document.getElementById('btn-refresh-all-srs');
        if (btn) {
            btn.disabled = true;
            btn.textContent = '⏳ Обновляю...';
        }

        refreshInProgress = true;
        try {
            const r = await api('/api/rulesets/refresh', 'POST', { all: true });
            if (r.error) {
                showMsg('Ошибка: ' + r.error, true);
            } else if (r.errors && r.errors.length > 0) {
                showMsg('Обновлено: ' + r.refreshed + '. Ошибки: ' + r.errors.join('; '), true);
            } else {
                showMsg('Обновлено списков: ' + r.refreshed, false);
            }
            await renderRulesetsStatus();
        } catch (e) {
            showMsg('Ошибка сети: ' + e.message, true);
        } finally {
            refreshInProgress = false;
            if (btn) {
                btn.disabled = false;
                btn.textContent = '🔄 Обновить все';
            }
        }
    }

    function showMsg(text, isErr) {
        const el = document.getElementById('rulesets-status-msg');
        if (!el) return;
        el.textContent = text;
        el.style.color = isErr ? '#e74c3c' : '#2ecc71';
        setTimeout(() => { el.textContent = ''; }, 5000);
    }

    function escapeHtml(s) {
        if (s === undefined || s === null) return '';
        return String(s)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;');
    }

    function init() {
        const allBtn = document.getElementById('btn-refresh-all-srs');
        if (allBtn) allBtn.addEventListener('click', refreshAll);

        setTimeout(renderRulesetsStatus, 800);

        setInterval(() => {
            if (typeof currentTab !== 'undefined' && currentTab === 'rules') {
                renderRulesetsStatus();
            }
        }, 30000);

        document.querySelectorAll('.nav-tab[data-tab="rules"]').forEach(el => {
            el.addEventListener('click', () => {
                setTimeout(renderRulesetsStatus, 200);
            });
        });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
