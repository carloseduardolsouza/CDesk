package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// tela de teste com gradiente e um padrão, parecida o bastante com uma área de trabalho para o JPEG
func fakeScreen(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x / 4), uint8(y / 3), uint8((x/32 + y/32) % 2 * 90), 255})
		}
	}
	return img
}

func paint(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func TestDeltaDetectaMudancas(t *testing.T) {
	var d deltaEncoder
	img := fakeScreen(1000, 600) // não é múltiplo de 64: confere as bordas
	if r, same := d.next(img, 0); r != nil || same {
		t.Fatal("o primeiro quadro tem que ir inteiro")
	}
	if r, same := d.next(img, 0); r != nil || !same {
		t.Fatal("tela igual não manda nada")
	}
	paint(img, image.Rect(70, 70, 90, 90), color.RGBA{255, 0, 0, 255})
	r, same := d.next(img, 0)
	if same || len(r) != 1 || r[0] != image.Rect(64, 64, 128, 128) {
		t.Fatalf("mudança pequena deveria virar um bloco alinhado, veio %v same=%v", r, same)
	}
	paint(img, image.Rect(960, 560, 1000, 600), color.RGBA{0, 255, 0, 255}) // canto: bloco cortado pela borda
	r, _ = d.next(img, 0)
	if len(r) != 1 || r[0] != image.Rect(960, 512, 1000, 600) {
		t.Fatalf("bloco da borda errado: %v", r)
	}
	paint(img, image.Rect(0, 0, 1000, 400), color.RGBA{9, 9, 9, 255}) // mais da metade
	if r, same := d.next(img, 0); same || len(r) != 1 || r[0] != img.Bounds() {
		t.Fatalf("mudança grande deve mandar a tela toda (em faixas), veio %v", r)
	}
	if r, _ := d.next(img, 1); r != nil {
		t.Fatal("trocar de monitor manda o quadro inteiro")
	}
	if r, _ := d.next(fakeScreen(800, 600), 1); r != nil {
		t.Fatal("trocar de resolução manda o quadro inteiro")
	}
}

func TestDeltaMuitosRetangulosViraUm(t *testing.T) {
	var d deltaEncoder
	img := fakeScreen(1280, 720)
	d.next(img, 0)
	for i := 0; i < 20; i++ { // 20 blocos soltos em diagonal, sem faixas em comum
		x, y := (i%10)*128, (i/10)*256+(i%3)*64
		paint(img, image.Rect(x+1, y+1, x+4, y+4), color.RGBA{255, 255, 0, 255})
	}
	r, _ := d.next(img, 0)
	if len(r) > 1 {
		t.Fatalf("passou de %d retângulos e deveria virar um só: %d", maxRects, len(r))
	}
}

// ida e volta: o que o host manda só com os blocos alterados reconstrói a tela no viewer
func TestBlocosReconstroemATela(t *testing.T) {
	var d deltaEncoder
	var buf bytes.Buffer
	img := fakeScreen(1280, 720)
	d.next(img, 0)
	var full bytes.Buffer
	jpeg.Encode(&full, img, &jpeg.Options{Quality: 80})
	v := &viewSession{}
	v.key = full.Bytes()

	paint(img, image.Rect(300, 200, 420, 260), color.RGBA{250, 30, 30, 255})
	rects, same := d.next(img, 0)
	if same || rects == nil {
		t.Fatal("deveria haver blocos")
	}
	if err := writeTiles(&buf, img, rects, 80); err != nil {
		t.Fatal(err)
	}
	if buf.Len()*5 > full.Len() {
		t.Fatalf("a mensagem de blocos (%d B) deveria ser bem menor que o quadro inteiro (%d B)", buf.Len(), full.Len())
	}
	if err := v.applyTiles(buf.Bytes()[headerBytes:]); err != nil {
		t.Fatal(err)
	}
	out := v.latest()
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	near := func(a, b color.Color) bool {
		r1, g1, b1, _ := a.RGBA()
		r2, g2, b2, _ := b.RGBA()
		d := func(x, y uint32) int { return abs(int(x>>8) - int(y>>8)) }
		return d(r1, r2) < 40 && d(g1, g2) < 40 && d(b1, b2) < 40
	}
	for _, p := range []image.Point{{350, 230}, {10, 10}, {1200, 700}, {299, 199}, {420, 260}} {
		if !near(got.At(p.X, p.Y), img.At(p.X, p.Y)) {
			t.Errorf("pixel %v: remontado %v, tela %v", p, got.At(p.X, p.Y), img.At(p.X, p.Y))
		}
	}

	// segunda mudança em cima da primeira: o viewer mantém a tela remontada
	paint(img, image.Rect(800, 500, 860, 540), color.RGBA{20, 20, 250, 255})
	rects, _ = d.next(img, 0)
	writeTiles(&buf, img, rects, 80)
	if err := v.applyTiles(buf.Bytes()[headerBytes:]); err != nil {
		t.Fatal(err)
	}
	out = v.latest()
	got, _ = jpeg.Decode(bytes.NewReader(out))
	if !near(got.At(350, 230), img.At(350, 230)) || !near(got.At(830, 520), img.At(830, 520)) {
		t.Error("as duas mudanças deveriam estar na tela remontada")
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestBlocosInvalidosSaoRecusados(t *testing.T) {
	img := fakeScreen(256, 256)
	var full, buf bytes.Buffer
	jpeg.Encode(&full, img, nil)

	if err := (&viewSession{}).applyTiles([]byte{0, 0}); err == nil {
		t.Fatal("bloco sem quadro inteiro antes deve ser recusado")
	}
	mk := func() *viewSession { return &viewSession{key: full.Bytes()} }
	writeTiles(&buf, img, []image.Rectangle{image.Rect(0, 0, 64, 64)}, 80)
	good := append([]byte{}, buf.Bytes()[headerBytes:]...)
	if err := mk().applyTiles(good); err != nil {
		t.Fatalf("mensagem boa recusada: %v", err)
	}
	bad := map[string][]byte{
		"vazio":          {},
		"corpo cortado":  good[:len(good)/2],
		"fora da tela":   func() []byte { b := append([]byte{}, good...); b[2], b[3] = 0x10, 0; return b }(), // x = 4096
		"largura zero":   func() []byte { b := append([]byte{}, good...); b[6], b[7] = 0, 0; return b }(),
		"tamanho errado": func() []byte { b := append([]byte{}, good...); b[6], b[7] = 0, 32; return b }(),
		"len gigante": func() []byte {
			b := append([]byte{}, good...)
			b[10], b[11], b[12], b[13] = 0xff, 0xff, 0xff, 0xff
			return b
		}(),
	}
	for nome, b := range bad {
		if err := mk().applyTiles(b); err == nil {
			t.Errorf("%s: deveria ser recusado", nome)
		}
	}
}

// Quadro grande: a tela toda sai em faixas codificadas em paralelo e o viewer remonta igual.
func TestTelaTodaEmFaixas(t *testing.T) {
	img := fakeScreen(1920, 1080)
	rects := splitStrips([]image.Rectangle{img.Bounds()})
	if len(rects) < 2 {
		t.Fatalf("a tela cheia deveria virar várias faixas, veio %d", len(rects))
	}
	area := 0
	for i, r := range rects {
		area += r.Dx() * r.Dy()
		if r.Min.Y%tileSize != 0 || r.Min.X != 0 || r.Dx() != 1920 || (i > 0 && r.Min.Y != rects[i-1].Max.Y) {
			t.Fatalf("faixa %d fora do alinhamento: %v", i, r)
		}
	}
	if area != 1920*1080 {
		t.Fatalf("as faixas deveriam cobrir a tela inteira: %d px", area)
	}
	var key, msg bytes.Buffer
	jpeg.Encode(&key, fakeScreen(1920, 1080), &jpeg.Options{Quality: 50})
	paint(img, image.Rect(0, 0, 1920, 1080), color.RGBA{10, 200, 90, 255})
	if err := writeTiles(&msg, img, []image.Rectangle{img.Bounds()}, 80); err != nil {
		t.Fatal(err)
	}
	v := &viewSession{key: key.Bytes()}
	if err := v.applyTiles(msg.Bytes()[headerBytes:]); err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(v.latest()))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := got.At(1900, 1000).RGBA()
	if abs(int(r>>8)-10) > 12 || abs(int(g>>8)-200) > 12 || abs(int(b>>8)-90) > 12 {
		t.Fatalf("a tela remontada não é a enviada: %v %v %v", r>>8, g>>8, b>>8)
	}
}
