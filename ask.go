package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// O Wails v2 só tem uma janela por processo. Para o pedido de acesso aparecer fora da janela do
// CDesk, como no AnyDesk, o app abre outro processo de si mesmo (CDesk.exe --ask ...) que mostra
// só o aviso. O resultado volta pelo código de saída; qualquer outro código conta como recusa.
const askAccepted = 42

// askWindow mostra o pedido de acesso em outra janela e devolve se o usuário aceitou.
func (a *App) askWindow(viewer, name string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	s := a.settings()
	// a janela se recusa sozinha no prazo; este limite só cobre uma janela travada
	ctx, cancel := context.WithTimeout(a.ctx, time.Duration(s.AskSeconds+10)*time.Second)
	defer cancel()
	err = exec.CommandContext(ctx, exe, "--ask",
		"--id", viewer,
		"--name", name,
		"--seconds", strconv.Itoa(s.AskSeconds),
		"--top="+strconv.FormatBool(s.AskOnTop),
		"--sound="+strconv.FormatBool(s.AskSound),
		"--control="+strconv.FormatBool(s.AllowControl),
	).Run()
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == askAccepted
}

// AskParams são os dados que a janela do pedido mostra.
type AskParams struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Seconds int    `json:"seconds"`
	Control bool   `json:"control"`
}

// AskApp é o app do processo --ask (exposto ao frontend ask.html).
type AskApp struct {
	ctx      context.Context
	p        AskParams
	sound    bool
	accepted bool
}

func (k *AskApp) startup(ctx context.Context) {
	k.ctx = ctx
	if k.sound {
		beep()
	}
}

func (k *AskApp) Params() AskParams { return k.p }

func (k *AskApp) Answer(allow bool) {
	k.accepted = allow
	wr.Quit(k.ctx)
}

// askAssets serve o frontend, com ask.html como página inicial.
func askAssets() http.Handler {
	sub, _ := fs.Sub(embedded, "frontend")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			r.URL.Path = "/ask.html"
		}
		files.ServeHTTP(w, r)
	})
}

// runAsk é o modo --ask: mostra a janela do pedido e sai com askAccepted se o usuário aceitou.
func runAsk(args []string) {
	fl := flag.NewFlagSet("ask", flag.ExitOnError)
	k := &AskApp{}
	fl.StringVar(&k.p.ID, "id", "", "")
	fl.StringVar(&k.p.Name, "name", "", "")
	fl.IntVar(&k.p.Seconds, "seconds", 30, "")
	fl.BoolVar(&k.p.Control, "control", true, "")
	fl.BoolVar(&k.sound, "sound", true, "")
	top := fl.Bool("top", true, "")
	fl.Parse(args)

	err := wails.Run(&options.App{
		Title:            "CDesk - Pedido de acesso",
		Width:            500,
		Height:           470,
		DisableResize:    true,
		Frameless:        true,
		AlwaysOnTop:      *top,
		BackgroundColour: &options.RGBA{R: 17, G: 24, B: 35, A: 255},
		AssetServer:      &assetserver.Options{Handler: askAssets()},
		Windows: &windows.Options{
			Theme:               windows.Dark,
			WebviewUserDataPath: filepath.Join(os.TempDir(), "cdesk-ask"), // não briga com o app principal
		},
		OnStartup: k.startup,
		Bind:      []interface{}{k},
	})
	if err != nil || !k.accepted {
		os.Exit(1)
	}
	os.Exit(askAccepted)
}
