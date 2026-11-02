package main

import (
	"image"
	"testing"
	"time"
)

var getAsyncKeyState = user32.NewProc("GetAsyncKeyState")

func down(vk int) bool {
	s, _, _ := getAsyncKeyState.Call(uintptr(vk))
	return s&0x8000 != 0
}

// Usa F24, que nenhum programa usa, para não atrapalhar o que estiver aberto durante o teste.
func TestInjetarTecla(t *testing.T) {
	const f24 = 0x87
	b := image.Rect(0, 0, 100, 100)
	inject(Event{T: "kdown", K: f24}, b)
	if !down(f24) {
		t.Fatal("a tecla deveria estar apertada depois do kdown (SendInput falhou?)")
	}
	inject(Event{T: "kup", K: f24}, b)
	if down(f24) {
		t.Fatal("a tecla deveria estar solta depois do kup")
	}
}

// Uma tecla apertada que o viewer deixou para trás é solta pelo host, senão fica "presa".
func TestSoltarTeclaPresa(t *testing.T) {
	const f24 = 0x87
	inject(Event{T: "kdown", K: f24}, image.Rectangle{})
	releaseKey(f24)
	if down(f24) {
		t.Fatal("releaseKey deveria soltar a tecla")
	}
}

func TestTeclaInvalidaNaoQuebra(t *testing.T) {
	for _, vk := range []int{-1, 0, 255, 99999} {
		if pressVK(vk, false) {
			t.Errorf("o código %d não é uma tecla válida e deveria ser ignorado", vk)
		}
	}
}

// Do viewer ao teclado do host, pelo protocolo: a tecla apertada no viewer fica apertada no host,
// e se o viewer sair com ela apertada o host solta (senão ela ficaria presa).
func TestTeclaPeloProtocoloESoltaAoSair(t *testing.T) {
	const f24 = 0x87
	host, view, _, _ := pair(t)
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	view.SendInput(host.myID, `{"t":"kdown","k":135}`)
	if !eventually(3*time.Second, func() bool { return down(f24) }) {
		t.Fatal("a tecla apertada no viewer não chegou ao teclado do host")
	}
	view.Disconnect(host.myID) // sai com a tecla apertada
	if !eventually(3*time.Second, func() bool { return !down(f24) }) {
		releaseKey(f24) // não deixa a tecla presa na máquina de quem está testando
		t.Fatal("o host deveria soltar a tecla quando o viewer sai")
	}
}

// Sem permissão de controle (só visualização) nenhuma tecla é injetada.
func TestTeclaNaoVaiNoModoSoVisualizacao(t *testing.T) {
	const f24 = 0x87
	host, view, _, _ := pair(t)
	s := host.settings()
	s.AllowControl = false
	if err := host.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := view.Connect(host.myID, "", false); err != nil {
		t.Fatal(err)
	}
	defer view.Disconnect(host.myID)
	view.SendInput(host.myID, `{"t":"kdown","k":135}`)
	time.Sleep(500 * time.Millisecond)
	if down(f24) {
		releaseKey(f24)
		t.Fatal("no modo só visualização a tecla não deveria ser injetada")
	}
}
