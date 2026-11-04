package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/kbinani/screenshot"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/multiformats/go-multihash"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

const proto = "/cdesk/1.0.0"

// hello é a primeira linha que o viewer manda ao host: senha de acesso não supervisionado (pode
// ser vazia), o nome do viewer e a preferência de imagem.
type hello struct {
	PW       string `json:"pw"`
	Name     string `json:"name"`
	Priority string `json:"prio"`
	FPS      int    `json:"fps"`
	Ask      bool   `json:"ask"`    // pedir permissão ao usuário do host mesmo que ele tenha senha de acesso
	Tiles    bool   `json:"tiles"`  // sabe receber só as áreas alteradas da tela (msgTiles)
	NoWall   bool   `json:"nowall"` // esconder o papel de parede do host durante a sessão
}

// Event é um evento de input vindo do viewer. X/Y são normalizados (0..1).
type Event struct {
	T string  `json:"t"` // move, down, up, wheel, kdown, kup
	X float64 `json:"x"`
	Y float64 `json:"y"`
	B int     `json:"b"` // botão do mouse: 0 esq, 1 meio, 2 dir
	K int     `json:"k"` // keyCode (VK no Windows)
	D int     `json:"d"` // delta do scroll
	S string  `json:"s"` // texto da área de transferência (t = clip)
	// t = quality: mudança de qualidade no meio da sessão (S = prioridade)
	Adapt  *bool `json:"adapt,omitempty"`
	Detail bool  `json:"detail,omitempty"`
	FPS    int   `json:"fps,omitempty"`
}

// App é exposto ao frontend pelo Wails (window.go.main.App.*).
type App struct {
	ctx    context.Context
	dir    string                         // pasta de dados; vazio = pasta de config do usuário
	emit   func(name string, data ...any) // evento para o frontend (trocado nos testes)
	askFn  func(viewer, name string) bool // mostra o pedido de acesso (trocado nos testes)
	noMDNS bool

	h    host.Host
	dht  *dht.IpfsDHT
	err  string
	myID string // ID numérico de 9 dígitos, derivado da chave

	busy     atomic.Bool  // host: pedido de acesso ou sessão em andamento
	quitting atomic.Bool  // "Sair" foi pedido: fechar a janela não deve só esconder na bandeja
	pwFails  atomic.Int32 // tentativas seguidas com senha errada (freio contra adivinhação)
	savedMu  sync.Mutex   // saved.json
	peersMu  sync.Mutex   // peers.json

	mu     sync.Mutex
	pwHash string    // hash da senha de acesso não supervisionado ("" = sem senha)
	cfg    Settings  // configurações do usuário
	in     *hostConn // sessão como host
	// área de transferência compartilhada (clip.go)
	clipGet    func() string
	clipSet    func(string)
	clipEvery  time.Duration
	lastClip   string                  // último texto visto na área de transferência local
	activeView string                  // aba em uso: só ela troca área de transferência
	out        map[string]*viewSession // sessões como viewer, por ID do computador remoto (uma aba cada)
	jobs       map[string]*job         // transferências de arquivos em andamento, por chave (files.go)
	// janela da sessão no host (session_win.go) e o que ela mostra
	sessWin  func(viewer, name string, since time.Time) (stop func())
	hostChat []chatMsg
	hostLog  []chatMsg
	ipc      *ipcServer
	// conexões em andamento (abrir a sessão pode levar até 1 min), para poder cancelar
	connecting map[string]context.CancelFunc
	// quem está online (presence.go)
	probing      map[string]bool       // IDs sendo verificados agora
	probeSem     chan struct{}         // limita as verificações simultâneas
	serverUp     atomic.Bool           // o servidor próprio respondeu na última chamada
	serverDownAt atomic.Int64          // quando o servidor deixou de responder (unix nano); evita esperar o timeout a cada busca
	reservedAt   atomic.Int64          // quando reservamos vaga no relay do servidor (unix nano)
	lan          map[peer.ID]time.Time // computadores achados por mDNS e quando
	lanTTL       time.Duration
	probeBudget  time.Duration // 0 = probeBudget (os testes encurtam)
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.emit = func(name string, data ...any) { wr.EventsEmit(ctx, name, data...) }
	a.defaultClipboard()
	a.askFn = a.askWindow
	a.sessWin = a.sessionWindow
	if err := a.initP2P(); err != nil {
		a.err = err.Error()
	}
	a.startTray()
	go a.clipLoop()
	if a.settings().CheckUpdates {
		go func() {
			if u, err := a.CheckUpdate(); err == nil && u.Newer {
				a.emit("update", u.Latest, u.URL)
			}
		}()
	}
}

