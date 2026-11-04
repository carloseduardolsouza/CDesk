package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/libp2p/go-libp2p/core/peerstore"
)

// newPeer sobe um App sem janela; r registra os eventos ao frontend e responde aos pedidos de acesso.
// testSettings: os testes não falam com o servidor real; os testes ao vivo trocam por "{}" (padrão do app).
var testSettings = `{"useServer":false}`

func newPeer(t *testing.T, noMDNS bool, r *recorder) *App {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := &App{ctx: ctx, dir: t.TempDir(), noMDNS: noMDNS}
	os.WriteFile(filepath.Join(a.dir, "settings.json"), []byte(testSettings), 0o600)
	a.emit = func(name string, data ...any) { r.on(name, data) }
	a.askFn = r.ask
	fc := &fakeClip{}
	fakes.Store(a, fc)
	a.clipGet, a.clipSet = fc.get, fc.set
	a.clipEvery = 30 * time.Millisecond
	if err := a.initP2P(); err != nil {
		t.Fatal(err)
	}
	go a.clipLoop()
	t.Cleanup(func() { a.h.Close() })
	return a
}

type recorder struct {
	mu     sync.Mutex
	events map[string]int
	frame  chan struct{}
	accept bool
	asked  []string        // "id|nome" de cada pedido de acesso mostrado
	mon    [2]int          // último aviso de monitores: quantos há, qual está sendo mostrado
	online map[string]bool // último resultado de presença por ID
	checks map[string]int  // quantos resultados de presença chegaram por ID
}

func newRecorder(accept bool) *recorder {
	return &recorder{events: map[string]int{}, frame: make(chan struct{}, 1), accept: accept, online: map[string]bool{}, checks: map[string]int{}}
}

func (r *recorder) on(name string, data []any) {
	r.mu.Lock()
	r.events[name]++
	r.mu.Unlock()
	if name == "presence" {
		r.mu.Lock()
		r.online[data[0].(string)] = data[1].(bool)
		r.checks[data[0].(string)]++
		r.mu.Unlock()
	}
	if name == "monitors" {
		r.mu.Lock()
		r.mon = [2]int{data[1].(int), data[2].(int)}
		r.mu.Unlock()
	}
	if name == "frame" {
		r.mu.Lock()
		r.events["frame:"+data[0].(string)]++
		r.mu.Unlock()
		select {
		case r.frame <- struct{}{}:
		default:
		}
	}
}

// ask faz o papel da janela de pedido de acesso.
func (r *recorder) ask(viewer, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events["request"]++
	r.asked = append(r.asked, viewer+"|"+name)
	return r.accept
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[name]
}

// Dois peers conhecidos por endereço (como na rede local): cobre protocolo, permissão e senha.
func pair(t *testing.T) (hostApp, viewApp *App, hostRec, viewRec *recorder) {
	hostRec, viewRec = newRecorder(true), newRecorder(true)
	hostApp = newPeer(t, true, hostRec)
	viewApp = newPeer(t, true, viewRec)
	viewApp.h.Peerstore().AddAddrs(hostApp.h.ID(), hostApp.h.Addrs(), peerstore.PermanentAddrTTL)
	return
}

func TestConnectComPermissao(t *testing.T) {
	host, view, hr, vr := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	select {
	case <-vr.frame:
	case <-time.After(10 * time.Second):
		t.Fatal("nenhum frame recebido")
	}
	if hr.count("request") != 1 {
		t.Fatalf("esperava 1 pedido de permissão, houve %d", hr.count("request"))
	}
}

func TestRecusado(t *testing.T) {
	host, view, hr, _ := pair(t)
	hr.accept = false
	if err := view.Connect(host.myID, "", false); err == nil {
		t.Fatal("conexão recusada deveria dar erro")
	}
}

