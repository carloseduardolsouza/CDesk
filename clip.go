package main

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Área de transferência compartilhada: o que for copiado (Ctrl+C) num computador pode ser colado
// (Ctrl+V) no outro. Cada lado olha a própria área de transferência a cada 400 ms; quando o texto
// muda, manda ao outro lado da sessão ativa, que o coloca na sua área de transferência.
// shortcut: só texto (até 256 KB); imagens e arquivos ficam para a transferência de arquivos.

const (
	maxClip     = 256 << 10
	msgFrame    = 0 // mensagem host -> viewer: quadro JPEG
	msgClip     = 1 // mensagem host -> viewer: texto da área de transferência
	msgInfo     = 2 // mensagem host -> viewer: quantos monitores e qual está sendo mostrado
	headerBytes = 5 // tipo (1) + tamanho (4)
)

// hostConn é a sessão no lado de quem está sendo controlado. Os quadros e o texto da área de
// transferência saem por goroutines diferentes, então as escritas passam por um cadeado.
type hostConn struct {
	s       network.Stream
	wmu     sync.Mutex
	monitor atomic.Int32 // monitor mostrado ao viewer (0 = principal)
}

func (c *hostConn) write(msg []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.s.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.s.Write(msg)
	return err
}

func (c *hostConn) sendMsg(typ byte, payload []byte) error {
	msg := make([]byte, headerBytes+len(payload))
	msg[0] = typ
	putU32(msg[1:], uint32(len(payload)))
	copy(msg[headerBytes:], payload)
	return c.write(msg)
}

func (c *hostConn) sendClip(text string) error { return c.sendMsg(msgClip, []byte(text)) }

// defaultClipboard usa a área de transferência do Windows pelo runtime do Wails.
func (a *App) defaultClipboard() {
	a.clipGet = func() string { t, _ := wr.ClipboardGetText(a.ctx); return t }
	a.clipSet = func(t string) { wr.ClipboardSetText(a.ctx, t) }
}

// setClip coloca no texto recebido do outro computador na área de transferência local. Guarda o
// valor para o próximo ciclo não achar que foi uma cópia do usuário e devolvê-lo (eco).
func (a *App) setClip(text string) {
	if text == "" || len(text) > maxClip {
		return
	}
	a.mu.Lock()
	a.lastClip = text
	a.mu.Unlock()
	a.clipSet(text)
}

func (a *App) clipLoop() {
	every := a.clipEvery
	if every == 0 {
		every = 400 * time.Millisecond
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
			a.clipTick()
		}
	}
}

// clipTick manda o texto copiado pelo usuário para a sessão ativa (como host e/ou como viewer).
func (a *App) clipTick() {
	text := a.clipGet()
	if text == "" || len(text) > maxClip {
		return
	}
	a.mu.Lock()
	if text == a.lastClip {
		a.mu.Unlock()
		return
	}
	a.lastClip = text
	hc, vs := a.in, a.out[a.activeView]
	a.mu.Unlock()
	cfg := a.settings()
	if hc != nil && cfg.AllowClipboard && cfg.AllowControl {
		hc.sendClip(text)
	}
	if vs != nil {
		vs.sendClip(text)
	}
}

// SetActiveSession diz qual aba está em uso ("" = nenhuma). Só a aba ativa troca área de
// transferência, para o que o usuário copia não ir parar em computadores de abas em segundo plano.
// Ao entrar numa aba, o texto que já está copiado vai junto, para dar para copiar antes e colar depois.
func (a *App) SetActiveSession(id string) {
	a.mu.Lock()
	a.activeView = id
	v := a.out[id]
	a.mu.Unlock()
	if v == nil || a.clipGet == nil {
		return
	}
	if text := a.clipGet(); text != "" && len(text) <= maxClip {
		v.sendClip(text)
	}
}

func (v *viewSession) sendClip(text string) {
	b, _ := json.Marshal(Event{T: "clip", S: text})
	v.write(append(b, '\n'))
}
