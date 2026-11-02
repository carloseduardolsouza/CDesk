package main

import (
	_ "embed"

	"fyne.io/systray"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed assets/icon.ico
var trayIcon []byte

// O Wails v2 não tem ícone de bandeja, então ele roda por fora, na biblioteca systray. Com ele o
// CDesk continua rodando (e recebendo conexões) depois que a janela é fechada.

func (a *App) showWindow() {
	wr.WindowUnminimise(a.ctx)
	wr.WindowShow(a.ctx)
}

func (a *App) startTray() {
	go systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("CDesk")
		systray.SetTooltip("CDesk - ID " + fmtID(a.myID))
		systray.SetOnTapped(a.showWindow)
		open := systray.AddMenuItem("Abrir CDesk", "")
		systray.AddSeparator()
		quit := systray.AddMenuItem("Sair do CDesk", "")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					a.showWindow()
				case <-quit.ClickedCh:
					a.Quit()
					return
				}
			}
		}()
	}, nil)
}

// Quit fecha o aplicativo de verdade (fechar a janela só a esconde na bandeja).
func (a *App) Quit() {
	a.quitting.Store(true)
	systray.Quit()
	wr.Quit(a.ctx)
}

// fmtID agrupa o ID em blocos de 3 dígitos: 123 456 789.
func fmtID(id string) string {
	if len(id) != 9 {
		return id
	}
	return id[:3] + " " + id[3:6] + " " + id[6:]
}