func TestSenhaNaoSupervisionadaDispensaPermissao(t *testing.T) {
	host, view, hr, vr := pair(t)
	if err := host.SetPassword("segredo123"); err != nil {
		t.Fatal(err)
	}
	if err := view.Connect(host.myID, "segredo123", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	select {
	case <-vr.frame:
	case <-time.After(10 * time.Second):
		t.Fatal("nenhum frame recebido")
	}
	if hr.count("request") != 0 {
		t.Fatal("com a senha certa não deveria pedir permissão")
	}
}

func TestIDNumerico(t *testing.T) {
	host, _, _, _ := pair(t)
	if len(host.myID) != 9 {
		t.Fatalf("ID %q não tem 9 dígitos", host.myID)
	}
}

// Caminho real pela internet: o host se anuncia na DHT e o viewer acha só pelo ID.
func TestViaDHT(t *testing.T) {
	if testing.Short() {
		t.Skip("usa a internet")
	}
	hr, vr := newRecorder(true), newRecorder(true)
	host := newPeer(t, true, hr)
	view := newPeer(t, true, vr)

	var err error
	for i := 0; i < 8; i++ { // dá tempo para entrar na DHT e publicar o anúncio
		time.Sleep(10 * time.Second)
		if err = view.Connect(host.myID, "", false); err == nil {
			break
		}
		t.Log("tentativa", i+1, "->", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	select {
	case <-vr.frame:
	case <-time.After(10 * time.Second):
		t.Fatal("nenhum frame recebido")
	}
}

// Um viewer com duas abas: frames de cada host chegam separados, e fechar uma não derruba a outra.
func TestDuasSessoesSimultaneas(t *testing.T) {
	h1r, h2r, vr := newRecorder(true), newRecorder(true), newRecorder(true)
	h1 := newPeer(t, true, h1r)
	h2 := newPeer(t, true, h2r)
	view := newPeer(t, true, vr)
	for _, h := range []*App{h1, h2} {
		view.h.Peerstore().AddAddrs(h.h.ID(), h.h.Addrs(), peerstore.PermanentAddrTTL)
		if err := view.Connect(h.myID, "", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := view.Connect(h1.myID, "", false); err != nil { // repetir o ID não abre outra sessão
		t.Fatal(err)
	}
	waitFrames := func(id string) {
		t.Helper()
		for i := 0; i < 100 && vr.count("frame:"+id) == 0; i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if vr.count("frame:"+id) == 0 {
			t.Fatalf("nenhum frame do computador %s", id)
		}
	}
	waitFrames(h1.myID)
	waitFrames(h2.myID)

	view.Disconnect(h1.myID)
	before := vr.count("frame:" + h2.myID)
	time.Sleep(time.Second)
	if vr.count("frame:"+h2.myID) <= before {
		t.Fatal("a sessão 2 parou depois de fechar a sessão 1")
	}
	view.Disconnect(h2.myID)
}

func TestPacerAjustaQualidade(t *testing.T) {
	p := newPacer("equilibrada", 15)
	if p.sent() != 70 {
		t.Fatal("qualidade inicial deveria ser 70")
	}
	p.sentAt = []time.Time{time.Now().Add(-500 * time.Millisecond)} // confirmação lenta
	p.ack()
	if p.quality != 60 {
		t.Fatalf("confirmação lenta deveria baixar para 60, está em %d", p.quality)
	}
	p.sentAt = []time.Time{time.Now()} // confirmação rápida
	p.ack()
	if p.quality != 65 {
		t.Fatalf("confirmação rápida deveria subir para 65, está em %d", p.quality)
	}
	for i := 0; i < 20; i++ { // nunca passa dos limites
		p.sentAt = []time.Time{time.Now().Add(-time.Second)}
		p.ack()
	}
	if p.quality != p.minQ {
		t.Fatalf("piso deveria ser %d, está em %d", p.minQ, p.quality)
	}
}

// O host só manda um quadro novo depois que o viewer confirma: nunca mais que maxInflight em trânsito.
func TestPacerLimitaQuadrosEmTransito(t *testing.T) {
	p := newPacer("equilibrada", 15)
	for i := 0; i < maxInflight; i++ {
		select {
		case <-p.tokens:
		default:
			t.Fatalf("deveria haver %d envios liberados", maxInflight)
		}
	}
	select {
	case <-p.tokens:
		t.Fatal("não deveria liberar outro envio antes da confirmação")
	default:
	}
	p.ack()
	select {
	case <-p.tokens:
	default:
		t.Fatal("a confirmação deveria liberar um envio")
	}
}

// O frontend busca o quadro em binário pelo handler HTTP.
func TestQuadroEmBinario(t *testing.T) {
	host, view, _, vr := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	select {
	case <-vr.frame:
	case <-time.After(10 * time.Second):
		t.Fatal("nenhum frame recebido")
	}
	w := httptest.NewRecorder()
	frameHandler{view}.ServeHTTP(w, httptest.NewRequest("GET", "/frame/"+host.myID, nil))
	b := w.Body.Bytes()
	if w.Code != 200 || len(b) < 1000 || b[0] != 0xFF || b[1] != 0xD8 {
		t.Fatalf("esperava um JPEG, veio status %d com %d bytes", w.Code, len(b))
	}
	w = httptest.NewRecorder()
	frameHandler{view}.ServeHTTP(w, httptest.NewRequest("GET", "/frame/000000000", nil))
	if w.Code != 404 {
		t.Fatalf("sessão inexistente deveria dar 404, deu %d", w.Code)
	}
}

// O nome do viewer (das configurações dele) chega ao aviso de pedido de acesso do host.
func TestNomeDoViewerNoPedido(t *testing.T) {
	host, view, hr, _ := pair(t)
	if err := view.SaveSettings(func() Settings { s := view.settings(); s.Name = "Notebook da Ana"; return s }()); err != nil {
		t.Fatal(err)
	}
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	want := numID(view.h.ID()) + "|Notebook da Ana"
	if len(hr.asked) != 1 || hr.asked[0] != want {
		t.Fatalf("o pedido deveria mostrar %q, mostrou %v", want, hr.asked)
	}
}

func TestSoComputadoresPermitidos(t *testing.T) {
	host, view, hr, _ := pair(t)
	s := host.settings()
	s.OnlyAllowed = true
	if err := host.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := view.Connect(host.myID, "", false); err == nil {
		t.Fatal("um ID fora da lista deveria ser recusado")
	}
	if hr.count("request") != 0 {
		t.Fatal("ID fora da lista não deveria nem chegar a pedir permissão")
	}
	s.Allowed = []Allowed{{Name: "Viewer", ID: numID(view.h.ID())}}
	if err := host.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatalf("um ID da lista deveria poder pedir acesso: %v", err)
	}
	view.Disconnect(host.myID)
}

// Com o controle desligado, o viewer vê a tela mas o host avisa que é só visualização.
func TestSoVisualizacao(t *testing.T) {
	host, view, _, vr := pair(t)
	s := host.settings()
	s.AllowControl = false
	if err := host.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	select {
	case <-vr.frame:
	case <-time.After(10 * time.Second):
		t.Fatal("nenhum frame recebido")
	}
	if vr.count("mode") != 1 {
		t.Fatalf("o viewer deveria ser avisado de que é só visualização (avisos: %d)", vr.count("mode"))
	}
}

func TestConfiguracoesValidamESalvam(t *testing.T) {
	a := &App{dir: t.TempDir()}
	a.loadSettings()
	if s := a.settings(); s.AskSeconds != 30 || !s.AllowControl || s.FPS != 30 {
		t.Fatalf("padrões inesperados: %+v", s)
	}
	ruins := map[string]func(*Settings){
		"tempo":      func(s *Settings) { s.AskSeconds = 7 },
		"prioridade": func(s *Settings) { s.Priority = "x" },
		"fps":        func(s *Settings) { s.FPS = 99 },
		"porta":      func(s *Settings) { s.Port = 80 },
		"id":         func(s *Settings) { s.Allowed = []Allowed{{ID: "123"}} },
	}
	for nome, muda := range ruins {
		s := a.settings()
		muda(&s)
		if a.SaveSettings(s) == nil {
			t.Errorf("%s inválido deveria ser rejeitado", nome)
		}
	}
	s := a.settings()
	s.Name = "  Meu\tPC  "
	s.Port = 40000
	s.Allowed = []Allowed{{Name: "A", ID: "482 913 077"}}
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	b := &App{dir: a.dir} // outra execução do app lê o que foi salvo
	b.loadSettings()
	got := b.settings()
	if got.Name != "MeuPC" || got.Port != 40000 || len(got.Allowed) != 1 || got.Allowed[0].ID != "482913077" {
		t.Fatalf("configurações salvas não voltaram certas: %+v", got)
	}
}

// Host com senha: senha errada é recusada na hora, sem incomodar o usuário com um pedido.
func TestSenhaErradaNaoEntra(t *testing.T) {
	host, view, hr, _ := pair(t)
	host.SetPassword("segredo123")
	err := view.Connect(host.myID, "errada", false)
	if err == nil || err.Error() != ErrBadPassword {
		t.Fatalf("esperava %s, veio %v", ErrBadPassword, err)
	}
	if hr.count("request") != 0 {
		t.Fatal("senha errada não deve abrir o pedido de permissão")
	}
}

// Host com senha: sem senha o viewer é avisado para digitá-la, ou pode pedir permissão.
func TestSenhaNecessariaOuPedirPermissao(t *testing.T) {
	host, view, hr, _ := pair(t)
	host.SetPassword("segredo123")
	err := view.Connect(host.myID, "", false)
	if err == nil || err.Error() != ErrNeedPassword {
		t.Fatalf("esperava %s, veio %v", ErrNeedPassword, err)
	}
	if hr.count("request") != 0 {
		t.Fatal("sem senha não deve abrir o pedido enquanto o viewer não pedir permissão")
	}
	if err := view.Connect(host.myID, "", true); err != nil {
		t.Fatalf("pedir permissão deveria funcionar: %v", err)
	}
	defer view.Disconnect(host.myID)
	if hr.count("request") != 1 {
		t.Fatalf("esperava 1 pedido de permissão, houve %d", hr.count("request"))
	}
}

// "Lembrar senha": depois de salva, conectar de novo não pede nada.
func TestSenhaLembrada(t *testing.T) {
	host, view, hr, _ := pair(t)
	host.SetPassword("segredo123")
	if err := view.SavePassword(host.myID, "segredo123"); err != nil {
		t.Fatal(err)
	}
	if got := view.savedPassword(host.myID); got != "segredo123" {
		t.Fatalf("a senha salva não voltou igual: %q", got)
	}
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatalf("com a senha lembrada deveria conectar direto: %v", err)
	}
	if hr.count("request") != 0 {
		t.Fatal("com a senha lembrada não deve pedir permissão")
	}
	view.Disconnect(host.myID)

	if err := view.ForgetPassword(host.myID); err != nil {
		t.Fatal(err)
	}
	if len(view.SavedPasswords()) != 0 {
		t.Fatal("esquecer a senha deveria limpar a lista")
	}
	err := view.Connect(host.myID, "", false)
	if err == nil || err.Error() != ErrNeedPassword {
		t.Fatalf("sem a senha lembrada deveria pedir de novo, veio %v", err)
	}
}

// Cancelar interrompe a conexão enquanto o outro computador ainda não respondeu ao pedido.
func TestCancelarConexao(t *testing.T) {
	host, view, _, _ := pair(t)
	release := make(chan struct{})
	defer close(release)
	host.askFn = func(string, string) bool { <-release; return true } // usuário "pensando"
	done := make(chan error, 1)
	go func() { done <- view.Connect(host.myID, "", false) }()
	time.Sleep(500 * time.Millisecond)
	view.Cancel(host.myID)
	select {
	case err := <-done:
		if err == nil || err.Error() != ErrCanceled {
			t.Fatalf("esperava %s, veio %v", ErrCanceled, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelar não interrompeu a conexão")
	}
}

// área de transferência simulada, uma por computador
type fakeClip struct {
	mu sync.Mutex
	s  string
}

func (f *fakeClip) get() string  { f.mu.Lock(); defer f.mu.Unlock(); return f.s }
func (f *fakeClip) set(s string) { f.mu.Lock(); f.s = s; f.mu.Unlock() }

var fakes sync.Map // *App -> *fakeClip

func clipOf(a *App) *fakeClip { v, _ := fakes.Load(a); return v.(*fakeClip) }

func eventually(d time.Duration, cond func() bool) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// Ctrl+C num computador, Ctrl+V no outro: o texto copiado atravessa a sessão nos dois sentidos.
func TestAreaDeTransferenciaNosDoisSentidos(t *testing.T) {
	host, view, _, _ := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	view.SetActiveSession(host.myID)

	clipOf(view).set("copiado no viewer")
	if !eventually(3*time.Second, func() bool { return clipOf(host).get() == "copiado no viewer" }) {
		t.Fatalf("o texto do viewer não chegou ao host: %q", clipOf(host).get())
	}
	clipOf(host).set("copiado no host")
	if !eventually(3*time.Second, func() bool { return clipOf(view).get() == "copiado no host" }) {
		t.Fatalf("o texto do host não chegou ao viewer: %q", clipOf(view).get())
	}
	time.Sleep(300 * time.Millisecond) // sem eco: ninguém devolve o texto de volta e muda o que o outro copiou
	if clipOf(host).get() != "copiado no host" || clipOf(view).get() != "copiado no host" {
		t.Fatalf("o texto mudou depois de sincronizar: host=%q viewer=%q", clipOf(host).get(), clipOf(view).get())
	}
}

// Quem já copiou antes de abrir a aba também consegue colar: o texto vai ao entrar na aba.
func TestAreaDeTransferenciaCopiadaAntesDeEntrarNaAba(t *testing.T) {
	host, view, _, _ := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	clipOf(view).set("copiado antes")
	view.SetActiveSession(host.myID)
	if !eventually(3*time.Second, func() bool { return clipOf(host).get() == "copiado antes" }) {
		t.Fatal("o texto copiado antes de entrar na aba não chegou")
	}
}

// Sem aba ativa (início, configurações, outra aba) nada do que o usuário copia sai do computador.
func TestAreaDeTransferenciaSoNaAbaAtiva(t *testing.T) {
	host, view, _, _ := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	view.SetActiveSession("")
	clipOf(view).set("segredo copiado")
	time.Sleep(500 * time.Millisecond)
	if got := clipOf(host).get(); got != "" {
		t.Fatalf("o texto vazou para um computador de aba inativa: %q", got)
	}
}

func TestAreaDeTransferenciaBloqueadaNoHost(t *testing.T) {
	host, view, _, _ := pair(t)
	s := host.settings()
	s.AllowClipboard = false
	if err := host.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	view.SetActiveSession(host.myID)
	clipOf(view).set("não deveria chegar")
	clipOf(host).set("nem este")
	time.Sleep(500 * time.Millisecond)
	if clipOf(host).get() != "nem este" || clipOf(view).get() != "não deveria chegar" {
		t.Fatalf("com a área de transferência bloqueada nada deveria mudar: host=%q viewer=%q", clipOf(host).get(), clipOf(view).get())
	}
}

func (r *recorder) monitors() [2]int { r.mu.Lock(); defer r.mu.Unlock(); return r.mon }

// O host informa quantos monitores tem e qual mostra; o viewer escolhe outro. Funciona com 1 monitor
// (o índice inválido volta ao principal) e com vários (troca de verdade).
func TestEscolherMonitor(t *testing.T) {
	host, view, _, vr := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	n := screenshot.NumActiveDisplays()
	if !eventually(3*time.Second, func() bool { return vr.monitors() == [2]int{n, 0} }) {
		t.Fatalf("o host deveria informar %d monitor(es) e o 0 em uso, veio %v", n, vr.monitors())
	}
	want := min(1, n-1) // com 1 monitor, pedir o segundo não existe e fica no principal
	view.SetMonitor(host.myID, 1)
	if !eventually(3*time.Second, func() bool { return vr.monitors() == [2]int{n, want} }) {
		t.Fatalf("depois de escolher o monitor 2 esperava %v, veio %v", [2]int{n, want}, vr.monitors())
	}
	view.SetMonitor(host.myID, 99) // inexistente: volta para o principal
	if !eventually(3*time.Second, func() bool { return vr.monitors() == [2]int{n, 0} }) {
		t.Fatalf("um monitor inexistente deveria voltar ao principal, veio %v", vr.monitors())
	}
	if n > 1 { // a imagem do outro monitor chega de fato
		before := vr.count("frame:" + host.myID)
		view.SetMonitor(host.myID, 1)
		if !eventually(3*time.Second, func() bool { return vr.count("frame:"+host.myID) > before }) {
			t.Fatal("trocou de monitor mas não chegou imagem nova")
		}
	}
}

// Depois de conectar uma vez, os endereços do computador ficam salvos: ao reiniciar o app, reconectar
// não precisa procurar na rede P2P (que leva de 20 s a mais de 1 minuto).
func TestReconectarSemProcurarNaRede(t *testing.T) {
	host, view1, _, _ := pair(t)
	if err := view1.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	view1.Disconnect(host.myID)
	if len(view1.readPeers()) != 1 {
		t.Fatalf("deveria ter salvo o endereço do computador, salvou %v", view1.readPeers())
	}
	view1.h.Close() // "fecha" o app do viewer

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	view2 := &App{ctx: ctx, dir: view1.dir, noMDNS: true} // mesma pasta de dados, memória vazia
	view2.emit = func(string, ...any) {}
	view2.askFn = newRecorder(true).ask
	if err := view2.initP2P(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { view2.h.Close() })
	if len(view2.h.Peerstore().Addrs(host.h.ID())) == 0 {
		t.Fatal("o endereço salvo deveria ter voltado para o peerstore ao abrir o app")
	}
	t0 := time.Now()
	if err := view2.Connect(host.myID, "", false); err != nil {
		t.Fatalf("deveria reconectar com o endereço salvo, sem a rede P2P: %v", err)
	}
	defer view2.Disconnect(host.myID)
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("reconectar levou %v; com o endereço salvo deveria ser quase instantâneo", d)
	}
}

func (r *recorder) presence(id string) (online bool, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.online[id], r.checks[id]
}

// Computador que responde aparece online; quando ele sai do ar, offline.
func TestPresencaOnlineEOffline(t *testing.T) {
	host, view, _, vr := pair(t)
	view.probeBudget = 3 * time.Second
	view.CheckOnline([]string{host.myID, view.myID, "123"}) // o próprio ID e IDs inválidos são ignorados
	if !eventually(5*time.Second, func() bool { on, n := vr.presence(host.myID); return n == 1 && on }) {
		t.Fatal("o computador no ar deveria aparecer online")
	}
	if n := vr.count("presence"); n != 1 {
		t.Fatalf("só o host deveria ser verificado, houve %d resultados", n)
	}
	if len(view.h.Network().ConnsToPeer(host.h.ID())) != 0 {
		t.Fatal("a conexão aberta só para verificar deveria ser fechada")
	}

	host.h.Close() // o computador sai do ar
	view.CheckOnline([]string{host.myID})
	if !eventually(8*time.Second, func() bool { on, n := vr.presence(host.myID); return n == 2 && !on }) {
		t.Fatal("o computador fora do ar deveria aparecer offline")
	}
}

// Pedir a verificação de novo enquanto a anterior roda não dispara outra.
func TestPresencaNaoRepeteVerificacaoEmAndamento(t *testing.T) {
	host, view, _, vr := pair(t)
	view.probeBudget = 3 * time.Second
	for i := 0; i < 5; i++ {
		view.CheckOnline([]string{host.myID})
	}
	if !eventually(5*time.Second, func() bool { _, n := vr.presence(host.myID); return n >= 1 }) {
		t.Fatal("a verificação não terminou")
	}
	time.Sleep(300 * time.Millisecond)
	if _, n := vr.presence(host.myID); n != 1 {
		t.Fatalf("esperava 1 resultado para 5 pedidos seguidos, houve %d", n)
	}
}

// Computadores achados na rede local (mDNS) entram na lista enquanto respondem e saem depois do prazo.
func TestEncontradosNaRedeLocal(t *testing.T) {
	host, view, _, _ := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	view.Disconnect(host.myID)
	if len(view.LanPeers()) != 0 {
		t.Fatal("ninguém foi achado na rede local ainda")
	}
	view.noteLAN(host.h.ID())
	if got := view.LanPeers(); len(got) != 1 || got[0] != host.myID {
		t.Fatalf("esperava [%s], veio %v", host.myID, got)
	}
	view.lanTTL = time.Millisecond
	time.Sleep(20 * time.Millisecond)
	if len(view.LanPeers()) != 0 {
		t.Fatal("depois do prazo o computador deveria sair da lista")
	}
}
