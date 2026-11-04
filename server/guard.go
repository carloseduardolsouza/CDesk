package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/proto"
	ma "github.com/multiformats/go-multiaddr"
)

// numID é a mesma conta do app (app.go): 9 dígitos derivados do PeerID. Mantenha as duas iguais.
func numID(id peer.ID) string {
	h := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%09d", binary.BigEndian.Uint64(h[:8])%1_000_000_000)
}

type user struct {
	day, month       int64 // bytes já usados
	dayKey, monthKey string
	pend             int64 // ainda não gravado no SQLite
	rate             float64
	hot              int // segundos seguidos acima do limite de banda
}

// Guard aplica os limites do painel: ACL do relay, gater de conexão e contagem de uso.
type Guard struct {
	h    host.Host
	st   *Store
	bw   *metrics.BandwidthCounter
	now  func() time.Time
	mu   sync.Mutex
	last map[peer.ID]int64
	usr  map[string]*user
	sm   sync.Mutex
	cur  *snapshot
}

// snapshot guarda limites e bloqueios em memória. O handshake de toda conexão (inclusive de curiosos da internet)
// consulta isso, e o SQLite tem uma conexão só, então ir ao banco a cada consulta travava o servidor.
type snapshot struct {
	limits    map[string]Limits
	acl       map[string]string
	allowOnly bool
	at        time.Time
}

const snapshotTTL = 2 * time.Second

func (g *Guard) snap() *snapshot {
	g.sm.Lock()
	defer g.sm.Unlock()
	if g.cur != nil && g.now().Sub(g.cur.at) < snapshotTTL {
		return g.cur
	}
	s := &snapshot{limits: map[string]Limits{}, acl: map[string]string{}, at: g.now()}
	if m, err := g.st.Overrides(); err == nil {
		s.limits = m
	} else if g.cur != nil {
		return g.cur
	}
	if es, err := g.st.Entries(); err == nil {
		for _, e := range es {
			s.acl[e.ID] = e.Kind
		}
	}
	s.allowOnly = g.st.Get("allowonly") == "1"
	g.cur = s
	return s
}

// Reload faz a próxima consulta reler o banco (o painel chama depois de salvar).
func (g *Guard) Reload() {
	g.sm.Lock()
	g.cur = nil
	g.sm.Unlock()
}

// limit é o limite do ID: o do próprio ID ou, sem ele, o padrão.
func (g *Guard) limit(id string) Limits {
	s := g.snap()
	if l, ok := s.limits[id]; ok {
		return l
	}
	return s.limits[defaultID]
}

func newGuard(st *Store, bw *metrics.BandwidthCounter) *Guard {
	return &Guard{st: st, bw: bw, now: time.Now, last: map[peer.ID]int64{}, usr: map[string]*user{}}
}

func (g *Guard) keys() (day, month string) {
	t := g.now()
	return t.Format("2006-01-02"), t.Format("2006-01") + "-01"
}

// with devolve o contador do ID, carregando do SQLite na primeira vez e ao virar o dia/mês.
// Precisa ser chamado com g.mu travado.
func (g *Guard) with(id string) *user {
	day, month := g.keys()
	u := g.usr[id]
	if u == nil {
		u = &user{dayKey: day, monthKey: month}
		u.day, u.month = g.st.UsageSince(id, day), g.st.UsageSince(id, month)
		g.usr[id] = u
		return u
	}
	if u.dayKey != day || u.monthKey != month {
		g.flushOne(id, u)
		if u.dayKey != day {
			u.dayKey, u.day = day, 0
		}
		if u.monthKey != month {
			u.monthKey, u.month = month, 0
		}
	}
	return u
}

func (g *Guard) flushOne(id string, u *user) {
	if u.pend > 0 && g.st.AddUsage(id, u.dayKey, u.pend) == nil {
		u.pend = 0
	}
}

func (g *Guard) flush() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, u := range g.usr {
		g.flushOne(id, u)
	}
}

func (g *Guard) overQuota(id string) bool {
	l := g.limit(id)
	g.mu.Lock()
	u := g.with(id)
	day, month := u.day, u.month
	g.mu.Unlock()
	return (l.DayMB > 0 && day >= l.DayMB<<20) || (l.MonthMB > 0 && month >= l.MonthMB<<20)
}

// blocked: banido, fora da lista de permitidos (se o modo estiver ligado) ou sem cota.
func (g *Guard) blocked(id string) bool {
	s := g.snap()
	k := s.acl[id]
	if k == "block" || (s.allowOnly && k != "allow") {
		return true
	}
	return g.overQuota(id)
}

// Kick derruba todas as conexões do ID.
func (g *Guard) Kick(id string) {
	for _, p := range g.h.Network().Peers() {
		if numID(p) == id {
			g.h.Network().ClosePeer(p)
		}
	}
}

// Run mede o uso a cada segundo e grava no SQLite a cada 30 s.
func (g *Guard) Run(stop <-chan struct{}) {
	t, f := time.NewTicker(time.Second), time.NewTicker(30*time.Second)
	defer t.Stop()
	defer f.Stop()
	for {
		select {
		case <-t.C:
			g.tick()
		case <-f.C:
			g.flush()
		case <-stop:
			g.flush()
			return
		}
	}
}

