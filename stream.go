package main

import (
	"bytes"
	"hash/crc32"
	"image/jpeg"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/libp2p/go-libp2p/core/network"
)

const maxInflight = 2 // quadros enviados e ainda não confirmados pelo viewer

// pacer controla o envio de quadros do host. Sem isso o host manda mais do que a rede aguenta e o
// excesso vira fila nos buffers, que aparece como lentidão cada vez maior. Cada quadro só sai
// quando o viewer confirmou um anterior, e o tempo de confirmação ajusta a qualidade da imagem.
type pacer struct {
	tokens  chan struct{}
	mu      sync.Mutex
	sentAt  []time.Time
	quality int // JPEG atual, sempre entre minQ e maxQ
	minQ    int // piso e teto da qualidade (dependem da prioridade escolhida pelo viewer)
	maxQ    int
	every   time.Duration // intervalo mínimo entre quadros (limite de quadros por segundo)
}

// newPacer monta o controle de envio para a prioridade (velocidade|equilibrada|qualidade) e o
// limite de quadros por segundo pedidos pelo viewer. Valores desconhecidos viram o padrão.
func newPacer(priority string, fps int) *pacer {
	p := &pacer{tokens: make(chan struct{}, maxInflight), quality: 70, minQ: 35, maxQ: 90}
	switch priority {
	case "velocidade":
		p.quality, p.minQ, p.maxQ = 50, 30, 70
	case "qualidade":
		p.quality, p.minQ, p.maxQ = 85, 60, 95
	}
	if fps < 5 || fps > 30 {
		fps = 15
	}
	p.every = time.Second / time.Duration(fps)
	for i := 0; i < maxInflight; i++ {
		p.tokens <- struct{}{}
	}
	return p
}

func (p *pacer) release() {
	select {
	case p.tokens <- struct{}{}:
	default:
	}
}

// ack: o viewer recebeu um quadro. Confirmação lenta baixa a qualidade; rápida sobe.
func (p *pacer) ack() {
	p.mu.Lock()
	if len(p.sentAt) > 0 {
		rtt := time.Since(p.sentAt[0])
		p.sentAt = p.sentAt[1:]
		switch {
		case rtt > 350*time.Millisecond:
			p.quality = max(p.minQ, p.quality-10)
		case rtt < 120*time.Millisecond:
			p.quality = min(p.maxQ, p.quality+5)
		}
	}
	p.mu.Unlock()
	p.release()
}

func (p *pacer) sent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sentAt = append(p.sentAt, time.Now())
	return p.quality
}

// sendFrames termina quando a escrita falha (viewer saiu e o stream foi fechado).
func sendFrames(c *hostConn, p *pacer) {
	var buf bytes.Buffer
	var prev uint32
	lastMon := -1
	var last time.Time
	for {
		select {
		case <-p.tokens:
		case <-time.After(2 * time.Second): // sem confirmação por 2 s: segue assim mesmo, não trava
		}
		time.Sleep(p.every - time.Since(last)) // limite de quadros por segundo (negativo = não espera)
		last = time.Now()
		mon := c.currentMonitor()
		img, err := screenshot.CaptureDisplay(mon)
		if err != nil {
			p.release()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if sum := crc32.ChecksumIEEE(img.Pix); sum == prev && mon == lastMon { // tela parada: não manda nada
			p.release()
			time.Sleep(100 * time.Millisecond)
			continue
		} else {
			prev, lastMon = sum, mon
		}
		buf.Reset()
		buf.Write([]byte{msgFrame, 0, 0, 0, 0})
		if jpeg.Encode(&buf, img, &jpeg.Options{Quality: p.sent()}) != nil {
			p.release()
			continue
		}
		b := buf.Bytes()
		putU32(b[1:], uint32(len(b)-headerBytes))
		if err := c.write(b); err != nil {
			c.s.Close()
			return
		}
	}
}

func putU32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}

// viewSession é uma sessão como viewer (uma aba).
type viewSession struct {
	s     network.Stream
	wmu   sync.Mutex   // serializa as escritas (input do usuário e confirmações)
	frame atomic.Value // []byte: último JPEG recebido
}

func (v *viewSession) write(b []byte) {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	v.s.Write(b)
}

// frameHandler entrega o último quadro de uma sessão ao frontend em binário (GET /frame/<id>),
// sem passar por base64 e eventos.
type frameHandler struct{ a *App }

func (f frameHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutPrefix(r.URL.Path, "/frame/")
	f.a.mu.Lock()
	v := f.a.out[id]
	f.a.mu.Unlock()
	if !ok || v == nil {
		http.NotFound(w, r)
		return
	}
	b, _ := v.frame.Load().([]byte)
	if b == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
