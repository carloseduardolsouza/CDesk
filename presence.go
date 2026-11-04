package main

import (
	"context"
	"slices"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Quem está online: sem servidor central não há uma lista de presença, então cada computador é
// verificado tentando alcançá-lo: primeiro pelos endereços já conhecidos (rápido, funciona na rede
// local e para quem já conectou antes), depois pela rede P2P (DHT, lenta e menos confiável).
// shortcut: "offline" por essa via pode ser falso para quem só é achado pela DHT lenta; subir o
// orçamento de tempo ou cachear a última vez em que ficou online se isso incomodar.

const (
	probeBudget  = 15 * time.Second // tempo máximo para decidir se um computador está online
	probeKnown   = 4 * time.Second  // parte dele para os endereços já conhecidos
	probeWorkers = 4                // verificações ao mesmo tempo
	lanTTL       = 90 * time.Second // quanto tempo um computador achado por mDNS continua na lista
)

// busyWith diz se há uma sessão (ou conexão em andamento) com o computador id.
func (a *App) busyWith(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, open := a.out[id]
	_, running := a.connecting[id]
	return open || running
}

// probe diz se o computador id responde agora.
func (a *App) probe(id string) bool {
	budget := a.probeBudget
	if budget == 0 {
		budget = probeBudget
	}
	ctx, cancel := context.WithTimeout(a.ctx, budget)
	defer cancel()
	// A conexão aberta só para verificar é fechada: não fica sobrando conexão para cada item da lista.
	done := func(p peer.ID, wasConnected bool) bool {
		if !wasConnected && !a.busyWith(id) {
			a.h.Network().ClosePeer(p)
		}
		return true
	}
	for _, p := range a.h.Peerstore().Peers() {
		if p != a.h.ID() && numID(p) == id && a.h.Network().Connectedness(p) == network.Connected {
			return true
		}
	}
	p, err := a.find(ctx, id) // endereços conhecidos, servidor e DHT ao mesmo tempo
	if err != nil {
		return false
	}
	return done(p, false)
}

// CheckOnline verifica em segundo plano quais dos computadores (IDs de 9 dígitos) estão online. Cada
// resultado chega pelo evento "presence" (id, online). Quem já está sendo verificado é ignorado.
func (a *App) CheckOnline(ids []string) {
	if a.h == nil {
		return
	}
	for _, raw := range ids {
		id := onlyDigits(raw)
		if len(id) != 9 || id == a.myID {
			continue
		}
		a.mu.Lock()
		if a.probing == nil {
			a.probing = map[string]bool{}
		}
		skip := a.probing[id]
		a.probing[id] = true
		a.mu.Unlock()
		if skip {
			continue
		}
		go func() {
			a.probeSem <- struct{}{}
			online := a.probe(id)
			<-a.probeSem
			a.mu.Lock()
			delete(a.probing, id)
			a.mu.Unlock()
			a.emit("presence", id, online)
		}()
	}
}

// noteLAN registra um computador achado na rede local (mDNS).
func (a *App) noteLAN(p peer.ID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lan == nil {
		a.lan = map[peer.ID]time.Time{}
	}
	a.lan[p] = time.Now()
}

// LanPeers lista os IDs dos computadores com CDesk achados na rede local há pouco tempo e que
// respondem (aba "Encontrados na rede").
func (a *App) LanPeers() []string {
	ttl := a.lanTTL
	if ttl == 0 {
		ttl = lanTTL
	}
	a.mu.Lock()
	var peers []peer.ID
	for p, seen := range a.lan {
		if time.Since(seen) <= ttl {
			peers = append(peers, p)
		}
	}
	a.mu.Unlock()
	ids := []string{}
	for _, p := range peers {
		if a.h.Network().Connectedness(p) == network.Connected {
			ids = append(ids, numID(p))
		}
	}
	slices.Sort(ids)
	return ids
}