func (g *Guard) tick() {
	seen := map[peer.ID]bool{}
	for p, s := range g.bw.GetBandwidthByPeer() {
		seen[p] = true
		tot := s.TotalIn + s.TotalOut
		d := tot - g.last[p]
		g.last[p] = tot
		id := numID(p)
		if d == 0 { // parado: não custa consulta nenhuma (a lista traz todo peer que já passou por aqui)
			g.mu.Lock()
			if u := g.usr[id]; u != nil {
				u.rate, u.hot = 0, 0
			}
			g.mu.Unlock()
			continue
		}
		lim := g.limit(id)
		g.mu.Lock()
		u := g.with(id)
		u.day, u.month, u.pend, u.rate = u.day+d, u.month+d, u.pend+d, float64(d)
		if lim.MBps > 0 && u.rate > lim.MBps*1e6 {
			u.hot++
		} else {
			u.hot = 0
		}
		hot := u.hot >= 3
		g.mu.Unlock()
		// shortcut: sem shaping de banda; quem passa do limite por 3 s seguidos é desconectado. Trocar por limitador no stream se virar abuso.
		if hot || g.blocked(id) {
			g.h.Network().ClosePeer(p)
		}
	}
	for p := range g.last {
		if !seen[p] {
			delete(g.last, p)
		}
	}
	g.bw.TrimIdle(g.now().Add(-10 * time.Minute))
}

func (g *Guard) circuits(p peer.ID) int {
	n := 0
	for _, c := range g.h.Network().ConnsToPeer(p) {
		for _, s := range c.GetStreams() {
			if s.Protocol() == proto.ProtoIDv2Hop {
				n++
			}
		}
	}
	return n
}

func (g *Guard) isCDesk(p peer.ID) bool {
	v, err := g.h.Peerstore().Get(p, "AgentVersion")
	s, ok := v.(string)
	// identify pode não ter terminado: só recusa quando o agente já é conhecido e não é o CDesk
	return err != nil || !ok || strings.HasPrefix(s, "cdesk/")
}

// AllowReserve e AllowConnect implementam relay.ACLFilter.
func (g *Guard) AllowReserve(p peer.ID, _ ma.Multiaddr) bool {
	return g.isCDesk(p) && !g.blocked(numID(p))
}

func (g *Guard) AllowConnect(src peer.ID, _ ma.Multiaddr, dest peer.ID) bool {
	id := numID(src)
	if !g.isCDesk(src) || g.blocked(id) || g.blocked(numID(dest)) {
		return false
	}
	max := g.limit(id).Circuits
	return max == 0 || g.circuits(src) <= max // o stream do próprio pedido já conta
}

// ConnectionGater: banido ou sem cota nem chega a conectar.
func (g *Guard) InterceptPeerDial(peer.ID) bool               { return true }
func (g *Guard) InterceptAddrDial(peer.ID, ma.Multiaddr) bool { return true }
func (g *Guard) InterceptAccept(network.ConnMultiaddrs) bool  { return true }
func (g *Guard) InterceptSecured(_ network.Direction, p peer.ID, _ network.ConnMultiaddrs) bool {
	return !g.blocked(numID(p))
}
func (g *Guard) InterceptUpgraded(network.Conn) (bool, control.DisconnectReason) { return true, 0 }

type Live struct {
	ID       string  `json:"id"`
	Addr     string  `json:"addr"`
	MBps     float64 `json:"mbps"`
	Circuits int     `json:"circuits"`
	DayMB    float64 `json:"dayMB"`
	MonthMB  float64 `json:"monthMB"`
	Limits   Limits  `json:"limits"`
}

func (g *Guard) Live() []Live {
	out := []Live{}
	for _, p := range g.h.Network().Peers() {
		id := numID(p)
		l := Live{ID: id, Circuits: g.circuits(p), Limits: g.limit(id)}
		if cs := g.h.Network().ConnsToPeer(p); len(cs) > 0 {
			l.Addr = cs[0].RemoteMultiaddr().String()
		}
		g.mu.Lock()
		u := g.with(id)
		l.MBps, l.DayMB, l.MonthMB = u.rate/1e6, float64(u.day)/(1<<20), float64(u.month)/(1<<20)
		g.mu.Unlock()
		out = append(out, l)
	}
	return out
}

// Watch derruba quem não é o app: a 4001 é a porta padrão do IPFS, então nós da rede IPFS e varreduras
// conectam o tempo todo. Conexão que se identifica como outro agente cai na hora; a que não se identifica
// em 15 s também. Sem isso elas lotam os limites de conexão e as de verdade são recusadas.
func (g *Guard) Watch(stop <-chan struct{}) {
	sub, err := g.h.EventBus().Subscribe([]any{new(event.EvtPeerIdentificationCompleted), new(event.EvtPeerConnectednessChanged)})
	if err != nil {
		return
	}
	defer sub.Close()
	agent := func(p peer.ID) (string, bool) {
		v, err := g.h.Peerstore().Get(p, "AgentVersion")
		s, ok := v.(string)
		return s, err == nil && ok
	}
	for {
		select {
		case <-stop:
			return
		case e := <-sub.Out():
			switch e := e.(type) {
			case event.EvtPeerIdentificationCompleted:
				if !strings.HasPrefix(e.AgentVersion, "cdesk/") {
					g.h.Network().ClosePeer(e.Peer)
				}
			case event.EvtPeerConnectednessChanged:
				if e.Connectedness == network.Connected {
					p := e.Peer
					time.AfterFunc(15*time.Second, func() {
						if s, ok := agent(p); (!ok || !strings.HasPrefix(s, "cdesk/")) && g.h.Network().Connectedness(p) == network.Connected {
							g.h.Network().ClosePeer(p)
						}
					})
				}
			}
		}
	}
}
