package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
	"golang.org/x/crypto/bcrypt"
)

func testStore(t *testing.T) *Store {
	st, err := openStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.db.Close() })
	return st
}

func TestLimitsAndBlocking(t *testing.T) {
	st := testStore(t)
	g := newGuard(st, metrics.NewBandwidthCounter())
	const a, b = "111111111", "222222222"

	st.SetLimit(defaultID, Limits{DayMB: 10})
	st.SetLimit(a, Limits{DayMB: 1})
	g.Reload()
	if st.EffectiveLimit(a).DayMB != 1 || st.EffectiveLimit(b).DayMB != 10 {
		t.Fatal("o override do ID deve vencer o padrão, e os demais usam o padrão")
	}

	if g.blocked(a) {
		t.Fatal("ID sem restrição foi bloqueado")
	}
	st.AddEntry(Entry{ID: a, Kind: "block"})
	g.Reload()
	if !g.blocked(a) {
		t.Fatal("ID banido não foi bloqueado")
	}
	st.DelEntry(a)

	st.Set("allowonly", "1")
	g.Reload()
	if !g.blocked(a) {
		t.Fatal("modo 'só permitidos' deve bloquear quem não está na lista")
	}
	st.AddEntry(Entry{ID: a, Kind: "allow"})
	g.Reload()
	if g.blocked(a) {
		t.Fatal("ID permitido foi bloqueado")
	}
	st.Set("allowonly", "0")
}

func TestQuotaPersists(t *testing.T) {
	st := testStore(t)
	g := newGuard(st, metrics.NewBandwidthCounter())
	const id = "333333333"
	st.SetLimit(defaultID, Limits{DayMB: 1, MonthMB: 100})
	g.Reload()

	g.mu.Lock()
	u := g.with(id)
	u.day, u.month, u.pend = 2<<20, 2<<20, 2<<20
	g.mu.Unlock()
	if !g.overQuota(id) {
		t.Fatal("passou da franquia diária e não foi bloqueado")
	}

	g.flush()
	g2 := newGuard(st, metrics.NewBandwidthCounter()) // simula reiniciar o servidor
	if !g2.overQuota(id) {
		t.Fatal("o uso gravado deve sobreviver ao reinício")
	}

	g2.now = func() time.Time { return time.Now().AddDate(0, 0, 1) } // virou o dia
	g2.mu.Lock()
	g2.usr = map[string]*user{}
	g2.mu.Unlock()
	if g2.overQuota(id) {
		t.Fatal("no dia seguinte a franquia diária deve zerar")
	}
}

