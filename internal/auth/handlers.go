package auth

import (
	"encoding/json"
	"net/http"
)

// LoginRequest — тело запроса /api/auth/login.
type LoginRequest struct {
	Password string `json:"password"`
}

// LoginResponse — ответ /api/auth/login.
type LoginResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// HandleLogin — POST /api/auth/login.
func (m *Manager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, LoginResponse{Error: "bad request"})
		return
	}

	if !m.CheckPassword(req.Password) {
		writeJSON(w, http.StatusUnauthorized, LoginResponse{Error: "Неверный пароль"})
		return
	}

	token := m.CreateSession()
	SetSessionCookie(w, token)
	writeJSON(w, http.StatusOK, LoginResponse{OK: true})
}

// HandleLogout — POST /api/auth/logout.
func (m *Manager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := GetSessionToken(r)
	if token != "" {
		m.DeleteSession(token)
	}
	ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// HandleStatus — GET /api/auth/status.
func (m *Manager) HandleStatus(w http.ResponseWriter, r *http.Request) {
	token := GetSessionToken(r)
	valid := m.ValidateSession(token)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"authenticated": valid,
	})
}

// ChangePasswordRequest — тело /api/auth/change-password.
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// HandleChangePassword — POST /api/auth/change-password.
// Требует валидную сессию (middleware уже проверил) и знание старого пароля.
func (m *Manager) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "bad request"})
		return
	}
	if !m.CheckPassword(req.OldPassword) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "Неверный старый пароль"})
		return
	}
	if len(req.NewPassword) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "Пароль должен быть не короче 4 символов"})
		return
	}
	if err := m.SetNewPassword(req.NewPassword); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	// Инвалидируем сессии — нужно залогиниться заново
	ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "Пароль изменён, войдите заново"})
}

// OnPasswordHashChange — колбэк, вызывается при смене хеша,
// чтобы сохранить его в state.json.
var OnPasswordHashChange func(hash string)

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
