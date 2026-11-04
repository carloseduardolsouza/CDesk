package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/draw"
	"image/jpeg"
	"sync"
)

// Envio só do que mudou. O host divide a tela em blocos de tileSize, compara com o quadro anterior e
// manda em JPEG apenas os retângulos alterados (msgTiles). O viewer aplica sobre o último quadro inteiro
// e entrega ao frontend o mesmo JPEG de tela cheia de sempre, então nada acima da rede muda.
// Quadro inteiro (msgFrame) continua indo na primeira vez, ao trocar de monitor ou de resolução e quando
// mais da metade da tela mudou (vídeo, rolagem), onde o recorte não compensa.
// shortcut: o viewer remonta a tela e recodifica em JPEG (qualidade 92), o que soma uma segunda compressão;
// trocar por um canvas no frontend se a perda de nitidez incomodar.

const (
	msgTiles = 8  // mensagem host -> viewer: retângulos alterados da tela
	tileSize = 64 // múltiplo de 16 (bloco do JPEG): o recorte codifica igual ao pedaço da tela inteira
	maxRects = 16 // acima disso manda um único retângulo que cobre tudo o que mudou
)

var errTiles = errors.New("mensagem de blocos inválida")

type deltaEncoder struct {
	w, h, mon int
	hashes    []uint32
}

func tileHashes(img *image.RGBA, cols, rows int) []uint32 {
	b := img.Bounds()
	out := make([]uint32, cols*rows)
	for ty := 0; ty < rows; ty++ {
		y0, y1 := ty*tileSize, min((ty+1)*tileSize, b.Dy())
		for tx := 0; tx < cols; tx++ {
			x0, x1 := tx*tileSize, min((tx+1)*tileSize, b.Dx())
			var h uint32
			for y := y0; y < y1; y++ {
				o := img.PixOffset(b.Min.X+x0, b.Min.Y+y)
				h = crc32.Update(h, crc32.IEEETable, img.Pix[o:o+(x1-x0)*4])
			}
			out[ty*cols+tx] = h
		}
	}
	return out
}

// next compara a tela com a anterior. same: nada mudou. rects == nil e !same: mandar o quadro inteiro (msgFrame);
// isso só acontece na primeira vez e ao trocar de monitor ou de resolução.
// Coordenadas relativas ao canto da imagem.
func (d *deltaEncoder) next(img *image.RGBA, mon int) (rects []image.Rectangle, same bool) {
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	cols, rows := (W+tileSize-1)/tileSize, (H+tileSize-1)/tileSize
	hs := tileHashes(img, cols, rows)
	old := d.hashes
	fresh := old == nil || d.w != W || d.h != H || d.mon != mon
	d.w, d.h, d.mon, d.hashes = W, H, mon, hs
	if fresh {
		return nil, false
	}
	changed := make([]bool, len(hs))
	n := 0
	for i := range hs {
		if hs[i] != old[i] {
			changed[i] = true
			n++
		}
	}
	if n == 0 {
		return nil, true
	}
	full := []image.Rectangle{image.Rect(0, 0, W, H)}
	if n*2 > len(hs) {
		return full, false // a tela toda, mas em faixas: o quadro inteiro só vai na primeira vez
	}
	// uma faixa por sequência de blocos alterados na linha; faixas iguais em linhas seguidas viram um retângulo só
	type run struct{ x0, x1, y0, y1 int }
	var rs []run
	for ty := 0; ty < rows; ty++ {
		for tx := 0; tx < cols; {
			if !changed[ty*cols+tx] {
				tx++
				continue
			}
			s := tx
			for tx < cols && changed[ty*cols+tx] {
				tx++
			}
			merged := false
			for i := range rs {
				if rs[i].x0 == s && rs[i].x1 == tx && rs[i].y1 == ty {
					rs[i].y1++
					merged = true
					break
				}
			}
			if !merged {
				rs = append(rs, run{s, tx, ty, ty + 1})
			}
		}
	}
	for _, r := range rs {
		rects = append(rects, image.Rect(r.x0*tileSize, r.y0*tileSize, min(r.x1*tileSize, W), min(r.y1*tileSize, H)))
	}
	if len(rects) > maxRects {
		bb := rects[0]
		for _, r := range rects[1:] {
			bb = bb.Union(r)
		}
		if bb.Dx()*bb.Dy()*10 > W*H*6 {
			return full, false
		}
		rects = []image.Rectangle{bb}
	}
	return rects, false
}

// reset obriga o próximo quadro a ir inteiro (por exemplo quando um quadro não pôde ser enviado).
func (d *deltaEncoder) reset() { d.hashes = nil }

// splitStrips parte retângulos grandes em faixas horizontais (múltiplos de tileSize) para codificar em paralelo:
// o JPEG do Go usa um núcleo só, e a tela toda levava ~25 ms.
func splitStrips(rects []image.Rectangle) []image.Rectangle {
	var out []image.Rectangle
	for _, r := range rects {
		n := min(4, r.Dy()/(tileSize*2))
		if n < 2 || r.Dx()*r.Dy() < 200_000 {
			out = append(out, r)
			continue
		}
		step := ((r.Dy()+n-1)/n + tileSize - 1) / tileSize * tileSize
		for y := r.Min.Y; y < r.Max.Y; y += step {
			out = append(out, image.Rect(r.Min.X, y, r.Max.X, min(y+step, r.Max.Y)))
		}
	}
	return out
}