// numID transforma o PeerID em um ID de 9 dígitos. O viewer confere esse hash no peer
// encontrado, então quem responde pelo ID precisa ter a chave que gera aquele número.
// shortcut: 9 dígitos podem ser forjados com força bruta de chaves; subir para 10+ se virar problema.
func numID(id peer.ID) string {
	h := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%09d", binary.BigEndian.Uint64(h[:8])%1_000_000_000)
}

func provKey(numericID string) cid.Cid {
	mh, _ := multihash.Sum([]byte("cdesk:"+numericID), multihash.SHA2_256, -1)
	return cid.NewCidV1(cid.Raw, mh)
}

func hashPw(pw string) string {
	h := sha256.Sum256([]byte("cdesk:" + pw))
	return hex.EncodeToString(h[:])
}

type mdnsNotifee struct{ a *App }

func (n mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	n.a.noteLAN(pi.ID)
	go n.a.h.Connect(n.a.ctx, pi)
}

func (a *App) initP2P() error {
	if a.dir == "" {
		d, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		a.dir = filepath.Join(d, "CDesk")
	}
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return err
	}
	a.loadSettings()
	keyPath := filepath.Join(a.dir, "key")
	kb, err := os.ReadFile(keyPath)
	if err != nil {
		k, _, _ := crypto.GenerateEd25519Key(rand.Reader)
		kb, _ = crypto.MarshalPrivateKey(k)
		if err := os.WriteFile(keyPath, kb, 0o600); err != nil {
			return err
		}
	}
	priv, err := crypto.UnmarshalPrivateKey(kb)
	if err != nil {
		return err
	}
	if b, err := os.ReadFile(filepath.Join(a.dir, "unattended")); err == nil {
		a.pwHash = string(b)
	}

	boot := dht.GetDefaultBootstrapPeerAddrInfos()
	relays := slices.Clone(boot) // relays para quem está atrás de NAT: os públicos e, se houver, o próprio
	if ai, err := peer.AddrInfoFromString(a.cfg.serverAddress()); a.cfg.UseServer && err == nil {
		relays = append([]peer.AddrInfo{*ai}, relays...)
	}
	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.UserAgent("cdesk/" + version), // o servidor próprio só atende apps CDesk
		// shortcut: relays públicos do bootstrap IPFS, trocar por relays próprios/da comunidade
		libp2p.EnableAutoRelayWithStaticRelays(relays),
	}
	if a.cfg.UPnP {
		opts = append(opts, libp2p.NATPortMap())
	}
	if a.cfg.HolePunch {
		opts = append(opts, libp2p.EnableHolePunching())
	}
	if p := a.cfg.Port; p != 0 {
		opts = append(opts, libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", p), fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", p),
			fmt.Sprintf("/ip6/::/tcp/%d", p), fmt.Sprintf("/ip6/::/udp/%d/quic-v1", p)))
	}
	a.h, err = libp2p.New(opts...)
	if err != nil {
		return err
	}
	a.myID = numID(a.h.ID())
	a.probeSem = make(chan struct{}, probeWorkers)
	a.loadPeers() // endereços dos computadores a que já conectamos: reconectar sem procurar na rede
	if a.dht, err = dht.New(a.h); err != nil {
		return err
	}
	a.h.SetStreamHandler(proto, a.serve)
	a.h.SetStreamHandler(filesProto, a.serveFiles)
	if !a.noMDNS && a.cfg.MDNS { // achar PCs na mesma rede sem depender da internet
		if s := mdns.NewMdnsService(a.h, "cdesk", mdnsNotifee{a}); s != nil {
			s.Start()
		}
	}

	// Os endereços de relay só aparecem depois que o AutoNAT conclui (~1 min), então o anúncio
	// precisa ser refeito quando os endereços mudam, senão fica com endereços que ninguém alcança.
	sub, err := a.h.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		return err
	}
	go func() {
		for _, p := range relays {
			go a.h.Connect(a.ctx, p)
		}
		if rs, err := a.h.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated)); err == nil { // vale também com a opção desligada: ligar nas Configurações passa a valer na hora
			go a.registerLoop(rs)
		}
		a.dht.Bootstrap(a.ctx)
		// anunciar antes de a tabela de rotas ter peers falha e só tenta de novo em 15 s: espera ter alguns
		for deadline := time.Now().Add(20 * time.Second); a.dht.RoutingTable().Size() < 6 && time.Now().Before(deadline); {
			select {
			case <-time.After(250 * time.Millisecond):
			case <-a.ctx.Done():
				return
			}
		}
		next := time.After(0)
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-sub.Out():
			case <-next:
			}
			wait := 10 * time.Minute
			if a.dht.Provide(a.ctx, provKey(a.myID), true) != nil {
				wait = 15 * time.Second
			}
			next = time.After(wait)
		}
	}()
	return nil
}

