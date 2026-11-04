package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(p string) []byte { b, _ := os.ReadFile(p); return b }

func connected(t *testing.T) (host, view *App) {
	host, view, _, _ = pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { view.Disconnect(host.myID) })
	return
}

func TestTransferirArquivosEPastas(t *testing.T) {
	host, view := connected(t)
	local, remote := t.TempDir(), t.TempDir()
	data := bytes.Repeat([]byte("cdesk"), 300_000) // ~1,5 MB
	write(t, filepath.Join(local, "a.bin"), data)
	write(t, filepath.Join(local, "pasta", "sub", "b.txt"), []byte("b"))
	os.MkdirAll(filepath.Join(local, "pasta", "vazia"), 0o755)
	write(t, filepath.Join(remote, "r", "c.txt"), []byte("c"))

	if err := view.FilesUpload(host.myID, "j1", local, []string{"a.bin", "pasta"}, remote); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read(filepath.Join(remote, "a.bin")), data) || string(read(filepath.Join(remote, "pasta", "sub", "b.txt"))) != "b" || !isDir(filepath.Join(remote, "pasta", "vazia")) {
		t.Fatal("upload chegou diferente")
	}
	if err := view.FilesDownload(host.myID, "j2", remote, []string{"r", "a.bin"}, local); err != nil {
		t.Fatal(err)
	}
	if string(read(filepath.Join(local, "r", "c.txt"))) != "c" || !bytes.Equal(read(filepath.Join(local, "a (2).bin")), data) {
		t.Fatal("download chegou diferente (arquivo existente deve ganhar nome novo)")
	}
	resp, err := view.FilesOp(host.myID, true, FileReq{Op: "list", Path: remote})
	if err != nil || len(resp.List.Entries) != 3 {
		t.Fatalf("listagem remota: %v %+v", err, resp.List)
	}
	if view.FilesDownload(host.myID, "j3", remote, []string{"../x"}, local) == nil {
		t.Fatal("nome com .. deveria ser recusado")
	}
}

func TestOperacoesDeExplorador(t *testing.T) {
	host, view := connected(t)
	d := t.TempDir()
	write(t, filepath.Join(d, "a.txt"), []byte("12345"))
	op := func(q FileReq) FileResp {
		t.Helper()
		r, err := view.FilesOp(host.myID, true, q)
		if err != nil {
			t.Fatalf("%s: %v", q.Op, err)
		}
		return r
	}
	op(FileReq{Op: "mkdir", Path: d, Name: "nova"})
	op(FileReq{Op: "rename", Path: d, Name: "a.txt", To: "b.txt"})
	op(FileReq{Op: "copy", Path: d, Names: []string{"b.txt"}, To: filepath.Join(d, "nova")})
	op(FileReq{Op: "move", Path: d, Names: []string{"b.txt"}, To: filepath.Join(d, "nova")})
	if exists(filepath.Join(d, "b.txt")) || !exists(filepath.Join(d, "nova", "b.txt")) || !exists(filepath.Join(d, "nova", "b (2).txt")) {
		t.Fatal("copiar/mover não deu o resultado esperado")
	}
	if p := op(FileReq{Op: "props", Path: d, Names: []string{"nova"}}).Props; p.Files != 2 || p.Size != 10 || p.Dirs != 1 {
		t.Fatalf("propriedades: %+v", p)
	}
	op(FileReq{Op: "delete", Path: d, Names: []string{"nova"}})
	if exists(filepath.Join(d, "nova")) {
		t.Fatal("não excluiu")
	}
	if _, err := view.FilesOp(host.myID, true, FileReq{Op: "delete", Path: d, Names: []string{".."}}); err == nil {
		t.Fatal("excluir .. deveria ser recusado")
	}
}