// writeTiles monta a mensagem msgTiles completa (com cabeçalho) em buf. Os retângulos são codificados em paralelo.
func writeTiles(buf *bytes.Buffer, img *image.RGBA, rects []image.Rectangle, quality int) error {
	rects = splitStrips(rects)
	min0 := img.Bounds().Min
	parts := make([]bytes.Buffer, len(rects))
	errs := make([]error, len(rects))
	var wg sync.WaitGroup
	for i, r := range rects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = jpeg.Encode(&parts[i], img.SubImage(r.Add(min0)), &jpeg.Options{Quality: quality})
		}()
	}
	wg.Wait()
	buf.Reset()
	buf.Write([]byte{msgTiles, 0, 0, 0, 0, byte(len(rects) >> 8), byte(len(rects))})
	for i, r := range rects {
		if errs[i] != nil {
			return errs[i]
		}
		var h [12]byte
		binary.BigEndian.PutUint16(h[0:], uint16(r.Min.X))
		binary.BigEndian.PutUint16(h[2:], uint16(r.Min.Y))
		binary.BigEndian.PutUint16(h[4:], uint16(r.Dx()))
		binary.BigEndian.PutUint16(h[6:], uint16(r.Dy()))
		binary.BigEndian.PutUint32(h[8:], uint32(parts[i].Len()))
		buf.Write(h[:])
		buf.Write(parts[i].Bytes())
	}
	b := buf.Bytes()
	putU32(b[1:], uint32(len(b)-headerBytes))
	return nil
}

// applyTiles aplica o corpo de uma msgTiles sobre a tela remontada. Só decodifica e desenha: o JPEG de tela cheia
// para o frontend sai em latest(), quando ele pede, e não a cada mensagem (isso atrasava a confirmação do quadro).
// Só a goroutine de leitura da sessão chama isto. Os limites vêm da rede, então tudo é conferido.
func (v *viewSession) applyTiles(body []byte) error {
	v.cmu.Lock()
	defer v.cmu.Unlock()
	if v.canvas == nil {
		if v.key == nil {
			return errTiles
		}
		im, err := jpeg.Decode(bytes.NewReader(v.key))
		if err != nil {
			return err
		}
		c := image.NewRGBA(im.Bounds())
		draw.Draw(c, c.Bounds(), im, im.Bounds().Min, draw.Src)
		v.canvas = c
	}
	if len(body) < 2 {
		return errTiles
	}
	n, p := int(binary.BigEndian.Uint16(body)), body[2:]
	W, H := v.canvas.Bounds().Dx(), v.canvas.Bounds().Dy()
	type piece struct {
		r    image.Rectangle
		data []byte
		img  image.Image
		err  error
	}
	pieces := make([]piece, n)
	for i := range pieces {
		if len(p) < 12 {
			return errTiles
		}
		x, y := int(binary.BigEndian.Uint16(p)), int(binary.BigEndian.Uint16(p[2:]))
		w, h := int(binary.BigEndian.Uint16(p[4:])), int(binary.BigEndian.Uint16(p[6:]))
		l := int(binary.BigEndian.Uint32(p[8:]))
		p = p[12:]
		if w == 0 || h == 0 || x+w > W || y+h > H || l > len(p) {
			return errTiles
		}
		pieces[i] = piece{r: image.Rect(x, y, x+w, y+h), data: p[:l]}
		p = p[l:]
	}
	var wg sync.WaitGroup
	for i := range pieces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pieces[i].img, pieces[i].err = jpeg.Decode(bytes.NewReader(pieces[i].data))
		}()
	}
	wg.Wait()
	for _, pc := range pieces {
		if pc.err != nil {
			return pc.err
		}
		if pc.img.Bounds().Dx() != pc.r.Dx() || pc.img.Bounds().Dy() != pc.r.Dy() {
			return errTiles
		}
		draw.Draw(v.canvas, pc.r, pc.img, pc.img.Bounds().Min, draw.Src)
	}
	v.dirty = true
	return nil
}

// setKey guarda um quadro inteiro recebido: vira a nova base e descarta a tela remontada.
func (v *viewSession) setKey(jpg []byte) {
	v.cmu.Lock()
	v.key, v.canvas, v.dirty = jpg, nil, false
	v.cmu.Unlock()
	v.frame.Store(jpg)
}

// latest devolve o JPEG mais recente da tela. Se chegaram blocos desde a última vez, recodifica a tela remontada
// (qualidade 92); o frontend busca no máximo uma vez por quadro mostrado, então é no máximo uma codificação por quadro
// exibido, e não uma por mensagem recebida.
func (v *viewSession) latest() []byte {
	v.emu.Lock()
	defer v.emu.Unlock()
	v.cmu.Lock()
	if !v.dirty || v.canvas == nil {
		v.cmu.Unlock()
		b, _ := v.frame.Load().([]byte)
		return b
	}
	if v.snap == nil || v.snap.Rect != v.canvas.Rect {
		v.snap = image.NewRGBA(v.canvas.Rect)
	}
	copy(v.snap.Pix, v.canvas.Pix) // a codificação roda fora do cadeado para não segurar a leitura da rede
	v.dirty = false
	v.cmu.Unlock()
	var out bytes.Buffer
	if jpeg.Encode(&out, v.snap, &jpeg.Options{Quality: 92}) != nil {
		b, _ := v.frame.Load().([]byte)
		return b
	}
	v.frame.Store(out.Bytes())
	return out.Bytes()
}