// Info devolve o ID deste computador e se há senha de acesso não supervisionado.
func (a *App) Info() map[string]any {
	if a.h == nil {
		return map[string]any{"err": a.err}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"id": a.myID, "hasPw": a.pwHash != ""}
}

// Status diz se já entramos na rede (DHT) e quantos computadores a rede já conhece.
func (a *App) Status() map[string]any {
	n := 0
	if a.dht != nil {
		n = a.dht.RoutingTable().Size()
	}
	return map[string]any{"online": n > 0, "known": n, "server": a.serverState()}
}

// SetPassword define a senha de acesso não supervisionado; vazio remove.
func (a *App) SetPassword(pw string) error {
	if pw != "" && len(pw) < 6 {
		return fmt.Errorf("A senha precisa ter pelo menos 6 caracteres.")
	}
	h := ""
	if pw != "" {
		h = hashPw(pw)
	}
	path := filepath.Join(a.dir, "unattended")
	if h == "" {
		os.Remove(path)
	} else if err := os.WriteFile(path, []byte(h), 0o600); err != nil {
		return err
	}
	a.mu.Lock()
	a.pwHash = h
	a.mu.Unlock()
	return nil
}

// ---- lado host ----

func (a *App) hasPassword() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pwHash != ""
}

func (a *App) passwordOK(pw string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pwHash != "" && pw != "" && subtle.ConstantTimeCompare([]byte(hashPw(pw)), []byte(a.pwHash)) == 1
}

// EndSession encerra a sessão em que este computador está sendo controlado.
func (a *App) EndSession() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.in != nil {
		a.in.hostEnded.Store(true)
		a.in.s.Reset()
	}
}

