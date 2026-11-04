//go:build windows

package main

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kbinani/screenshot"
)

func TestCursorEJanelaEmFoco(t *testing.T) {
	if _, _, ok := cursorPos(); !ok {
		t.Fatal("GetCursorPos falhou")
	}
	if r, ok := foregroundRect(); ok && r.Empty() {
		t.Fatal("janela em foco com área vazia")
	}
}

// Pedir a resolução que já está em uso não muda nada (e confere o layout do DEVMODE).
func TestResolucaoAtualNaoMuda(t *testing.T) {
	cur, ok := displayMode(enumCurrent)
	if !ok {
		t.Skip("sem monitor")
	}
	if err := setResolution(int(cur.pelsWidth), int(cur.pelsHeight)); err != nil {
		t.Fatal(err)
	}
	if again, _ := displayMode(enumCurrent); again.pelsWidth != cur.pelsWidth || again.pelsHeight != cur.pelsHeight {
		t.Fatal("a resolução mudou")
	}
	if dm, ok := bestMode(int(cur.pelsWidth), int(cur.pelsHeight)); !ok || dm.pelsWidth != cur.pelsWidth {
		t.Fatalf("bestMode deveria escolher a resolução atual: %v %v", ok, dm.pelsWidth)
	}
}

// Com o bloqueio ligado, o que o próprio CDesk injeta continua passando (senão o viewer perderia o controle).
func TestBloqueioDeixaPassarOInjetado(t *testing.T) {
	const f24 = 0x87
	if err := blockInput(true); err != nil {
		t.Fatal(err)
	}
	defer blockInput(false)
	b := image.Rect(0, 0, 100, 100)
	inject(Event{T: "kdown", K: f24}, b)
	if !down(f24) {
		t.Fatal("a tecla injetada deveria passar pelo bloqueio")
	}
	inject(Event{T: "kup", K: f24}, b)
	blockInput(false)
	if !eventually(2*time.Second, func() bool { hookMu.Lock(); defer hookMu.Unlock(); return hookTid == 0 }) {
		t.Fatal("os ganchos deveriam ser removidos ao desbloquear")
	}
}

// A janela de privacidade cobre a tela mas não aparece na captura: quem controla vê a tela normal.
func TestPrivacidadeNaoApareceNaCaptura(t *testing.T) {
	area := image.Rect(60, 60, 260, 260)
	privBounds = &area
	defer func() { privBounds = nil }()
	before, err := screenshot.CaptureRect(area)
	if err != nil {
		t.Skip("sem captura:", err)
	}
	if err := privacyMode(true); err != nil {
		t.Skip("modo privacidade indisponível neste Windows:", err)
	}
	time.Sleep(700 * time.Millisecond)
	after, _ := screenshot.CaptureRect(area)
	privacyMode(false)
	bright := 0
	for i := 0; i < len(before.Pix); i += 4 {
		bright += int(before.Pix[i])
	}
	if bright/(len(before.Pix)/4) < 25 {
		t.Skip("a área testada já é escura: o teste não distinguiria a janela preta")
	}
	diff := 0
	for i := 0; i < len(before.Pix); i += 4 {
		d := int(before.Pix[i]) - int(after.Pix[i])
		if d < 0 {
			d = -d
		}
		diff += d
	}
	if avg := diff / (len(before.Pix) / 4); avg > 12 {
		t.Fatalf("a captura mostrou a janela de privacidade (diferença média %d)", avg)
	}
}

func TestDropFilesMontaOCabecalho(t *testing.T) {
	b := dropFiles([]string{`C:\a.txt`})
	if b[0] != 20 || b[16] != 1 || len(b) != 20+2*(len(`C:\a.txt`)+1)+2 {
		t.Fatalf("DROPFILES inválido: % x", b)
	}
}

// Mexe na área de transferência do usuário, então só roda com CDESK_TEST_CLIP=1.
func TestArquivosNaAreaDeTransferencia(t *testing.T) {
	if os.Getenv("CDESK_TEST_CLIP") != "1" {
		t.Skip("define CDESK_TEST_CLIP=1 para testar (sobrescreve a área de transferência)")
	}
	d := t.TempDir()
	want := []string{filepath.Join(d, "a.txt"), filepath.Join(d, "b ç.txt")}
	if err := setClipFiles(want); err != nil {
		t.Fatal(err)
	}
	if got := clipFiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("leu %v, queria %v", got, want)
	}
}

// A captura inicia sem erro; com CDESK_TEST_AUDIO=1 toca um bip curto e confere que chegou PCM.
func TestCapturaDeAudio(t *testing.T) {
	stop := make(chan struct{})
	got := make(chan int, 64)
	errc := make(chan error, 1)
	go func() {
		errc <- audioCapture(stop, func(b []byte) {
			select {
			case got <- len(b):
			default:
			}
		})
	}()
	if os.Getenv("CDESK_TEST_AUDIO") == "1" {
		time.Sleep(300 * time.Millisecond)
		exec.Command("powershell", "-NoProfile", "-Command", "[console]::Beep(800,400)").Run()
	}
	time.Sleep(800 * time.Millisecond)
	close(stop)
	if err := <-errc; err != nil {
		t.Skip("sem áudio neste computador:", err)
	}
	if os.Getenv("CDESK_TEST_AUDIO") == "1" {
		total := 0
		for len(got) > 0 {
			total += <-got
		}
		if total == 0 {
			t.Fatal("tocou um bip e a captura não recebeu nada")
		}
		t.Logf("recebidos %d bytes de PCM", total)
	}
}

// Troca o papel de parede por um instante; só roda com CDESK_TEST_WALL=1.
func TestPapelDeParede(t *testing.T) {
	if os.Getenv("CDESK_TEST_WALL") != "1" {
		t.Skip("define CDESK_TEST_WALL=1 para testar (o papel de parede some por um instante)")
	}
	orig := wallpaper()
	restore := hideWallpaper()
	time.Sleep(300 * time.Millisecond)
	restore()
	if got := wallpaper(); got != orig {
		t.Fatalf("papel de parede não voltou: %q != %q", got, orig)
	}
}
