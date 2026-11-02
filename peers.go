package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	ma "github.com/multiformats/go-multiaddr"
)

// Achar um computador pela rede P2P (DHT) leva de 20 s a mais de 1 minuto. Depois da primeira
// conexão o CDesk lembra os endereços dele (peers.json) e, na próxima vez, conecta direto, sem
// procurar na rede. Se o endereço mudou (IP novo), cai na busca normal.

const maxKnownPeers = 50

type knownPeer struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

func (a *App) peersFile() string { return filepath.Join(a.dir, "peers.json") }

func (a *App) readPeers() map[string]knownPeer {
	m := map[string]knownPeer{}
	if b, err := os.ReadFile(a.peersFile()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

// loadPeers devolve ao peerstore os endereços dos computadores a que já nos conectamos.
func (a *App) loadPeers() {
	for _, p := range a.readPeers() {
		pid, err := peer.Decode(p.ID)
		if err != nil {
			continue
		}
		var addrs []ma.Multiaddr
		for _, s := range p.Addrs {
			if m, err := ma.NewMultiaddr(s); err == nil {
				addrs = append(addrs, m)
			}
		}
		a.h.Peerstore().AddAddrs(pid, addrs, peerstore.PermanentAddrTTL)
	}
}

// rememberPeer guarda os endereços diretos de pid. Endereços de relay não valem a pena: o relay
// público corta a sessão.
func (a *App) rememberPeer(pid peer.ID) {
	seen := map[string]bool{}
	var addrs []string
	add := func(s string) {
		if !strings.Contains(s, "/p2p-circuit") && !seen[s] && len(addrs) < 8 {
			seen[s] = true
			addrs = append(addrs, s)
		}
	}
	for _, c := range a.h.Network().ConnsToPeer(pid) {
		if !c.Stat().Limited {
			add(c.RemoteMultiaddr().String()) // o endereço pelo qual acabamos de conectar
		}
	}
	for _, m := range a.h.Peerstore().Addrs(pid) {
		add(m.String())
	}
	if len(addrs) == 0 {
		return
	}
	a.peersMu.Lock()
	defer a.peersMu.Unlock()
	m := a.readPeers()
	for len(m) >= maxKnownPeers { // lista cheia: descarta um qualquer para abrir vaga
		for k := range m {
			delete(m, k)
			break
		}
	}
	m[numID(pid)] = knownPeer{ID: pid.String(), Addrs: addrs}
	b, _ := json.Marshal(m)
	os.WriteFile(a.peersFile(), b, 0o600)
}