func (a *App) serve(s network.Stream) {
	defer s.Close()
	if !a.busy.CompareAndSwap(false, true) {
		s.Write([]byte("BUSY\n"))
		return
	}
	defer a.busy.Store(false)

	r := bufio.NewReader(s)
	s.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := r.ReadString('\n')
	if err != nil {
		return
	}
	s.SetReadDeadline(time.Time{})
	var hi hello
	if json.Unmarshal([]byte(line), &hi) != nil {
		return
	}
	viewer := numID(s.Conn().RemotePeer())
	cfg := a.settings()
	if cfg.OnlyAllowed && !slices.ContainsFunc(cfg.Allowed, func(p Allowed) bool { return p.ID == viewer }) {
		s.Write([]byte("NO\n"))
		return
	}
	switch {
	case a.passwordOK(hi.PW): // senha de acesso não supervisionado certa entra direto
		a.pwFails.Store(0)
	case a.hasPassword() && !hi.Ask: // há senha: o viewer precisa digitá-la (ou pedir permissão)
		reply := "PW\n"
		if hi.PW != "" {
			reply = "BADPW\n"
			// shortcut: freio simples contra adivinhar a senha, sem limite por peer
			time.Sleep(time.Duration(min(a.pwFails.Add(1), 10)) * time.Second)
		}
		s.Write([]byte(reply))
		return
	default: // sem senha, ou o viewer pediu permissão: o usuário decide na janela do pedido
		if !a.askFn(viewer, cleanText(hi.Name, 40)) {
			s.Write([]byte("NO\n"))
			return
		}
	}
	if cfg.AllowControl {
		s.Write([]byte("OK\n"))
	} else {
		s.Write([]byte("OKV\n")) // só visualização
	}

	conn := &hostConn{s: s, quit: make(chan struct{})}
	conn.ctl.Store(cfg.AllowControl) // permissões desta sessão; a janela da sessão pode mudar durante ela
	conn.clip.Store(cfg.AllowClipboard)
	conn.files.Store(cfg.AllowFiles)
	name := cleanText(hi.Name, 40)
	a.mu.Lock()
	a.in = conn
	a.hostChat = []chatMsg{{Who: "sys", Text: "Pedido de sessão recebido", At: nowMs()}, {Who: "sys", Text: "Sessão iniciada", At: nowMs()}}
	a.hostLog = nil
	a.mu.Unlock()
	a.emit("hosting", viewer, name)
	if hi.NoWall {
		defer hideWallpaper()()
	}
	go a.hostAux(conn)
	defer a.hostCleanup(conn)
	if a.sessWin != nil {
		defer a.sessWin(viewer, name, time.Now())()
	}
	defer func() {
		a.mu.Lock()
		a.in = nil
		a.mu.Unlock()
		a.emit("hostingEnded")
	}()
	defer func() {
		if conn.lockEnd.Load() && !conn.hostEnded.Load() {
			lockWorkstation()
		}
	}()

	conn.tiles = hi.Tiles
	p := newPacer(hi.Priority, hi.FPS)
	go sendFrames(conn, p)
	conn.sendInfo()        // quantos monitores há e qual está sendo mostrado
	held := map[int]bool{} // teclas que o viewer está apertando
	defer func() {
		for vk := range held {
			releaseKey(vk) // o viewer saiu com a tecla apertada: solta, senão ela fica presa
		}
	}()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 2*maxClip+(64<<10)) // linhas com texto copiado podem ser grandes
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch {
		case e.T == "ack":
			p.ack()
		case e.T == "clip":
			if conn.clip.Load() && conn.ctl.Load() {
				a.setClip(e.S)
			}
		case e.T == "monitor": // o viewer escolheu outro monitor
			conn.monitor.Store(int32(e.K))
			conn.sendInfo()
		case a.hostEvent(conn, e):
		case e.T == "lockend": // o viewer pede bloquear a tela deste computador quando a sessão acabar
			conn.lockEnd.Store(e.K == 1 && conn.ctl.Load())
		case e.T == "quality":
			p.configure(e.S, e.Adapt == nil || *e.Adapt, e.Detail, e.FPS)
		case e.T == "chat":
			a.hostChatAdd("them", e.S)
		case conn.ctl.Load():
			switch e.T {
			case "kdown":
				held[e.K] = true
			case "kup":
				delete(held, e.K)
			}
			inject(e, screenshot.GetDisplayBounds(conn.currentMonitor()))
		}
	}
}

// ---- lado viewer ----

// find acha o peer cujo ID numérico é id. Os três caminhos (endereços já conhecidos, servidor CDesk e DHT)
// correm ao mesmo tempo e vale o primeiro que conectar: em série, a espera de um (endereço velho, servidor
// fora do ar) somava com a do próximo.
func (a *App) find(ctx context.Context, id string) (peer.ID, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // derruba os caminhos que perderam
	ways := []func(context.Context, string) (peer.ID, bool){a.viaKnown, a.viaServer, a.viaDHT}
	found := make(chan peer.ID, len(ways))
	for _, w := range ways {
		go func() {
			if p, ok := w(ctx, id); ok {
				found <- p
			} else {
				found <- ""
			}
		}()
	}
	for range ways {
		select {
		case p := <-found:
			if p != "" {
				return p, nil
			}
		case <-ctx.Done():
			return "", fmt.Errorf("Computador não encontrado. Confira o ID e se o CDesk está aberto nele.")
		}
	}
	return "", fmt.Errorf("Computador não encontrado. Confira o ID e se o CDesk está aberto nele.")
}

// viaKnown tenta os endereços que já temos do computador (rede local, conexões anteriores).
func (a *App) viaKnown(ctx context.Context, id string) (peer.ID, bool) {
	for _, p := range a.h.Peerstore().Peers() {
		if p == a.h.ID() || numID(p) != id {
			continue
		}
		// endereço lembrado pode estar velho (IP mudou): limita a espera, os outros caminhos seguem em paralelo
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := a.h.Connect(cctx, peer.AddrInfo{ID: p})
		cancel()
		if err == nil {
			return p, true
		}
	}
	return "", false
}

