package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// Cliente do servidor próprio (server/): registra os endereços deste computador e resolve ID → endereços
// sem esperar a DHT. O servidor é opcional: se não há endereço configurado ou ele está fora do ar, tudo
// segue pela DHT, mDNS e hole punching como antes. O PeerID continua sendo conferido com numID, então
// o servidor só entrega endereços e não consegue se passar por outro computador.

const dirProto = "/cdesk-dir/1.0.0"

// defaultServer é o servidor CDesk que o app usa quando o usuário não escolheu outro. O PeerID é o do server.key
// do servidor: se esse arquivo for trocado, o endereço muda e este valor precisa ser atualizado numa nova versão.
// shortcut: IP fixo; usar um domínio (/dns4/...) quando houver, para poder trocar de máquina sem nova versão.
const defaultServer = "/ip4/177.104.166.65/tcp/4001/p2p/12D3KooWQDgvjoA1xANVG6wVtzACLSkVKiazoYcXi62PFvekcnyt"

// serverAddress é o endereço personalizado das Configurações ou, vazio, o padrão.
func (s Settings) serverAddress() string {
	if s.Relay != "" {
		return s.Relay
	}
	return defaultServer
}

type dirRequest struct {
	Op    string   `json:"op"` // register | lookup
	Addrs []string `json:"addrs,omitempty"`
	ID    string   `json:"id,omitempty"`
}

type dirReply struct {
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
	Rec *struct {
		Peer  string   `json:"peer"`
		Addrs []string `json:"addrs"`
	} `json:"rec,omitempty"`
}

var errNoServer = errors.New("sem servidor configurado")

// serverAddr é o servidor configurado em Configurações > Rede, esteja ele ligado ou não.
func (a *App) serverAddr() (peer.AddrInfo, error) {
	ai, err := peer.AddrInfoFromString(a.settings().serverAddress())
	if err != nil {
		return peer.AddrInfo{}, err
	}
	return *ai, nil
}

// serverInfo é o servidor a usar agora: vazio se a opção "usar servidor CDesk" está desligada.
func (a *App) serverInfo() (peer.AddrInfo, error) {
	if !a.settings().UseServer {
		return peer.AddrInfo{}, errNoServer
	}
	return a.serverAddr()
}

// serverState resume o servidor para a tela: off (desligado nas Configurações), none (sem endereço),
// down (não respondeu) ou up.
func (a *App) serverState() string {
	switch _, err := a.serverAddr(); {
	case !a.settings().UseServer:
		return "off"
	case err != nil:
		return "none"
	case a.serverUp.Load():
		return "up"
	}
	return "down"
}

func (a *App) dirCall(ctx context.Context, req dirRequest) (dirReply, error) {
	var r dirReply
	ai, err := a.serverInfo()
	if err != nil {
		return r, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if a.h.Network().Connectedness(ai.ID) != network.Connected {
		if err = a.h.Connect(ctx, ai); err != nil {
			a.serverDown()
			return r, err
		}
	}
	var s network.Stream
	if s, err = a.h.NewStream(ctx, ai.ID, dirProto); err != nil {
		a.serverDown()
		return r, err
	}
	defer s.Close()
	s.SetDeadline(time.Now().Add(5 * time.Second))
	if err = json.NewEncoder(s).Encode(req); err != nil {
		return r, err
	}
	if err = json.NewDecoder(bufio.NewReader(s)).Decode(&r); err != nil {
		return r, err
	}
	a.serverUp.Store(true)
	a.serverDownAt.Store(0) // respondeu: está no ar, mesmo que a resposta seja "não achei"
	if !r.OK {
		err = errors.New(r.Err)
	}
	return r, err
}

// registerLoop avisa o servidor dos nossos endereços: ao iniciar, a cada minuto e quando eles mudam.
func (a *App) registerLoop(sub event.Subscription) {
	defer sub.Close()
	next := time.After(0)
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-sub.Out():
		case <-next:
		}
		wait := time.Minute
		if err := a.register(); err != nil {
			wait = 15 * time.Second
		}
		next = time.After(wait)
	}
}