func TestCancelarTransferencia(t *testing.T) {
	host, view := connected(t)
	local, remote := t.TempDir(), t.TempDir()
	write(t, filepath.Join(local, "g.bin"), bytes.Repeat([]byte("x"), 50<<20))
	done := make(chan error, 1)
	go func() { done <- view.FilesUpload(host.myID, "j9", local, []string{"g.bin"}, remote) }()
	eventually(5*time.Second, func() bool { view.mu.Lock(); defer view.mu.Unlock(); return view.jobs["j9"] != nil })
	view.FilesCancel("j9")
	select {
	case err := <-done:
		if err == nil || err.Error() != ErrCanceled {
			t.Fatalf("esperava cancelado, veio %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelar não interrompeu")
	}
}

func TestArquivosBloqueadosNoHost(t *testing.T) {
	host, view, _, _ := pair(t)
	s := host.settings()
	s.AllowFiles = false
	if err := host.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	if _, err := view.FilesOp(host.myID, true, FileReq{Op: "list", Path: t.TempDir()}); err == nil {
		t.Fatal("host com arquivos desligados não deveria listar")
	}
}

func TestChatNosDoisSentidos(t *testing.T) {
	host, view, _, vr := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	if err := view.SendChat(host.myID, "oi do viewer"); err != nil {
		t.Fatal(err)
	}
	if !eventually(5*time.Second, func() bool {
		host.mu.Lock()
		defer host.mu.Unlock()
		return len(host.hostChat) == 3 && host.hostChat[2].Text == "oi do viewer"
	}) {
		t.Fatal("host não recebeu o chat")
	}
	if err := host.SendChat(view.myID, "oi do host"); err != nil {
		t.Fatal(err)
	}
	if !eventually(5*time.Second, func() bool { return vr.count("chat") == 1 }) {
		t.Fatal("viewer não recebeu o chat")
	}
	if host.SendChat("123456789", "x") == nil {
		t.Fatal("chat para quem não está na sessão deveria falhar")
	}
}

// A janela da sessão do host fala com o app principal por HTTP local: estado, chat, permissões e desconectar.
func TestJanelaDaSessaoDoHost(t *testing.T) {
	host, view := connected(t)
	ipc, err := host.startIPC()
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string) (int, string) {
		req, _ := http.NewRequest(method, "http://"+ipc.addr+path, strings.NewReader(body))
		req.Header.Set("X-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := call("GET", "/poll", "", "errado"); code != http.StatusUnauthorized {
		t.Fatalf("sem a chave certa deveria negar, veio %d", code)
	}
	code, body := call("GET", "/poll?chat=0&log=0", "", ipc.token)
	var p pollResp
	if json.Unmarshal([]byte(body), &p); code != 200 || !p.Active || !p.Ctl || !p.Files || len(p.Chat) != 2 {
		t.Fatalf("estado inicial: %d %+v", code, p)
	}
	if code, _ := call("POST", "/send", `{"text":"oi viewer"}`, ipc.token); code != 200 {
		t.Fatalf("send: %d", code)
	}
	call("POST", "/perm", `{"key":"files","on":false}`, ipc.token)
	if _, err := view.FilesOp(host.myID, true, FileReq{Op: "list", Path: t.TempDir()}); err == nil {
		t.Fatal("com arquivos desligados pela janela, listar deveria falhar")
	}
	call("POST", "/end", "", ipc.token)
	if !eventually(5*time.Second, func() bool { _, b := call("GET", "/poll", "", ipc.token); return strings.Contains(b, `"active":false`) }) {
		t.Fatal("desconectar não encerrou a sessão")
	}
}

func TestOpcoesDaSessao(t *testing.T) {
	var locked atomic.Int32
	old := lockWorkstation
	lockWorkstation = func() { locked.Add(1) }
	t.Cleanup(func() { lockWorkstation = old })

	// viewer pede bloquear ao final e desconecta: o host bloqueia
	host, view := connected(t)
	view.SetSessionOption(host.myID, "lockEnd", true)
	time.Sleep(300 * time.Millisecond)
	view.Disconnect(host.myID)
	if !eventually(5*time.Second, func() bool { return locked.Load() == 1 }) {
		t.Fatal("o host deveria bloquear ao fim da sessão")
	}

	// se o host é quem encerra, não bloqueia a própria tela
	host2, view2 := connected(t)
	view2.SetSessionOption(host2.myID, "lockEnd", true)
	time.Sleep(300 * time.Millisecond)
	host2.EndSession()
	time.Sleep(500 * time.Millisecond)
	if locked.Load() != 1 {
		t.Fatalf("host encerrou a sessão e não deveria bloquear (bloqueios: %d)", locked.Load())
	}

	// desligar a sincronização do texto: nada passa nos dois sentidos
	host3, view3 := connected(t)
	view3.SetSessionOption(host3.myID, "clip", false)
	view3.SetActiveSession(host3.myID)
	clipOf(view3).set("não vai")
	clipOf(host3).set("nem este")
	time.Sleep(500 * time.Millisecond)
	if clipOf(host3).get() != "nem este" || clipOf(view3).get() != "não vai" {
		t.Fatal("com a sincronização desligada o texto não deveria trocar")
	}
}

func TestVersaoMaisNova(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.2.0", "0.1.0", true}, {"0.1.0", "0.1.0", false}, {"0.1", "0.1.0", false}, {"1.0.0", "0.9.9", true}, {"0.1.0", "0.1.1", false}, {"v0.1.1-beta", "0.1.0", true}} {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

// O viewer pede o cursor e a janela em foco do host e recebe a posição.
func TestCursorEJanelaChegamAoViewer(t *testing.T) {
	host, view, _, vr := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	view.SendInput(host.myID, `{"t":"feat","k":3}`)
	if !eventually(5*time.Second, func() bool { return vr.count("cursor") > 0 }) {
		t.Fatal("o viewer não recebeu a posição do cursor")
	}
	if !eventually(5*time.Second, func() bool { return vr.count("focus") > 0 }) {
		t.Skip("sem janela em foco neste momento")
	}
}
