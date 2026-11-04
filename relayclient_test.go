package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEstadoDoServidor(t *testing.T) {
	a := &App{dir: t.TempDir()}
	a.loadSettings()
	if !a.settings().UseServer {
		t.Fatal("usar o servidor CDesk deve vir ligado por padrão")
	}
	want := func(state string) {
		t.Helper()
		if got := a.serverState(); got != state {
			t.Fatalf("estado do servidor = %q, queria %q", got, state)
		}
	}
	if !strings.HasPrefix(a.settings().serverAddress(), "/ip4/177.104.166.65/") {
		t.Fatal("sem endereço personalizado deve valer o servidor padrão embutido")
	}
	want("down") // padrão, ainda sem resposta
	a.cfg.Relay = "/ip4/10.0.0.1/tcp/4001/p2p/12D3KooWFh5WWWzpedwuHFBBHrbySb9ddtfVLfuX75xZxTWrfsd7"
	if a.settings().serverAddress() != a.cfg.Relay {
		t.Fatal("o endereço personalizado deve ter prioridade")
	}
	a.cfg.Relay = ""
	a.serverUp.Store(true)
	want("up")

	// desligado nas Configurações: nunca consulta o servidor, mesmo com endereço e servidor no ar
	s := a.settings()
	s.UseServer = false
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	want("off")
	if _, err := a.serverInfo(); err != errNoServer {
		t.Fatalf("com a opção desligada serverInfo deve recusar, veio %v", err)
	}
	if _, ok := a.viaServer(t.Context(), "123456789"); ok {
		t.Fatal("com a opção desligada viaServer não pode achar nada")
	}
	b := &App{dir: a.dir} // a escolha sobrevive a reiniciar o app
	b.loadSettings()
	if b.settings().UseServer {
		t.Fatal("a opção desligada não foi salva")
	}
}

func TestModoDaSessao(t *testing.T) {
	host, view, _, _ := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	m := view.SessionMode(host.myID)
	if m["mode"] != "lan" { // os dois apps do teste conversam por 127.0.0.1
		t.Fatalf("modo = %v, queria lan", m["mode"])
	}
	if ms, _ := m["ms"].(int); ms < 1 {
		t.Fatalf("o ping deveria vir preenchido, veio %v", m["ms"])
	}
	if len(view.SessionMode("000000000")) != 0 {
		t.Fatal("sessão inexistente não deve ter modo")
	}
}

func TestEnderecoDoServidorValido(t *testing.T) {
	a := &App{dir: t.TempDir()}
	a.loadSettings()
	s := a.settings()
	s.Relay = "/ip4/177.104.166.65/tcp/4001/p2p/12D3KooWQDgvjoA1xANVG6wVtzACLSkVKiazoYcXi62PFvekcnyt"
	if err := a.SaveSettings(s); err != nil {
		t.Fatalf("endereço do servidor recusado: %v", err)
	}
}

func TestEnderecoDoServidorColadoDoLog(t *testing.T) {
	const want = "/ip4/177.104.166.65/tcp/4001/p2p/12D3KooWQDgvjoA1xANVG6wVtzACLSkVKiazoYcXi62PFvekcnyt"
	for nome, colado := range map[string]string{
		"com espaços":          "  " + want + " \n",
		"com prefixo do log":   "2026/11/04 09:10:10 Endereço para o app (Configurações > Rede > Relay): " + want,
		"quebrado em 2 linhas": "/ip4/177.104.166.65/tcp/4001/p2p/12D3KooWQDgvjoA1xANVG6wVtzACLSkVKiaz\noYcXi62PFvekcnyt",
	} {
		a := &App{dir: t.TempDir()}
		a.loadSettings()
		s := a.settings()
		s.Relay = colado
		if err := a.SaveSettings(s); err != nil {
			t.Errorf("%s: recusado: %v", nome, err)
		} else if got := a.settings().Relay; got != want {
			t.Errorf("%s: salvou %q", nome, got)
		}
	}
	a := &App{dir: t.TempDir()}
	a.loadSettings()
	s := a.settings()
	s.Relay = "http://177.104.166.65:8443"
	if a.SaveSettings(s) == nil {
		t.Error("um endereço http:// não é um endereço de servidor e deve ser recusado")
	}
}

