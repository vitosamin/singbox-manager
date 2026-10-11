package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/shapovalenko/keenetic-singbox-manager/internal/ruleset"
)

// handleRulesetsStatus — GET → статус кэша .srs.
// Показывает: какие списки включены, есть ли для них файл, возраст файла.
// Ответ: {"items": [...], "total": N, "fresh": N, "stale": N, "missing": N}
func handleRulesetsStatus(w http.ResponseWriter, r *http.Request) {
	st := store.Snapshot()

	cacheStatus, err := ruleset.CacheStatus()
	if err != nil {
		writeJSON(w, map[string]interface{}{"error": err.Error()})
		return
	}

	type Item struct {
		ID         string `json:"id"`
		File       string `json:"file"`
		Cached     bool   `json:"cached"`
		AgeSeconds int64  `json:"age_seconds"`
		Status     string `json:"status"` // green / yellow / red
		Custom     bool   `json:"custom"`
	}

	items := make([]Item, 0, len(st.EnabledLists)+len(st.CustomSRS))
	fresh := 0
	stale := 0
	missing := 0

	// Включённые списки из каталога.
	for _, id := range st.EnabledLists {
		file := id + ".srs"
		it := Item{ID: id, File: file, Custom: false}

		if age, ok := cacheStatus[file]; ok {
			it.Cached = true
			it.AgeSeconds = int64(age.Seconds())
			if age < ruleset.CacheMaxAge {
				it.Status = "green"
				fresh++
			} else {
				it.Status = "yellow"
				stale++
			}
		} else {
			it.Status = "red"
			missing++
		}
		items = append(items, it)
	}

	// Свои .srs.
	for _, srs := range st.CustomSRS {
		file := srs.ID + ".srs"
		it := Item{ID: srs.ID, File: file, Custom: true}
		if age, ok := cacheStatus[file]; ok {
			it.Cached = true
			it.AgeSeconds = int64(age.Seconds())
			if age < ruleset.CacheMaxAge {
				it.Status = "green"
				fresh++
			} else {
				it.Status = "yellow"
				stale++
			}
		} else {
			it.Status = "red"
			missing++
		}
		items = append(items, it)
	}

	writeJSON(w, map[string]interface{}{
		"items":   items,
		"total":   len(items),
		"fresh":   fresh,
		"stale":   stale,
		"missing": missing,
	})
}

// handleRulesetsRefresh — POST → принудительное скачивание .srs.
// Тело: {"id": "youtube"} — один список, или {"all": true} — все включённые.
// Ответ: {"ok": true, "refreshed": N, "errors": [...]}.
func handleRulesetsRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		ID  string `json:"id"`
		All bool   `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	st := store.Snapshot()
	refreshed := 0
	var errs []string

	if body.All {
		// Обновляем все включённые списки каталога.
		for _, id := range st.EnabledLists {
			if _, err := ruleset.ForceDownloadByID(id); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", id, err))
				continue
			}
			refreshed++
		}
		// Обновляем все свои .srs.
		for _, srs := range st.CustomSRS {
			if _, err := ruleset.ForceDownload(srs.ID, srs.URL); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", srs.ID, err))
				continue
			}
			refreshed++
		}
	} else if body.ID != "" {
		// Один список: сначала проверяем, может это custom.
		handled := false
		for _, srs := range st.CustomSRS {
			if srs.ID == body.ID {
				handled = true
				if _, err := ruleset.ForceDownload(srs.ID, srs.URL); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", srs.ID, err))
				} else {
					refreshed = 1
				}
				break
			}
		}
		if !handled {
			if _, err := ruleset.ForceDownloadByID(body.ID); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", body.ID, err))
			} else {
				refreshed = 1
			}
		}
	} else {
		writeJSON(w, map[string]interface{}{"error": "укажи id или all=true"})
		return
	}

	resp := map[string]interface{}{
		"ok":        true,
		"refreshed": refreshed,
	}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	writeJSON(w, resp)
}
