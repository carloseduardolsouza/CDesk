package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"slices"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// instanceID identifica a instância única; testes isolados trocam com -ldflags "-X main.instanceID=..."
var instanceID = "cdesk-7f3c1a52-single-instance"

//go:embed frontend
var embedded embed.FS

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--ask" { // janela do pedido de acesso (outro processo)
		runAsk(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--session" { // janela da sessão no host (outro processo)
		runSession(os.Args[2:])
		return
	}
	assets, _ := fs.Sub(embedded, "frontend")
	app := &App{}
	err := wails.Run(&options.App{
		Title:     "CDesk",
		Width:     1024,
		Height:    700,
		MinWidth:  520,
		MinHeight: 480,
		AssetServer: &assetserver.Options{Assets: assets, Handler: frameHandler{app},
			// sem cache: o WebView2 guarda JS/CSS e, depois de uma atualização, mostraria a interface antiga
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Cache-Control", "no-store")
					next.ServeHTTP(w, r)
				})
			}},
		// mesma cor do fundo do CSS: evita flash branco ao abrir
		BackgroundColour: &options.RGBA{R: 21, G: 23, B: 27, A: 255},
		Windows:          &windows.Options{Theme: windows.Dark},
		StartHidden:      slices.Contains(os.Args, "--hidden"), // iniciado pelo Windows: só a bandeja
		// fechar a janela esconde o CDesk na bandeja, onde ele continua recebendo conexões
		OnBeforeClose: func(ctx context.Context) bool {
			if app.quitting.Load() || !app.settings().TrayOnClose {
				return false
			}
			wr.WindowHide(ctx)
			return true
		},
		// abrir o CDesk de novo só traz o que já está rodando (talvez na bandeja) para a frente
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               instanceID,
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.showWindow() },
		},
		OnStartup:  app.startup,
		OnShutdown: func(context.Context) { app.closeAll() },
		Bind:       []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