func newHost(t *testing.T) host.Host {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.UserAgent("cdesk/test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func dirCall(t *testing.T, c, srv host.Host, req dirReq) dirResp {
	t.Helper()
	ctx := context.Background()
	if err := c.Connect(ctx, peer.AddrInfo{ID: srv.ID(), Addrs: srv.Addrs()}); err != nil {
		t.Fatal(err)
	}
	s, err := c.NewStream(ctx, srv.ID(), dirProto)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	json.NewEncoder(s).Encode(req)
	var r dirResp
	if err := json.NewDecoder(s).Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDirectory(t *testing.T) {
	srv, cli := newHost(t), newHost(t)
	d := newDir(srv, []string{"/dns4/relay.exemplo.com/tcp/4001"})
	id := numID(cli.ID())

	if r := dirCall(t, cli, srv, dirReq{Op: "lookup", ID: id}); r.Err != "notfound" {
		t.Fatalf("antes de registrar deveria ser notfound, veio %+v", r)
	}
	// o ID vem do PeerID autenticado: o campo ID enviado no register é ignorado
	if r := dirCall(t, cli, srv, dirReq{Op: "register", ID: "999999999", Addrs: []string{"/ip4/1.2.3.4/tcp/9", "/ip4/9.9.9.9/tcp/1/p2p-circuit"}}); !r.OK {
		t.Fatalf("register falhou: %+v", r)
	}
	r := dirCall(t, cli, srv, dirReq{Op: "lookup", ID: id})
	if !r.OK || r.Rec.Peer != cli.ID().String() {
		t.Fatalf("lookup errado: %+v", r)
	}
	if len(r.Rec.Addrs) != 2 || r.Rec.Addrs[0] != "/ip4/1.2.3.4/tcp/9" ||
		!strings.HasSuffix(r.Rec.Addrs[1], "/p2p/"+srv.ID().String()+"/p2p-circuit") {
		t.Fatalf("endereços inesperados (circuit do cliente deve cair, o do servidor entra): %v", r.Rec.Addrs)
	}
	if r := dirCall(t, cli, srv, dirReq{Op: "lookup", ID: "999999999"}); r.Err != "notfound" {
		t.Fatal("não pode registrar em nome de outro ID")
	}
	if r := dirCall(t, cli, srv, dirReq{Op: "many", IDs: []string{id, "000000000"}}); len(r.Found) != 1 || r.Found[id] == nil {
		t.Fatalf("many errado: %+v", r)
	}

	d.now = func() time.Time { return time.Now().Add(dirTTL + time.Second) }
	if d.Online() != 0 || d.lookup(id) != nil {
		t.Fatal("registro deve expirar após o TTL")
	}
}

func TestDirectoryRateLimit(t *testing.T) {
	srv := newHost(t)
	d := newDir(srv, nil)
	p := peer.ID("x")
	for i := 0; i < ratePerMn; i++ {
		if d.limited(p) {
			t.Fatalf("limitou cedo demais na chamada %d", i)
		}
	}
	if !d.limited(p) {
		t.Fatal("deveria limitar após o teto de chamadas por minuto")
	}
}

func TestAdminPanel(t *testing.T) {
	st := testStore(t)
	g := newGuard(st, metrics.NewBandwidthCounter())
	pw, err := initAdmin(st)
	if err != nil || pw != "admin123" {
		t.Fatalf("senha inicial deveria ser admin123, veio %q (%v)", pw, err)
	}
	if st.Get("must_change") != "0" {
		t.Fatal("a senha admin123 não deve exigir troca")
	}
	st.Set("must_change", "1") // o resto do teste exercita a troca obrigatória, que continua existindo
	if again, _ := initAdmin(st); again != "" {
		t.Fatal("não pode gerar nova senha se o admin já existe")
	}
	ts := httptest.NewServer(newAdmin(st, g, nil, "srv").Handler())
	defer ts.Close()

	call := func(method, path string, in any, cookies []*http.Cookie, csrf string) (*http.Response, map[string]any) {
		b, _ := json.Marshal(in)
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
		for _, c := range cookies {
			req.AddCookie(c)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF", csrf)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp, m
	}

	if r, _ := call("GET", "/api/limits", nil, nil, ""); r.StatusCode != 401 {
		t.Fatal("API sem login deve dar 401")
	}
	if r, _ := call("POST", "/api/login", map[string]string{"user": "admin", "pass": "errada"}, nil, ""); r.StatusCode != 401 {
		t.Fatal("senha errada deve dar 401")
	}
	r, m := call("POST", "/api/login", map[string]string{"user": "admin", "pass": pw}, nil, "")
	if r.StatusCode != 200 || m["mustChange"] != true {
		t.Fatalf("login inicial: %d %v", r.StatusCode, m)
	}
	ck, csrf := r.Cookies(), m["csrf"].(string)

	if r, m := call("GET", "/api/limits", nil, ck, ""); r.StatusCode != 403 || m["error"] != "change-password" {
		t.Fatal("antes de trocar a senha só /api/password deve funcionar")
	}
	if r, _ := call("POST", "/api/password", map[string]string{"old": pw, "new": "curta"}, ck, csrf); r.StatusCode != 400 {
		t.Fatal("senha curta deve ser recusada")
	}
	if r, _ := call("POST", "/api/password", map[string]string{"old": pw, "new": "uma-senha-bem-longa"}, ck, ""); r.StatusCode != 403 {
		t.Fatal("POST sem token CSRF deve ser recusado")
	}
	if r, _ := call("POST", "/api/password", map[string]string{"old": pw, "new": "uma-senha-bem-longa"}, ck, csrf); r.StatusCode != 200 {
		t.Fatal("troca de senha falhou")
	}

	set := map[string]any{"id": "444444444", "limits": Limits{MBps: 1, Circuits: 1, DayMB: 5}}
	if r, _ := call("PUT", "/api/limits", set, ck, csrf); r.StatusCode != 200 {
		t.Fatal("salvar limite falhou")
	}
	if got := st.EffectiveLimit("444444444"); got.MBps != 1 || got.Circuits != 1 || got.DayMB != 5 {
		t.Fatalf("limite não foi gravado: %+v", got)
	}
	set["id"] = "12"
	if r, _ := call("PUT", "/api/limits", set, ck, csrf); r.StatusCode != 400 {
		t.Fatal("ID inválido deve ser recusado")
	}
}

func TestRelayEndToEnd(t *testing.T) {
	st := testStore(t)
	bw := metrics.NewBandwidthCounter()
	g := newGuard(st, bw)
	rc := relay.DefaultResources()
	rc.Limit = nil
	srv, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.BandwidthReporter(bw), libp2p.ConnectionGater(g),
		libp2p.ForceReachabilityPublic(), libp2p.EnableRelayService(relay.WithResources(rc), relay.WithACL(g)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	g.h = srv
	si := peer.AddrInfo{ID: srv.ID(), Addrs: srv.Addrs()}

	// A é o host (fica atrás do relay), B é o viewer
	a, b := newHost(t), newHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, h := range []host.Host{a, b} {
		if err := h.Connect(ctx, si); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Reserve(ctx, a, si); err != nil {
		t.Fatalf("reserva no relay: %v", err)
	}
	a.SetStreamHandler("/t/1", func(s network.Stream) { s.Write([]byte("oi")); s.Close() })

	circuit, _ := ma.NewMultiaddr("/p2p/" + srv.ID().String() + "/p2p-circuit")
	target := peer.AddrInfo{ID: a.ID(), Addrs: []ma.Multiaddr{circuit}}
	if err := b.Connect(ctx, target); err != nil {
		t.Fatalf("conexão pelo relay: %v", err)
	}
	for _, c := range b.Network().ConnsToPeer(a.ID()) {
		if c.Stat().Limited {
			t.Fatal("a conexão pelo nosso relay não pode ser Limited (senão o app a recusa)")
		}
	}
	s, err := b.NewStream(ctx, a.ID(), "/t/1")
	if err != nil {
		t.Fatalf("stream pelo relay: %v", err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(s, buf); err != nil || string(buf) != "oi" {
		t.Fatalf("dados pelo relay: %q %v", buf, err)
	}
	s.Close()

	// o painel bloqueia B: derruba a conexão e recusa novos circuitos
	st.AddEntry(Entry{ID: numID(b.ID()), Kind: "block"})
	g.Reload()
	g.Kick(numID(b.ID()))
	b.Network().ClosePeer(a.ID())
	b2 := newHost(t)
	b2.Connect(ctx, si)
	st.AddEntry(Entry{ID: numID(b2.ID()), Kind: "block"})
	g.Reload()
	if g.AllowConnect(b2.ID(), nil, a.ID()) || g.AllowReserve(b2.ID(), nil) {
		t.Fatal("ID bloqueado ainda passa pelo ACL do relay")
	}
}

func TestResetAdmin(t *testing.T) {
	st := testStore(t)
	initAdmin(st)
	h, _ := bcrypt.GenerateFromPassword([]byte("outra-senha-longa"), bcrypt.MinCost)
	st.Set("admin_hash", string(h)) // banco antigo com outra senha
	st.Set("must_change", "1")
	if err := setAdmin(st, defaultAdminPW); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(st.Get("admin_hash")), []byte("admin123")) != nil || st.Get("must_change") != "0" {
		t.Fatal("reset deveria voltar a senha admin123 sem exigir troca")
	}
}

// A 4001 é a porta padrão do IPFS: nós de fora conectam o tempo todo e não podem ocupar o servidor.
func TestWatchDerrubaQuemNaoEhCDesk(t *testing.T) {
	st := testStore(t)
	g := newGuard(st, metrics.NewBandwidthCounter())
	srv, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.UserAgent("cdesk-server/1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	g.h = srv
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go g.Watch(stop)

	ctx := context.Background()
	si := peer.AddrInfo{ID: srv.ID(), Addrs: srv.Addrs()}
	ipfs, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.UserAgent("kubo/0.30")) // como um nó IPFS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ipfs.Close() })
	app := newHost(t) // cdesk/test
	for _, h := range []host.Host{ipfs, app} {
		if err := h.Connect(ctx, si); err != nil {
			t.Fatal(err)
		}
	}
	if !eventuallyT(5*time.Second, func() bool { return srv.Network().Connectedness(ipfs.ID()) != network.Connected }) {
		t.Fatal("o nó que não é CDesk deveria ser desconectado")
	}
	time.Sleep(500 * time.Millisecond)
	if srv.Network().Connectedness(app.ID()) != network.Connected {
		t.Fatal("o app CDesk não pode ser desconectado")
	}
}

func eventuallyT(d time.Duration, cond func() bool) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// Com milhares de peers que só passaram por aqui, o tick não pode custar consultas ao banco.
func TestTickNaoConsultaOBancoParaPeersParados(t *testing.T) {
	st := testStore(t)
	bw := metrics.NewBandwidthCounter()
	g := newGuard(st, bw)
	srv := newHost(t)
	g.h = srv
	for i := 0; i < 3000; i++ {
		p := peer.ID(fmt.Sprintf("peer-%d", i))
		bw.LogSentMessageStream(100, "/x", p)
	}
	g.tick() // primeira passada: todos têm tráfego novo
	start := time.Now()
	for i := 0; i < 20; i++ {
		g.tick() // sem tráfego novo: tem que ser barato
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("20 passadas sem tráfego levaram %v", el)
	}
}
