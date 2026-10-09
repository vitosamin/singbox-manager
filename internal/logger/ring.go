package logger

import (
	"strings"
	"sync"
)

// Ring — кольцевой буфер строк в оперативке.
// Не тормозит диск, не занимает много памяти (по умолчанию 1000 строк).
type Ring struct {
	mu    sync.RWMutex
	lines []string
	size  int
	next  int
	full  bool
}

// NewRing создаёт буфер на size строк.
func NewRing(size int) *Ring {
	if size <= 0 {
		size = 1000
	}
	return &Ring{
		lines: make([]string, size),
		size:  size,
	}
}

// Write реализует io.Writer — можно подключать как stdout/stderr.
func (r *Ring) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\n")
	if text == "" {
		return len(p), nil
	}
	// Разбиваем на строки, если пришло несколько сразу
	for _, line := range strings.Split(text, "\n") {
		r.append(line)
	}
	return len(p), nil
}

func (r *Ring) append(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[r.next] = line
	r.next = (r.next + 1) % r.size
	if r.next == 0 {
		r.full = true
	}
}

// Snapshot возвращает все строки в порядке поступления.
func (r *Ring) Snapshot() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []string
	if r.full {
		out = append(out, r.lines[r.next:]...)
		out = append(out, r.lines[:r.next]...)
	} else {
		out = append(out, r.lines[:r.next]...)
	}
	return strings.Join(out, "\n")
}

// Tail возвращает последние N строк.
func (r *Ring) Tail(n int) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var ordered []string
	if r.full {
		ordered = append(ordered, r.lines[r.next:]...)
		ordered = append(ordered, r.lines[:r.next]...)
	} else {
		ordered = append(ordered, r.lines[:r.next]...)
	}
	if n > 0 && len(ordered) > n {
		ordered = ordered[len(ordered)-n:]
	}
	return strings.Join(ordered, "\n")
}

// Clear очищает буфер.
func (r *Ring) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next = 0
	r.full = false
	for i := range r.lines {
		r.lines[i] = ""
	}
}
