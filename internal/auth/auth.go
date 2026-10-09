package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	SessionCookieName = "sb_session"
	SessionTTL        = 24 * time.Hour
)

// Session — одна сессия пользователя.
type Session struct {
	Token     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Manager — управляет паролем и сессиями.
type Manager struct {
	mu           sync.RWMutex
	passwordHash string
	sessions     map[string]*Session
}

// NewManager создаёт менеджер.
// Если passwordHash пустой — генерирует дефолтный пароль admin/admin
// и возвращает его в открытом виде (для печати в лог).
func NewManager(passwordHash string) (m *Manager, defaultPassword string) {
	m = &Manager{
		passwordHash: passwordHash,
		sessions:     make(map[string]*Session),
	}

	if passwordHash == "" {
		defaultPassword = "admin"
		hash, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
		if err != nil {
			panic(fmt.Sprintf("bcrypt: %v", err))
		}
		m.passwordHash = string(hash)
	}

	go m.cleanupLoop()
	return m, defaultPassword
}

// SetPasswordHash устанавливает хеш пароля (при смене).
func (m *Manager) SetPasswordHash(hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.passwordHash = hash
	// Инвалидируем все сессии при смене пароля
	m.sessions = make(map[string]*Session)
}

// PasswordHash возвращает текущий хеш (для сохранения в state).
func (m *Manager) PasswordHash() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.passwordHash
}

// CheckPassword проверяет пароль.
func (m *Manager) CheckPassword(password string) bool {
	m.mu.RLock()
	hash := m.passwordHash
	m.mu.RUnlock()
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// SetNewPassword — хеширует и устанавливает новый пароль.
func (m *Manager) SetNewPassword(newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	m.SetPasswordHash(string(hash))
	return nil
}

// CreateSession создаёт новую сессию и возвращает токен.
func (m *Manager) CreateSession() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	token := randomToken(32)
	now := time.Now()
	m.sessions[token] = &Session{
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(SessionTTL),
	}
	return token
}

// ValidateSession проверяет токен.
func (m *Manager) ValidateSession(token string) bool {
	if token == "" {
		return false
	}
	m.mu.RLock()
	sess, ok := m.sessions[token]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(sess.ExpiresAt) {
		m.DeleteSession(token)
		return false
	}
	return true
}

// DeleteSession удаляет сессию.
func (m *Manager) DeleteSession(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
}

// cleanupLoop удаляет просроченные сессии раз в час.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for token, sess := range m.sessions {
			if now.After(sess.ExpiresAt) {
				delete(m.sessions, token)
			}
		}
		m.mu.Unlock()
	}
}

// SetSessionCookie устанавливает HttpOnly cookie с токеном.
func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(SessionTTL.Seconds()),
	})
}

// ClearSessionCookie удаляет cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// GetSessionToken читает токен из cookie.
func GetSessionToken(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// Middleware — оборачивает handler, требуя валидную сессию.
// Пропускает без проверки: /static/*, /api/auth/login.
func Middleware(m *Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Публичные пути
		if path == "/api/auth/login" {
			next.ServeHTTP(w, r)
			return
		}
		if len(path) >= 8 && path[:8] == "/static/" {
			next.ServeHTTP(w, r)
			return
		}
		// Проверка сессии
		token := GetSessionToken(r)
		if !m.ValidateSession(token) {
			if len(path) >= 4 && path[:4] == "/api" {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			// Для всего остального отдаём страницу логина (она встроена в index.html)
			serveLoginPage(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveLoginPage отдаёт минимальную HTML-страницу логина.
func serveLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(loginHTML))
}

const loginHTML = `<!DOCTYPE html>
<html lang="ru" data-theme="dark">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Sing-box VitosaminTM — Вход</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
:root, [data-theme="dark"] {
    --bg: #0e1520; --card: #1a2435; --border: #26344b;
    --text: #dde6f2; --text-dim: #7f8ea8;
    --accent: #4a9eff; --accent-hi: #5fb0ff; --danger: #f87171;
}
html, body {
    background: var(--bg); color: var(--text);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    min-height: 100vh; display: flex; align-items: center; justify-content: center;
    padding: 20px;
}
.login-card {
    background: var(--card); border: 1px solid var(--border);
    border-radius: 16px; padding: 36px 32px;
    width: 100%; max-width: 380px;
    box-shadow: 0 20px 60px rgba(0,0,0,0.5);
}
.login-logo {
    display: flex; align-items: center; gap: 12px;
    margin-bottom: 28px;
}
.login-logo-icon {
    font-size: 28px; background: linear-gradient(135deg, #4a9eff, #7c5cff);
    width: 48px; height: 48px; border-radius: 12px;
    display: flex; align-items: center; justify-content: center;
    box-shadow: 0 4px 14px rgba(74,158,255,0.35);
}
.login-logo-text .t { font-size: 18px; font-weight: 700; }
.login-logo-text .s { font-size: 11px; color: var(--text-dim); letter-spacing: 1px; }
.login-hint { font-size: 13px; color: var(--text-dim); margin-bottom: 20px; }
.login-field { display: flex; flex-direction: column; gap: 6px; margin-bottom: 14px; }
.login-field label { font-size: 12px; color: var(--text-dim); }
.login-field input {
    background: var(--bg); border: 1px solid var(--border);
    border-radius: 8px; color: var(--text);
    font-size: 14px; padding: 11px 14px;
    font-family: inherit;
    transition: all 0.15s;
}
.login-field input:focus {
    outline: none; border-color: var(--accent);
    box-shadow: 0 0 0 3px rgba(74,158,255,0.15);
}
.login-btn {
    width: 100%; background: var(--accent); color: #fff;
    border: none; padding: 12px 20px; border-radius: 8px;
    font-size: 14px; font-weight: 600; cursor: pointer;
    margin-top: 8px; transition: all 0.15s;
    font-family: inherit;
}
.login-btn:hover { background: var(--accent-hi); }
.login-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.login-error {
    color: var(--danger); font-size: 13px;
    margin-top: 12px; min-height: 18px;
}
</style>
</head>
<body>
<div class="login-card">
    <div class="login-logo">
        <div class="login-logo-icon">⚡</div>
        <div class="login-logo-text">
            <div class="t">Sing-box</div>
            <div class="s">VITOSAMINTM</div>
        </div>
    </div>
    <div class="login-hint">Вход в панель управления</div>
    <form id="login-form" autocomplete="off">
        <div class="login-field">
            <label>Пароль</label>
            <input type="password" id="password" autofocus required>
        </div>
        <button type="submit" class="login-btn" id="submit-btn">Войти</button>
        <div class="login-error" id="login-error"></div>
    </form>
</div>
<script>
document.getElementById('login-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const btn = document.getElementById('submit-btn');
    const err = document.getElementById('login-error');
    const pw = document.getElementById('password').value;
    btn.disabled = true;
    err.textContent = '';
    try {
        const r = await fetch('/api/auth/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ password: pw }),
        });
        const j = await r.json();
        if (j.ok) {
            window.location.href = '/';
        } else {
            err.textContent = j.error || 'Неверный пароль';
            btn.disabled = false;
        }
    } catch (ex) {
        err.textContent = 'Ошибка сети: ' + ex.message;
        btn.disabled = false;
    }
});
</script>
</body>
</html>`

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("rand: %v", err))
	}
	return hex.EncodeToString(b)
}
