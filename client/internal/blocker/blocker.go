package blocker

import (
	"log"
	"strings"
	"sync"
	"time"
)

type Blocker struct {
	mu              sync.Mutex
	allowedKeywords []string
	running         bool
	stopChan        chan struct{}
	// OnBlocked được gọi khi phát hiện ứng dụng không nằm trong danh sách cho phép
	// (để chụp ảnh + ghi vi phạm). Việc TỰ ĐÓNG ứng dụng do KillOnBlock quyết định.
	OnBlocked func(procName string, title string)
	// KillOnBlock=true -> tự tắt (terminate) ứng dụng không cho phép mỗi lần quét,
	// không chỉ báo cáo. Ảnh bằng chứng được chụp (qua OnBlocked) TRƯỚC khi tắt.
	KillOnBlock bool
}

// WindowInfo mô tả một ứng dụng đang mở có giao diện (cửa sổ hiển thị).
type WindowInfo struct {
	PID         uint32
	Title       string
	ProcessName string
}

var Instance = &Blocker{
	allowedKeywords: []string{"chrome", "idea64", "vscode", "wails", "simple_care", "client"},
	stopChan:        make(chan struct{}),
	KillOnBlock:     true, // tự tắt ứng dụng không cho phép
}

func parseKeywordList(keywords string) []string {
	keywords = strings.ReplaceAll(keywords, "\r\n", "\n")
	parts := strings.FieldsFunc(keywords, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';' || r == '|'
	})
	seen := map[string]bool{}
	var list []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(strings.ToLower(p))
		trimmed = strings.TrimSuffix(trimmed, ".exe")
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		list = append(list, trimmed)
	}
	return list
}

var keywordAliases = map[string][]string{
	"lark":   {"lark", "feishu", "larkshell", "larkhelper"},
	"feishu": {"lark", "feishu", "larkshell", "larkhelper"},
	"teams":  {"teams", "ms-teams", "msteams"},
	"zalo":   {"zalo", "zalopcb"},
}

func expandKeyword(kw string) []string {
	base := []string{kw}
	if aliases, ok := keywordAliases[kw]; ok {
		base = append(base, aliases...)
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range base {
		a = strings.TrimSpace(strings.ToLower(a))
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

func matchesAllowedKeyword(kw, pNameLower, wTitleLower string) bool {
	for _, variant := range expandKeyword(kw) {
		if strings.Contains(pNameLower, variant) || strings.Contains(wTitleLower, variant) {
			return true
		}
	}
	return false
}

func (b *Blocker) SetKeywords(keywords string) {
	b.mu.Lock()
	b.allowedKeywords = parseKeywordList(keywords)
	b.mu.Unlock()
	log.Printf("[BLOCKER] Keywords updated: %v", b.allowedKeywords)
}

func (b *Blocker) Start() {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return
	}
	b.running = true
	b.stopChan = make(chan struct{})
	b.mu.Unlock()

	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				b.checkAndReport()
			case <-b.stopChan:
				return
			}
		}
	}()
	log.Println("[BLOCKER] Application Blocker Daemon Started.")
}

func (b *Blocker) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		return
	}
	b.running = false
	close(b.stopChan)
	log.Println("[BLOCKER] Application Blocker Daemon Stopped.")
}
