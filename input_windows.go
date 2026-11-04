package main

import (
	"image"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	user32      = syscall.NewLazyDLL("user32.dll")
	setCursor   = user32.NewProc("SetCursorPos")
	mouseEvent  = user32.NewProc("mouse_event")
	sendInput   = user32.NewProc("SendInput")
	mapVK       = user32.NewProc("MapVirtualKeyW")
	setDPIAware = user32.NewProc("SetProcessDPIAware")
)

func init() { setDPIAware.Call() } // coordenadas em pixels reais

// flags de mouse_event por botão: {down, up}
var btnFlags = [3][2]uintptr{{0x2, 0x4}, {0x20, 0x40}, {0x8, 0x10}}

// kbInput é a estrutura INPUT do Windows (x64) para um evento de teclado.
type kbInput struct {
	typ   uint32 // INPUT_KEYBOARD = 1
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
	_     [8]byte // o resto da união (MOUSEINPUT é maior que KEYBDINPUT)
}

var _ = [1]struct{}{}[unsafe.Sizeof(kbInput{})-40] // o INPUT do Windows x64 tem 40 bytes

const (
	keyExtended = 0x1
	keyUp       = 0x2
	keyUnicode  = 0x4
)

// Teclas "estendidas": sem esta marca o Windows as trata como as do teclado numérico (com NumLock
// ligado, a seta para cima viraria o número 8).
var extendedVK = map[int]bool{
	0x21: true, 0x22: true, 0x23: true, 0x24: true, // PgUp PgDn End Home
	0x25: true, 0x26: true, 0x27: true, 0x28: true, // setas
	0x2C: true, 0x2D: true, 0x2E: true, // PrintScreen Insert Delete
	0x5B: true, 0x5C: true, 0x5D: true, // Win esquerda/direita, Menu
	0x6F: true, 0x90: true, // / do teclado numérico, NumLock
}

func key(vk, scan uint16, flags uint32) bool {
	in := kbInput{typ: 1, vk: vk, scan: scan, flags: flags}
	n, _, _ := sendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	return n == 1
}

// pressVK aperta ou solta uma tecla pelo código virtual do Windows (setas, Enter, F1..F12, Ctrl...).
func pressVK(vk int, up bool) bool {
	if vk <= 0 || vk > 0xFE {
		return false
	}
	scan, _, _ := mapVK.Call(uintptr(vk), 0) // MAPVK_VK_TO_VSC
	var f uint32
	if extendedVK[vk] {
		f |= keyExtended
	}
	if up {
		f |= keyUp
	}
	return key(uint16(vk), uint16(scan), f)
}

// typeText digita o texto como caracteres, não como teclas: o resultado é o mesmo mesmo que os dois
// computadores tenham teclados diferentes, e acentos (ç, ã, é) saem certos.
func typeText(s string) {
	n := 0
	for _, r := range s {
		if r < 32 || r == 127 {
			continue // controle: Enter, Tab etc. vão como teclas, não como texto
		}
		if n++; n > 16 { // um evento é uma tecla; este limite só barra abuso
			return
		}
		for _, u := range utf16.Encode([]rune{r}) {
			key(0, u, keyUnicode)
			key(0, u, keyUnicode|keyUp)
		}
	}
}

// releaseKey solta uma tecla que ficou apertada (viewer saiu ou perdeu o foco com ela apertada).
func releaseKey(vk int) { pressVK(vk, true) }

func inject(e Event, b image.Rectangle) {
	switch e.T {
	case "move", "down", "up":
		setCursor.Call(uintptr(b.Min.X+int(e.X*float64(b.Dx()))), uintptr(b.Min.Y+int(e.Y*float64(b.Dy()))))
	}
	switch e.T {
	case "down", "up", "rdown", "rup": // rdown/rup: botão sem mexer o cursor (mouse capturado)
		if e.B >= 0 && e.B < 3 {
			i := 0
			if e.T == "up" || e.T == "rup" {
				i = 1
			}
			mouseEvent.Call(btnFlags[e.B][i], 0, 0, 0, 0)
		}
	case "rel": // movimento relativo (mouse capturado)
		mouseMoveRel(int(e.X), int(e.Y))
	case "wheel":
		mouseEvent.Call(0x800, 0, 0, uintptr(int32(e.D)), 0)
	case "kdown":
		pressVK(e.K, false)
	case "kup":
		pressVK(e.K, true)
	case "char":
		typeText(e.S)
	}
}

var lockWorkstation = func() { user32.NewProc("LockWorkStation").Call() }