// Teste manual contra o servidor real: CDESK_LIVE=1 go test -run ServidorPadraoReal -v .
func TestServidorPadraoReal(t *testing.T) {
	if os.Getenv("CDESK_LIVE") == "" {
		t.Skip("defina CDESK_LIVE=1 para testar contra o servidor real")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := &App{ctx: ctx, dir: t.TempDir(), noMDNS: true}
	a.emit = func(string, ...any) {}
	if err := a.initP2P(); err != nil { // configuração padrão: nenhum endereço preenchido
		t.Fatal(err)
	}
	t.Cleanup(func() { a.h.Close() })
	if !eventually(20*time.Second, func() bool { return a.serverState() == "up" }) {
		t.Fatalf("o app não chegou ao servidor padrão (estado %q)", a.serverState())
	}
	r, err := a.dirCall(ctx, dirRequest{Op: "lookup", ID: a.myID})
	if err != nil || r.Rec == nil || r.Rec.Peer != a.h.ID().String() {
		t.Fatalf("o app não se registrou no diretório: %+v %v", r, err)
	}
	t.Logf("registrado no servidor com %d endereços: %v", len(r.Rec.Addrs), r.Rec.Addrs)
}

// Cronometra a conexão entre dois apps desta máquina, passando pelo servidor real ou só por P2P.
// CDESK_LIVE=1 go test -run TestVelocidadeReal -v -timeout 5m .
func TestVelocidadeReal(t *testing.T) {
	if os.Getenv("CDESK_LIVE") == "" {
		t.Skip("defina CDESK_LIVE=1")
	}
	for _, modo := range []struct{ nome, cfg string }{{"servidor", `{}`}, {"P2P", `{"useServer":false}`}} {
		t.Run(modo.nome, func(t *testing.T) {
			testSettings = modo.cfg
			defer func() { testSettings = `{"useServer":false}` }()
			host, view := newPeer(t, true, newRecorder(true)), newPeer(t, true, newRecorder(true))
			if modo.nome == "servidor" {
				if !eventually(30*time.Second, func() bool {
					r, err := view.dirCall(view.ctx, dirRequest{Op: "lookup", ID: host.myID})
					return err == nil && r.Rec != nil
				}) {
					t.Fatal("o host não apareceu no diretório do servidor")
				}
			} else { // P2P: o host precisa ter anunciado na DHT pública; mede quanto isso leva
				t0 := time.Now()
				if !eventually(150*time.Second, func() bool {
					ctx, cancel := context.WithTimeout(view.ctx, 8*time.Second)
					defer cancel()
					p, ok := view.viaDHT(ctx, host.myID)
					if ok {
						view.h.Network().ClosePeer(p)
					}
					return ok
				}) {
					t.Fatal("o host não ficou localizável na DHT em 150 s")
				}
				t.Logf("host localizável na DHT %v depois de abrir", time.Since(t0).Round(time.Second))
			}
			var mu sync.Mutex
			var linha []string
			t0 := time.Now()
			prev := view.emit
			view.emit = func(name string, data ...any) {
				if name == "status" && len(data) >= 3 {
					mu.Lock()
					linha = append(linha, fmt.Sprintf("%v %v", time.Since(t0).Round(10*time.Millisecond), data[2]))
					mu.Unlock()
				}
				prev(name, data...)
			}
			err := view.Connect(host.myID, "", false)
			t.Logf("[%s] total %v, erro=%v", modo.nome, time.Since(t0).Round(10*time.Millisecond), err)
			mu.Lock()
			for _, l := range linha {
				t.Log("   ", l)
			}
			mu.Unlock()
			view.Disconnect(host.myID)
		})
	}
}
