package main

import (
	"fmt"
	"image"
	"time"

	"github.com/kbinani/screenshot"
)

// Recursos que o viewer liga durante a sessão e que atuam no computador controlado: cursor e
// janela em foco (o viewer desenha/segue), janelas fora da tela, resolução, bloqueio das entradas,
// modo privacidade, áudio e papel de parede. Tudo isso exige o controle permitido (conn.ctl),
// menos mandar o cursor e a janela em foco, que só mostram o que já aparece na tela.
// O que muda o computador é desfeito em hostCleanup quando a sessão acaba, por qualquer motivo.

// hostAux manda o cursor e a janela em foco ao viewer e traz janelas fora da tela, até a sessão acabar.
func (a *App) hostAux(c *hostConn) {
	t := time.NewTicker(33 * time.Millisecond)
	defer t.Stop()
	var lastCur, lastWin string
	for n := 1; ; n++ {
		select {
		case <-c.quit:
			return
		case <-t.C:
		}
		feat := c.feat.Load()
		b := screenshot.GetDisplayBounds(c.currentMonitor())
		if feat&1 != 0 && b.Dx() > 0 {
			if x, y, ok := cursorPos(); ok {
				msg := fmt.Sprintf(`{"x":%.4f,"y":%.4f}`, float64(x-b.Min.X)/float64(b.Dx()), float64(y-b.Min.Y)/float64(b.Dy()))
				if msg != lastCur {
					lastCur = msg
					if c.sendMsg(msgCursor, []byte(msg)) != nil {
						return
					}
				}
			}
		}
		if feat&2 != 0 && n%8 == 0 && b.Dx() > 0 {
			if r, ok := foregroundRect(); ok {
				msg := fmt.Sprintf(`{"x":%.4f,"y":%.4f,"w":%.4f,"h":%.4f}`, float64(r.Min.X-b.Min.X)/float64(b.Dx()), float64(r.Min.Y-b.Min.Y)/float64(b.Dy()),
					float64(r.Dx())/float64(b.Dx()), float64(r.Dy())/float64(b.Dy()))
				if msg != lastWin {
					lastWin = msg
					if c.sendMsg(msgFocus, []byte(msg)) != nil {
						return
					}
				}
			}
		}
		if n%30 == 0 && c.winAssist.Load() && c.ctl.Load() {
			var all []image.Rectangle
			for i := 0; i < screenshot.NumActiveDisplays(); i++ {
				all = append(all, screenshot.GetDisplayBounds(i))
			}
			rescueWindows(all)
		}
	}
}

// hostAudio liga ou desliga o envio do som do computador ao viewer.
func (a *App) hostAudio(c *hostConn, on bool) {
	c.fxMu.Lock()
	defer c.fxMu.Unlock()
	if !on {
		if c.audioStop != nil {
			close(c.audioStop)
			c.audioStop = nil
		}
		return
	}
	if c.audioStop != nil {
		return
	}
	stop := make(chan struct{})
	c.audioStop = stop
	go func() {
		if err := audioCapture(stop, func(pcm []byte) {
			if c.sendMsg(msgAudio, pcm) != nil {
				c.s.Reset()
			}
		}); err != nil {
			a.hostChatAdd("sys", "Não foi possível transmitir o áudio: "+err.Error())
		}
	}()
}

// hostEvent trata os pedidos de recursos do viewer; devolve false se e não é um deles.
func (a *App) hostEvent(c *hostConn, e Event) bool {
	on := e.K == 1
	switch e.T {
	case "feat":
		c.feat.Store(int32(e.K))
	case "res":
		if c.ctl.Load() {
			go func() {
				c.fxMu.Lock()
				defer c.fxMu.Unlock()
				if setResolution(int(e.X), int(e.Y)) == nil {
					c.resetRes = e.X != 0 || e.Y != 0
				}
			}()
		}
	case "winassist":
		c.winAssist.Store(on)
	case "block":
		if c.ctl.Load() {
			blockInput(on)
		}
	case "privacy":
		if c.ctl.Load() {
			if err := privacyMode(on); err != nil {
				a.hostChatAdd("sys", "Modo privacidade: "+err.Error())
			} else {
				blockInput(on) // quem está no computador também não deve poder usá-lo
			}
		}
	case "audio":
		if c.ctl.Load() {
			a.hostAudio(c, on)
		}
	case "clipfilesopt":
		c.fileClip.Store(on)
	case "clipfiles":
		a.hostSetClipFiles(c, e.S)
	default:
		return false
	}
	return true
}

// hostCleanup desfaz o que a sessão mudou no computador.
func (a *App) hostCleanup(c *hostConn) {
	close(c.quit)
	a.hostAudio(c, false)
	blockInput(false)
	privacyMode(false)
	c.fxMu.Lock()
	if c.resetRes {
		setResolution(0, 0)
	}
	c.fxMu.Unlock()
}

// ---- papel de parede ----

// hideWallpaper esconde o papel de parede (deixa a imagem mais leve) e devolve a função que o restaura.
func hideWallpaper() func() {
	orig := wallpaper()
	if orig == "" {
		return func() {}
	}
	setWallpaper("")
	return func() { setWallpaper(orig) }
}
