package main

import (
	"bufio"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	dirProto  = protocol.ID("/cdesk-dir/1.0.0")
	dirTTL    = 2 * time.Minute
	maxAddrs  = 16
	maxIDs    = 100
	ratePerMn = 60
)

type dirReq struct {
	Op    string   `json:"op"` // register | lookup | many
	Addrs []string `json:"addrs,omitempty"`
	ID    string   `json:"id,omitempty"`
	IDs   []string `json:"ids,omitempty"`
}

type dirRec struct {
	Peer  string   `json:"peer"`
	Addrs []string `json:"addrs"`
}

type dirResp struct {
	OK    bool               `json:"ok"`
	Err   string             `json:"err,omitempty"`
	Rec   *dirRec            `json:"rec,omitempty"`
	Found map[string]*dirRec `json:"found,omitempty"`
}

type dirEntry struct {
	rec dirRec
	exp time.Time
}

// Dir guarda ID → endereços só em memória: ao reiniciar, os hosts se registram de novo sozinhos.
type Dir struct {
	h        host.Host
	announce []string // endereços públicos do servidor, para montar o /p2p-circuit
	now      func() time.Time
	mu       sync.Mutex
	m        map[string]dirEntry
	hits     map[peer.ID][]time.Time
}

func newDir(h host.Host, announce []string) *Dir {
	d := &Dir{h: h, announce: announce, now: time.Now, m: map[string]dirEntry{}, hits: map[peer.ID][]time.Time{}}
	h.SetStreamHandler(dirProto, d.serve)
	return d
}

func (d *Dir) limited(p peer.ID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.now()
	keep := d.hits[p][:0]
	for _, x := range d.hits[p] {
		if t.Sub(x) < time.Minute {
			keep = append(keep, x)
		}
	}
	d.hits[p] = append(keep, t)
	return len(d.hits[p]) > ratePerMn
}

func (d *Dir) serve(s network.Stream) {
	defer s.Close()
	s.SetDeadline(time.Now().Add(10 * time.Second))
	var req dirReq
	if err := json.NewDecoder(bufio.NewReader(&limitedReader{s, 16 << 10})).Decode(&req); err != nil {
		s.Reset()
		return
	}
	p := s.Conn().RemotePeer()
	json.NewEncoder(s).Encode(d.handle(p, req))
}

func (d *Dir) handle(p peer.ID, req dirReq) dirResp {
	if d.limited(p) {
		return dirResp{Err: "rate"}
	}
	switch req.Op {
	case "register":
		return d.register(p, req.Addrs)
	case "lookup":
		if r := d.lookup(req.ID); r != nil {
			return dirResp{OK: true, Rec: r}
		}
		return dirResp{Err: "notfound"}
	case "many":
		if len(req.IDs) > maxIDs {
			return dirResp{Err: "toomany"}
		}
		found := map[string]*dirRec{}
		for _, id := range req.IDs {
			if r := d.lookup(id); r != nil {
				found[id] = r
			}
		}
		return dirResp{OK: true, Found: found}
	}
	return dirResp{Err: "op"}
}

// register grava os endereços de quem chamou. O ID vem do PeerID autenticado do stream, nunca da mensagem,
// então ninguém registra endereços em nome de outro ID.
func (d *Dir) register(p peer.ID, addrs []string) dirResp {
	if len(addrs) > maxAddrs {
		addrs = addrs[:maxAddrs]
	}
	clean := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if len(a) <= 200 && !strings.Contains(a, "/p2p-circuit") {
			clean = append(clean, a)
		}
	}
	d.mu.Lock()
	d.m[numID(p)] = dirEntry{dirRec{Peer: p.String(), Addrs: clean}, d.now().Add(dirTTL)}
	d.mu.Unlock()
	return dirResp{OK: true}
}

// lookup devolve os endereços diretos e, em seguida, os caminhos pelo relay deste servidor.
func (d *Dir) lookup(id string) *dirRec {
	d.mu.Lock()
	e, ok := d.m[id]
	if ok && d.now().After(e.exp) {
		delete(d.m, id)
		ok = false
	}
	d.mu.Unlock()
	if !ok {
		return nil
	}
	r := dirRec{Peer: e.rec.Peer, Addrs: append([]string{}, e.rec.Addrs...)}
	for _, a := range d.announce {
		r.Addrs = append(r.Addrs, a+"/p2p/"+d.h.ID().String()+"/p2p-circuit")
	}
	return &r
}

// Online diz quantos hosts estão registrados e válidos agora.
func (d *Dir) Online() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, e := range d.m {
		if d.now().Before(e.exp) {
			n++
		}
	}
	return n
}

type limitedReader struct {
	s network.Stream
	n int
}

func (l *limitedReader) Read(b []byte) (int, error) {
	if l.n <= 0 {
		return 0, network.ErrReset
	}
	if len(b) > l.n {
		b = b[:l.n]
	}
	n, err := l.s.Read(b)
	l.n -= n
	return n, err
}
