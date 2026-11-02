package main

import (
	"encoding/json"

	"github.com/kbinani/screenshot"
)

// Vários monitores: o host diz quantos tem e qual está sendo mostrado; o viewer escolhe pela barra
// da sessão. O mouse é mapeado para o monitor escolhido.

// currentMonitor é o monitor mostrado ao viewer; um índice que não existe (ex.: monitor
// desligado no meio da sessão) volta para o principal.
func (c *hostConn) currentMonitor() int {
	i := int(c.monitor.Load())
	if i < 0 || i >= screenshot.NumActiveDisplays() {
		return 0
	}
	return i
}

func (c *hostConn) sendInfo() error {
	b, _ := json.Marshal(map[string]int{"n": screenshot.NumActiveDisplays(), "i": c.currentMonitor()})
	return c.sendMsg(msgInfo, b)
}

// SetMonitor pede ao computador id para mostrar o monitor idx (0 = primeiro).
func (a *App) SetMonitor(id string, idx int) {
	a.mu.Lock()
	v := a.out[id]
	a.mu.Unlock()
	if v != nil {
		b, _ := json.Marshal(Event{T: "monitor", K: idx})
		v.write(append(b, '\n'))
	}
}
