package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
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

// Janela da sessão no host, como no AnyDesk: enquanto alguém controla este computador, abre uma
// janela própria (outro processo, CDesk.exe --session ...) com quem está conectado, há quanto
// tempo, o que ele pode fazer, o chat e o registro de arquivos, e o botão Desconectar.
// O Wails v2 só tem uma janela por processo (veja ask.go), então a janela conversa com o app
// principal por HTTP em 127.0.0.1, com uma chave aleatória passada só para ela.
// shortcut: a janela pergunta o estado a cada 500 ms em vez de receber eventos.

type ipcServer struct{ addr, token string }

type pollResp struct {
	Chat   []chatMsg `json:"chat"`
	Log    []chatMsg `json:"log"`
	Ctl    bool      `json:"ctl"`
	Clip   bool      `json:"clip"`
	Files  bool      `json:"files"`
	Active bool      `json:"active"`
}

func (a *App) startIPC() (*ipcServer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ipc != nil {
		return a.ipc, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tok := make([]byte, 16)
	rand.Read(tok)
	s := &ipcServer{addr: ln.Addr().String(), token: hex.EncodeToString(tok)}
	mux := http.NewServeMux()
	mux.HandleFunc("/poll", a.ipcPoll)
	mux.HandleFunc("/send", a.ipcSend)
	mux.HandleFunc("/end", func(w http.ResponseWriter, r *http.Request) { a.EndSession() })
	mux.HandleFunc("/perm", a.ipcPerm)
	go http.Serve(ln, ipcAuth(s.token, mux))
	a.ipc = s
	return s, nil
}

func ipcAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(token)) != 1 {
			http.Error(w, "negado", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) ipcPoll(w http.ResponseWriter, r *http.Request) {
	ci, _ := strconv.Atoi(r.URL.Query().Get("chat"))
	li, _ := strconv.Atoi(r.URL.Query().Get("log"))
	a.mu.Lock()
	p := pollResp{Chat: []chatMsg{}, Log: []chatMsg{}}
	if in := a.in; in != nil {
		p.Active, p.Ctl, p.Clip, p.Files = true, in.ctl.Load(), in.clip.Load(), in.files.Load()
	}
	if ci >= 0 && ci < len(a.hostChat) {
		p.Chat = append(p.Chat, a.hostChat[ci:]...)
	}
	if li >= 0 && li < len(a.hostLog) {
		p.Log = append(p.Log, a.hostLog[li:]...)
	}
	a.mu.Unlock()
	json.NewEncoder(w).Encode(p)
}

func (a *App) ipcSend(w http.ResponseWriter, r *http.Request) {
	var b struct{ Text string }
	a.mu.Lock()
	in := a.in
	a.mu.Unlock()
	if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&b) != nil || in == nil {
		http.Error(w, "sem sessão", http.StatusBadRequest)
		return
	}
	if err := a.SendChat(numID(in.s.Conn().RemotePeer()), b.Text); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

func (a *App) ipcPerm(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Key string
		On  bool
	}
	a.mu.Lock()
	in := a.in
	a.mu.Unlock()
	if json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&b) != nil || in == nil {
		http.Error(w, "sem sessão", http.StatusBadRequest)
		return
	}
	switch b.Key {
	case "ctl":
		in.ctl.Store(b.On)
	case "clip":
		in.clip.Store(b.On)
	case "files":
		in.files.Store(b.On)
	default:
		http.Error(w, "permissão desconhecida", http.StatusBadRequest)
	}
}

// sessionWindow abre a janela da sessão e devolve a função que a fecha (chamada quando a sessão acaba).
func (a *App) sessionWindow(viewer, name string, since time.Time) func() {
	exe, err := os.Executable()
	if err != nil {
		return func() {}
	}
	ipc, err := a.startIPC()
	if err != nil {
		return func() {}
	}
	cmd := exec.Command(exe, "--session", "--addr", ipc.addr, "--token", ipc.token,
		"--id", viewer, "--name", name, "--since", strconv.FormatInt(since.UnixMilli(), 10))
	if cmd.Start() != nil {
		return func() {}
	}
	go cmd.Wait()
	return func() { cmd.Process.Kill() }
}

// ---- processo --session ----

// SessionParams são os dados fixos da janela.
type SessionParams struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Since int64  `json:"since"`
}

// SessionApp é o app do processo --session (exposto ao frontend session.html).
type SessionApp struct {
	ctx         context.Context
	p           SessionParams
	addr, token string
	fails       int
}

func (s *SessionApp) startup(ctx context.Context) { s.ctx = ctx }

func (s *SessionApp) Params() SessionParams { return s.p }

func (s *SessionApp) call(method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+s.addr+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Token", s.token)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(string(bytes.TrimSpace(out)))
	}
	return out, nil
}

// Poll devolve o que mudou desde as mensagens chat e log que a janela já mostra. Se o app
// principal sumir, a janela fecha sozinha.
func (s *SessionApp) Poll(chat, log int) (pollResp, error) {
	var p pollResp
	b, err := s.call("GET", fmt.Sprintf("/poll?chat=%d&log=%d", chat, log), nil)
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	if err != nil {
		if s.fails++; s.fails >= 5 {
			wr.Quit(s.ctx)
		}
		return p, err
	}
	s.fails = 0
	return p, nil
}

func (s *SessionApp) Send(text string) error {
	_, err := s.call("POST", "/send", map[string]string{"text": text})
	return err
}

func (s *SessionApp) End() { s.call("POST", "/end", nil) }

func (s *SessionApp) SetPerm(key string, on bool) error {
	_, err := s.call("POST", "/perm", map[string]any{"key": key, "on": on})
	return err
}

// runSession é o modo --session: mostra a janela da sessão até o app principal fechá-la.
func runSession(args []string) {
	fl := flag.NewFlagSet("session", flag.ExitOnError)
	s := &SessionApp{}
	fl.StringVar(&s.addr, "addr", "", "")
	fl.StringVar(&s.token, "token", "", "")
	fl.StringVar(&s.p.ID, "id", "", "")
	fl.StringVar(&s.p.Name, "name", "", "")
	fl.Int64Var(&s.p.Since, "since", time.Now().UnixMilli(), "")
	fl.Parse(args)

	err := wails.Run(&options.App{
		Title:            "CDesk - " + s.p.ID,
		Width:            760,
		Height:           540,
		MinWidth:         640,
		MinHeight:        460,
		BackgroundColour: &options.RGBA{R: 21, G: 21, B: 21, A: 255},
		AssetServer:      &assetserver.Options{Handler: pageAssets("/session.html")},
		Windows: &windows.Options{
			Theme:               windows.Dark,
			WebviewUserDataPath: filepath.Join(os.TempDir(), "cdesk-session"),
		},
		OnStartup: s.startup,
		Bind:      []interface{}{s},
	})
	if err != nil {
		os.Exit(1)
	}
}