// viaDHT procura quem anunciou o ID na DHT. Se o registro veio sem endereços, pergunta onde o peer está.
func (a *App) viaDHT(ctx context.Context, id string) (peer.ID, bool) {
	for p := range a.dht.FindProvidersAsync(ctx, provKey(id), 1) {
		if numID(p.ID) != id {
			continue
		}
		if len(p.Addrs) == 0 {
			if ai, err := a.dht.FindPeer(ctx, p.ID); err == nil {
				p = ai
			}
		}
		if a.h.Connect(ctx, p) == nil {
			return p.ID, true
		}
	}
	return "", false
}

// waitDirect espera o hole punching trocar a conexão de relay por uma direta. O relay público
// corta a sessão em ~2 min / 128 KB, o que não aguenta vídeo.
func (a *App) waitDirect(ctx context.Context, pid peer.ID) bool {
	deadline := time.After(20 * time.Second)
	for {
		for _, c := range a.h.Network().ConnsToPeer(pid) {
			if !c.Stat().Limited {
				return true
			}
		}
		select {
		case <-time.After(100 * time.Millisecond): // o hole punching costuma levar 1 a 3 s: não espera meio segundo a mais
		case <-deadline:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

// Códigos de erro que o frontend reconhece pelo texto do erro.
const (
	ErrNeedPassword = "SENHA_NECESSARIA" // o outro computador tem senha de acesso e nenhuma foi enviada
	ErrBadPassword  = "SENHA_INCORRETA"
	ErrCanceled     = "CANCELADO"
)

// Connect abre uma sessão com o computador id. Sem pw usa a senha lembrada, se houver. Com
// askPermission pede para o usuário do outro lado aceitar, mesmo que ele tenha senha de acesso.
func (a *App) Connect(id, pw string, askPermission bool) error {
	if a.h == nil {
		return fmt.Errorf("Rede P2P indisponível: %s", a.err)
	}
	id = onlyDigits(id)
	if len(id) != 9 {
		return fmt.Errorf("ID inválido. O ID tem 9 números.")
	}
	if id == a.myID {
		return fmt.Errorf("Esse é o ID deste computador. Digite o ID do outro computador.")
	}
	cctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	_, open := a.out[id]
	_, running := a.connecting[id]
	if !open && !running {
		if a.connecting == nil {
			a.connecting = map[string]context.CancelFunc{}
		}
		a.connecting[id] = cancel
	}
	a.mu.Unlock()
	if open || running { // já há uma aba com esse computador
		cancel()
		return nil
	}
	defer func() {
		a.mu.Lock()
		delete(a.connecting, id)
		a.mu.Unlock()
		cancel()
	}()
	err := a.connect(cctx, id, pw, askPermission)
	if err != nil && cctx.Err() != nil && a.ctx.Err() == nil {
		return errors.New(ErrCanceled)
	}
	return err
}

// Cancel interrompe a conexão em andamento com o computador id.
func (a *App) Cancel(id string) {
	a.mu.Lock()
	cancel := a.connecting[onlyDigits(id)]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) connect(cctx context.Context, id, pw string, askPermission bool) error {
	if pw == "" && !askPermission {
		pw = a.savedPassword(id)
	}
	ctx, cancel := context.WithTimeout(cctx, 60*time.Second)
	defer cancel()

	a.emit("status", id, 1, "Procurando o computador...")
	pid, err := a.find(ctx, id)
	if err != nil {
		return err
	}
	a.emit("status", id, 2, "Abrindo conexão direta...")
	if !a.waitDirect(ctx, pid) {
		return fmt.Errorf("Não foi possível abrir uma conexão direta. A rede de um dos computadores bloqueia conexões diretas (tente liberar o CDesk no firewall).")
	}
	s, err := a.h.NewStream(ctx, pid, proto)
	if err != nil {
		return err
	}
	a.rememberPeer(pid) // a próxima conexão com este computador não precisa procurar na rede
	a.emit("status", id, 3, "Aguardando resposta do outro computador...")
	r := bufio.NewReader(s)
	s.SetDeadline(time.Now().Add(45 * time.Second))
	stopCancel := context.AfterFunc(cctx, func() { s.SetDeadline(time.Now()) }) // cancelar interrompe a espera
	cfg := a.settings()
	hb, _ := json.Marshal(hello{PW: pw, Name: cfg.Name, Priority: cfg.Priority, FPS: cfg.FPS, Ask: askPermission, NoWall: cfg.HideWallpaper, Tiles: true})
	if _, err := s.Write(append(hb, '\n')); err != nil {
		s.Reset()
		return err
	}
	l, err := r.ReadString('\n')
	if err != nil {
		s.Reset()
		return fmt.Errorf("O outro computador não respondeu a tempo.")
	}
	switch l {
	case "OK\n", "OKV\n":
	case "PW\n":
		s.Reset()
		return errors.New(ErrNeedPassword)
	case "BADPW\n":
		s.Reset()
		return errors.New(ErrBadPassword)
	case "BUSY\n":
		s.Reset()
		return fmt.Errorf("O outro computador já está em uma sessão.")
	default:
		s.Reset()
		return fmt.Errorf("Conexão recusada pelo outro computador.")
	}
	stopCancel() // sessão aberta: cancelar não derruba mais
	s.SetDeadline(time.Time{})

	a.mu.Lock()
	if a.out == nil {
		a.out = map[string]*viewSession{}
	}
	v := &viewSession{s: s}
	a.out[id] = v
	a.mu.Unlock()
	if l == "OKV\n" {
		a.emit("mode", id, "view")
	}
	go a.readFrames(id, v, r)
	return nil
}

func (a *App) readFrames(id string, v *viewSession, r *bufio.Reader) {
	defer a.emit("closed", id)
	defer func() {
		a.mu.Lock()
		if a.out[id] == v {
			delete(a.out, id)
		}
		a.mu.Unlock()
	}()
	defer v.s.Close()
	var hdr [headerBytes]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n == 0 || n > 16<<20 {
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		if hdr[0] == msgInfo {
			var mi struct{ N, I int }
			if json.Unmarshal(buf, &mi) == nil {
				a.emit("monitors", id, mi.N, mi.I)
			}
			continue
		}
		if hdr[0] == msgChat {
			a.chatReceived(id, string(buf))
			continue
		}
		if hdr[0] == msgTiles {
			if err := v.applyTiles(buf); err != nil { // blocos que não encaixam: o outro lado está fora do protocolo
				return
			}
			a.emit("frame", id)
			v.write([]byte("{\"t\":\"ack\"}\n"))
			continue
		}
		switch hdr[0] {
		case msgAudio:
			v.audioMu.Lock()
			v.audioBuf = append(v.audioBuf, buf...)
			if over := len(v.audioBuf) - (audioRate*4)*2; over > 0 { // no máximo ~1 s guardado: o resto é atraso que não vale tocar
				v.audioBuf = v.audioBuf[over:]
			}
			v.audioMu.Unlock()
			continue
		case msgCursor:
			a.emit("cursor", id, string(buf))
			continue
		case msgFocus:
			a.emit("focus", id, string(buf))
			continue
		case msgClipFiles: // o host copiou arquivos: só vale para a aba em uso e com a opção ligada
			var m clipFilesMsg
			a.mu.Lock()
			current := a.activeView == id
			a.mu.Unlock()
			if current && v.fileClip.Load() && json.Unmarshal(buf, &m) == nil {
				go a.pullFilesFromHost(id, m)
			}
			continue
		}
		if hdr[0] == msgClip { // o host copiou algo: só vale para a aba em uso
			a.mu.Lock()
			current := a.activeView == id
			a.mu.Unlock()
			if current && !v.noClip.Load() {
				a.setClip(string(buf))
			}
			continue
		}
		v.setKey(buf)       // quadro inteiro: nova base para os blocos
		a.emit("frame", id) // só avisa; o frontend busca o quadro em /frame/<id>
		v.write([]byte("{\"t\":\"ack\"}\n"))
	}
}

// SendInput manda um evento de mouse/teclado para a sessão com o computador id.
func (a *App) SendInput(id, ev string) {
	a.mu.Lock()
	v := a.out[id]
	a.mu.Unlock()
	if v != nil {
		v.write([]byte(ev + "\n"))
	}
}

// Disconnect encerra a sessão com o computador id (fechar a aba).
func (a *App) Disconnect(id string) {
	a.mu.Lock()
	v := a.out[id]
	delete(a.out, id)
	a.mu.Unlock()
	if v != nil {
		v.s.Close()
	}
}

func (a *App) closeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, v := range a.out {
		v.s.Close()
	}
}