// reserve pede ao servidor uma vaga de relay agora. O AutoNAT só libera o relay automático cerca de 1 min depois
// de abrir o app, e até lá quem procura este computador pelo servidor não conseguiria chegar nele.
func (a *App) reserve() {
	ai, err := a.serverInfo()
	if err != nil {
		return
	}
	if last := a.reservedAt.Load(); last != 0 && time.Since(time.Unix(0, last)) < 20*time.Minute {
		return // a reserva dura 1 h
	}
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	if a.h.Network().Connectedness(ai.ID) != network.Connected && a.h.Connect(ctx, ai) != nil {
		return
	}
	if _, err := client.Reserve(ctx, a.h, ai); err == nil {
		a.reservedAt.Store(time.Now().UnixNano())
	}
}

func (a *App) register() error {
	a.reserve() // antes de anunciar, para o caminho pelo relay já existir quando alguém procurar
	var addrs []string
	for _, m := range a.h.Addrs() {
		// só endereços públicos: os de rede local já são achados por mDNS e não valem fora dela
		if !strings.Contains(m.String(), "/p2p-circuit") && manet.IsPublicAddr(m) {
			addrs = append(addrs, m.String())
		}
	}
	_, err := a.dirCall(a.ctx, dirRequest{Op: "register", Addrs: addrs})
	return err
}

// serverDown marca o servidor como fora do ar.
func (a *App) serverDown() {
	a.serverUp.Store(false)
	a.serverDownAt.Store(time.Now().UnixNano())
	a.reservedAt.Store(0) // ao voltar, reserva de novo
}

// viaServer pergunta ao servidor onde está o computador id e conecta nele.
func (a *App) viaServer(ctx context.Context, id string) (peer.ID, bool) {
	if d := a.serverDownAt.Load(); d != 0 && time.Since(time.Unix(0, d)) < 30*time.Second {
		return "", false // acabou de falhar: não gasta 5 s de novo; o registro periódico avisa quando voltar
	}
	r, err := a.dirCall(ctx, dirRequest{Op: "lookup", ID: id})
	if err != nil || r.Rec == nil {
		return "", false
	}
	pid, err := peer.Decode(r.Rec.Peer)
	if err != nil || numID(pid) != id { // o servidor não é confiável: o ID tem que bater com a chave
		return "", false
	}
	ai := peer.AddrInfo{ID: pid}
	for _, s := range r.Rec.Addrs {
		if m, err := ma.NewMultiaddr(s); err == nil {
			ai.Addrs = append(ai.Addrs, m)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return pid, a.h.Connect(cctx, ai) == nil
}

// SessionMode diz por onde passa a sessão com o computador id (server, direct, lan ou relay, que é um
// relay público) e o ping em ms (0 se não respondeu).
func (a *App) SessionMode(id string) map[string]any {
	a.mu.Lock()
	v := a.out[id]
	a.mu.Unlock()
	if v == nil {
		return map[string]any{}
	}
	c := v.s.Conn()
	addr, mode := c.RemoteMultiaddr(), "direct"
	switch s := addr.String(); {
	case strings.Contains(s, "/p2p-circuit"):
		mode = "relay"
		if ai, err := a.serverAddr(); err == nil && strings.Contains(s, "/p2p/"+ai.ID.String()+"/") {
			mode = "server"
		}
	case manet.IsPrivateAddr(addr) || manet.IsIPLoopback(addr):
		mode = "lan"
	}
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()
	ms := 0
	for r := range ping.Ping(ctx, a.h, c.RemotePeer()) {
		if r.Error == nil {
			ms = max(1, int((r.RTT+time.Millisecond-1)/time.Millisecond))
		}
		break
	}
	return map[string]any{"mode": mode, "ms": ms}
}
