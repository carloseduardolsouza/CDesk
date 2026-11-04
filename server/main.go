// cdesk-server: relay libp2p + diretório de IDs + painel web de limites para o CDesk.
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
)

func loadKey(path string) (crypto.PrivKey, error) {
	kb, err := os.ReadFile(path)
	if err != nil {
		k, _, _ := crypto.GenerateEd25519Key(rand.Reader)
		kb, _ = crypto.MarshalPrivateKey(k)
		if err := os.WriteFile(path, kb, 0o600); err != nil {
			return nil, err
		}
	}
	return crypto.UnmarshalPrivateKey(kb)
}

func main() {
	data := flag.String("data", ".", "pasta de dados (server.key e server.db)")
	port := flag.Int("port", 4001, "porta libp2p (tcp e udp/quic)")
	announce := flag.String("announce", "", "endereços públicos, separados por vírgula (ex.: /dns4/relay.exemplo.com/tcp/4001)")
	admin := flag.String("admin", "0.0.0.0:8443", "endereço do painel web")
	cert := flag.String("tls-cert", "", "certificado TLS do painel (opcional)")
	reset := flag.Bool("reset-admin", false, "redefine a senha do painel para admin123 e sai do modo de troca obrigatória")
	key := flag.String("tls-key", "", "chave TLS do painel (opcional)")
	flag.Parse()

	if err := run(*data, *port, *announce, *admin, *cert, *key, *reset); err != nil {
		log.Fatal(err)
	}
}

func run(data string, port int, announce, adminAddr, cert, tlsKey string, resetAdmin bool) error {
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	priv, err := loadKey(filepath.Join(data, "server.key"))
	if err != nil {
		return err
	}
	st, err := openStore(filepath.Join(data, "server.db"))
	if err != nil {
		return err
	}
	bw := metrics.NewBandwidthCounter()
	g := newGuard(st, bw)
	rc := relay.DefaultResources()
	rc.Limit = nil // sem corte por tempo nem por dados; quem limita é o painel
	rc.MaxReservations = 512
	rc.MaxReservationsPerIP = 64 // vários computadores atrás do mesmo roteador (casa, escritório) usam o mesmo IP
	rc.MaxReservationsPerASN = 512
	h, err := libp2p.New(
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port), fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", port),
			fmt.Sprintf("/ip6/::/tcp/%d", port), fmt.Sprintf("/ip6/::/udp/%d/quic-v1", port)),
		libp2p.UserAgent("cdesk-server/1"),
		libp2p.BandwidthReporter(bw),
		libp2p.ConnectionGater(g),
		libp2p.ForceReachabilityPublic(),
		libp2p.EnableRelayService(relay.WithResources(rc), relay.WithACL(g)),
	)
	if err != nil {
		return err
	}
	g.h = h
	var ann []string
	for _, a := range strings.Split(announce, ",") {
		if a = strings.TrimSpace(a); a != "" {
			ann = append(ann, a)
		}
	}
	d := newDir(h, ann)
	stop := make(chan struct{})
	go g.Run(stop)
	go g.Watch(stop)

	if resetAdmin {
		if err := setAdmin(st, defaultAdminPW); err != nil {
			return err
		}
		log.Printf("Senha do painel redefinida: usuário 'admin', senha '%s'", defaultAdminPW)
	}
	pw, err := initAdmin(st)
	if err != nil {
		return err
	}
	if pw != "" {
		log.Printf("Painel: usuário 'admin', senha '%s'. Troque em Conta antes de expor o painel na internet.", pw)
	}
	log.Printf("PeerID do servidor: %s", h.ID())
	for _, a := range ann {
		log.Printf("Endereço para o app (Configurações > Rede > Relay): %s/p2p/%s", a, h.ID())
	}
	srv := &http.Server{Addr: adminAddr, Handler: newAdmin(st, g, d, h.ID().String()).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		var err error
		if cert != "" && tlsKey != "" {
			err = srv.ListenAndServeTLS(cert, tlsKey)
		} else {
			// shortcut: HTTP puro; deixe em 127.0.0.1 (túnel SSH) ou ponha um proxy TLS na frente.
			err = srv.ListenAndServe()
		}
		log.Printf("painel parou: %v", err)
	}()
	log.Printf("Painel em %s", adminAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	close(stop)
	time.Sleep(200 * time.Millisecond) // deixa o Run gravar o uso
	return h.Close()
}
