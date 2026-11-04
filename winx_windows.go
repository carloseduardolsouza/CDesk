//go:build windows

package main

import (
	"errors"
	"fmt"
	"image"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Recursos do Windows usados no computador controlado: cursor e janela em foco (para o viewer
// seguir), janelas fora da tela, resolução, bloqueio das entradas do usuário, modo privacidade,
// papel de parede e arquivos na área de transferência. Tudo por chamadas diretas à API do Windows.

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	pGetWindowRect       = user32.NewProc("GetWindowRect")
	pIsWindowVisible     = user32.NewProc("IsWindowVisible")
	pIsIconic            = user32.NewProc("IsIconic")
	pEnumWindows         = user32.NewProc("EnumWindows")
	pGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	pSetWindowPos        = user32.NewProc("SetWindowPos")
	pDwmGetWindowAttr    = dwmapi.NewProc("DwmGetWindowAttribute")

	pEnumDisplaySettings = user32.NewProc("EnumDisplaySettingsW")
	pChangeDisplay       = user32.NewProc("ChangeDisplaySettingsExW")

	pSetHookEx       = user32.NewProc("SetWindowsHookExW")
	pUnhookEx        = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHook    = user32.NewProc("CallNextHookEx")
	pGetMessage      = user32.NewProc("GetMessageW")
	pTranslateMsg    = user32.NewProc("TranslateMessage")
	pDispatchMsg     = user32.NewProc("DispatchMessageW")
	pPostThreadMsg   = user32.NewProc("PostThreadMessageW")
	pPostMessage     = user32.NewProc("PostMessageW")
	pPostQuit        = user32.NewProc("PostQuitMessage")
	pAsyncKey        = user32.NewProc("GetAsyncKeyState")
	pGetCurrentTid   = kernel32.NewProc("GetCurrentThreadId")
	pGetModuleHandle = kernel32.NewProc("GetModuleHandleW")

	pRegisterClass   = user32.NewProc("RegisterClassExW")
	pCreateWindowEx  = user32.NewProc("CreateWindowExW")
	pDefWindowProc   = user32.NewProc("DefWindowProcW")
	pShowWindow      = user32.NewProc("ShowWindow")
	pSetLayeredAttrs = user32.NewProc("SetLayeredWindowAttributes")
	pSetAffinity     = user32.NewProc("SetWindowDisplayAffinity")
	pDestroyWindow   = user32.NewProc("DestroyWindow")
	pGetMetrics      = user32.NewProc("GetSystemMetrics")
	pGetStockObject  = gdi32.NewProc("GetStockObject")

	pSysParams = user32.NewProc("SystemParametersInfoW")

	pOpenClipboard   = user32.NewProc("OpenClipboard")
	pCloseClipboard  = user32.NewProc("CloseClipboard")
	pEmptyClipboard  = user32.NewProc("EmptyClipboard")
	pGetClipData     = user32.NewProc("GetClipboardData")
	pSetClipData     = user32.NewProc("SetClipboardData")
	pClipFormatAvail = user32.NewProc("IsClipboardFormatAvailable")
	pClipSeq         = user32.NewProc("GetClipboardSequenceNumber")
	pDragQueryFile   = shell32.NewProc("DragQueryFileW")
	pGlobalAlloc     = kernel32.NewProc("GlobalAlloc")
	pGlobalLock      = kernel32.NewProc("GlobalLock")
	pGlobalUnlock    = kernel32.NewProc("GlobalUnlock")
	pGlobalFree      = kernel32.NewProc("GlobalFree")
)

type point struct{ x, y int32 }
type rect struct{ l, t, r, b int32 }

func (r rect) image() image.Rectangle { return image.Rect(int(r.l), int(r.t), int(r.r), int(r.b)) }

// ---- cursor, janela em foco e janelas fora da tela ----

func cursorPos() (x, y int, ok bool) {
	var p point
	r, _, _ := pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return int(p.x), int(p.y), r != 0
}

func foregroundRect() (image.Rectangle, bool) {
	h, _, _ := pGetForegroundWindow.Call()
	if h == 0 {
		return image.Rectangle{}, false
	}
	var r rect
	if ok, _, _ := pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return image.Rectangle{}, false
	}
	return r.image(), true
}

func mouseMoveRel(dx, dy int) { mouseEvent.Call(0x1, uintptr(int32(dx)), uintptr(int32(dy)), 0, 0) } // MOUSEEVENTF_MOVE

var (
	rescueMu   sync.Mutex
	rescueDisp []image.Rectangle
	enumCB     = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if v, _, _ := pIsIconic.Call(hwnd); v != 0 {
			return 1
		}
		if n, _, _ := pGetWindowTextLength.Call(hwnd); n == 0 {
			return 1
		}
		var cloaked uint32 // janelas "escondidas" do Windows (apps suspensos) não contam
		pDwmGetWindowAttr.Call(hwnd, 14, uintptr(unsafe.Pointer(&cloaked)), 4)
		if cloaked != 0 {
			return 1
		}
		var r rect
		pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		w := r.image()
		if w.Empty() || len(rescueDisp) == 0 {
			return 1
		}
		for _, d := range rescueDisp {
			if w.Overlaps(d) {
				return 1
			}
		}
		pSetWindowPos.Call(hwnd, 0, uintptr(rescueDisp[0].Min.X+60), uintptr(rescueDisp[0].Min.Y+60), 0, 0, 0x15) // NOSIZE|NOZORDER|NOACTIVATE
		return 1
	})
)

// rescueWindows traz para dentro da tela as janelas que estão totalmente fora dos monitores.
func rescueWindows(displays []image.Rectangle) {
	rescueMu.Lock()
	defer rescueMu.Unlock()
	rescueDisp = displays
	pEnumWindows.Call(enumCB, 0)
}

// ---- resolução ----

// devMode é o DEVMODEW do Windows (220 bytes).
type devMode struct {
	deviceName    [32]uint16
	specVersion   uint16
	driverVersion uint16
	size          uint16
	driverExtra   uint16
	fields        uint32
	_             [16]byte // união de impressora/vídeo (posição, orientação)
	color         int16
	duplex        int16
	yResolution   int16
	ttOption      int16
	collate       int16
	formName      [32]uint16
	logPixels     uint16
	bitsPerPel    uint32
	pelsWidth     uint32
	pelsHeight    uint32
	displayFlags  uint32
	frequency     uint32
	_             [32]byte // ICM, mídia, ... e panning
}

var _ = [1]struct{}{}[unsafe.Sizeof(devMode{})-220]

const (
	enumCurrent      = 0xFFFFFFFF
	dmPelsWidth      = 0x80000
	dmPelsHeight     = 0x100000
	dmBitsPerPel     = 0x40000
	dmFrequency      = 0x400000
	cdsFullscreen    = 0x4 // temporário: o Windows volta à resolução salva quando o processo termina
	dispChangeOK     = 0
	minResW, minResH = 800, 600
)

func displayMode(i uint32) (devMode, bool) {
	dm := devMode{size: 220}
	r, _, _ := pEnumDisplaySettings.Call(0, uintptr(i), uintptr(unsafe.Pointer(&dm)))
	return dm, r != 0
}

// bestMode escolhe, entre as resoluções do monitor principal, a mais parecida com w x h (mesma
// proporção de tela, de preferência), com a mesma cor e taxa de atualização da atual.
func bestMode(w, h int) (devMode, bool) {
	cur, ok := displayMode(enumCurrent)
	if !ok || w <= 0 || h <= 0 {
		return devMode{}, false
	}
	var best devMode
	bestScore := math.MaxFloat64
	for i := uint32(0); ; i++ {
		dm, ok := displayMode(i)
		if !ok {
			break
		}
		if dm.bitsPerPel != cur.bitsPerPel || dm.frequency != cur.frequency || int(dm.pelsWidth) < minResW || int(dm.pelsHeight) < minResH {
			continue
		}
		aspect := math.Abs(float64(dm.pelsWidth)/float64(dm.pelsHeight) - float64(w)/float64(h))
		score := math.Abs(float64(int(dm.pelsWidth)-w)) + math.Abs(float64(int(dm.pelsHeight)-h)) + aspect*4000
		if score < bestScore {
			best, bestScore = dm, score
		}
	}
	return best, bestScore < math.MaxFloat64
}

// setResolution muda a resolução do monitor principal para a mais próxima de w x h; 0 x 0 volta
// à resolução salva. A mudança é temporária (cdsFullscreen).
func setResolution(w, h int) error {
	if w == 0 && h == 0 {
		pChangeDisplay.Call(0, 0, 0, 0, 0)
		return nil
	}
	dm, ok := bestMode(w, h)
	if !ok {
		return errors.New("nenhuma resolução compatível")
	}
	if cur, _ := displayMode(enumCurrent); cur.pelsWidth == dm.pelsWidth && cur.pelsHeight == dm.pelsHeight {
		return nil
	}
	dm.fields = dmPelsWidth | dmPelsHeight | dmBitsPerPel | dmFrequency
	if r, _, _ := pChangeDisplay.Call(0, uintptr(unsafe.Pointer(&dm)), 0, cdsFullscreen, 0); r != dispChangeOK {
		return fmt.Errorf("o Windows recusou a resolução (código %d)", int32(r))
	}
	return nil
}

// ---- bloquear as entradas de quem está no computador ----

// Ganchos de baixo nível descartam o mouse e o teclado FÍSICOS; o que o CDesk injeta (marcado como
// "injetado" pelo Windows) passa. Ctrl+Alt+Shift+F12 no teclado do próprio computador desbloqueia.
var (
	blockOn     atomic.Bool
	hookMu      sync.Mutex
	hookTid     uint32
	onEmergency func() // chamado quando o usuário local usa o atalho de emergência

	kbHookCB = syscall.NewCallback(func(code int32, wp, lp uintptr) uintptr {
		if code >= 0 && blockOn.Load() {
			k := (*struct{ vk, scan, flags, time uint32 })(*(*unsafe.Pointer)(unsafe.Pointer(&lp)))
			if k.flags&0x10 == 0 { // LLKHF_INJECTED
				if k.vk == 0x7B && keyDown(0x11) && keyDown(0x12) && keyDown(0x10) { // Ctrl+Alt+Shift+F12
					blockOn.Store(false)
					if f := onEmergency; f != nil {
						go f()
					}
					r, _, _ := pCallNextHook.Call(0, uintptr(code), wp, lp)
					return r
				}
				return 1
			}
		}
		r, _, _ := pCallNextHook.Call(0, uintptr(code), wp, lp)
		return r
	})
	msHookCB = syscall.NewCallback(func(code int32, wp, lp uintptr) uintptr {
		if code >= 0 && blockOn.Load() {
			m := (*struct {
				x, y      int32
				data, flg uint32
			})(*(*unsafe.Pointer)(unsafe.Pointer(&lp)))
			if m.flg&0x1 == 0 { // LLMHF_INJECTED
				return 1
			}
		}
		r, _, _ := pCallNextHook.Call(0, uintptr(code), wp, lp)
		return r
	})
)

func keyDown(vk uintptr) bool { s, _, _ := pAsyncKey.Call(vk); return s&0x8000 != 0 }

// blockInput liga ou desliga o bloqueio. Os ganchos ficam instalados só enquanto há bloqueio.
func blockInput(on bool) error {
	hookMu.Lock()
	defer hookMu.Unlock()
	blockOn.Store(on)
	if !on {
		if hookTid != 0 {
			pPostThreadMsg.Call(uintptr(hookTid), 0x12, 0, 0) // WM_QUIT
		}
		return nil
	}
	if hookTid != 0 {
		return nil
	}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid, _, _ := pGetCurrentTid.Call()
		kh, _, _ := pSetHookEx.Call(13, kbHookCB, 0, 0) // WH_KEYBOARD_LL
		mh, _, _ := pSetHookEx.Call(14, msHookCB, 0, 0) // WH_MOUSE_LL
		if kh == 0 || mh == 0 {
			pUnhookEx.Call(kh)
			pUnhookEx.Call(mh)
			ready <- errors.New("o Windows não permitiu bloquear as entradas")
			return
		}
		hookTid = uint32(tid) // hookMu está com quem chamou, que só espera o canal
		ready <- nil
		var msg [48]byte
		for {
			if r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0); int32(r) <= 0 {
				break
			}
			pTranslateMsg.Call(uintptr(unsafe.Pointer(&msg[0])))
			pDispatchMsg.Call(uintptr(unsafe.Pointer(&msg[0])))
		}
		pUnhookEx.Call(kh)
		pUnhookEx.Call(mh)
		hookMu.Lock()
		hookTid = 0
		hookMu.Unlock()
	}()
	if err := <-ready; err != nil {
		blockOn.Store(false)
		return err
	}
	return nil
}

// ---- modo privacidade ----

// Uma janela preta, sempre à frente e transparente aos cliques, cobre os monitores do computador.
// Ela é excluída da captura (WDA_EXCLUDEFROMCAPTURE), então quem controla continua vendo a tela
// normal e quem está no computador vê só preto.
var (
	privMu     sync.Mutex
	privHwnd   uintptr
	privBounds *image.Rectangle // testes usam uma área pequena
	privOnce   sync.Once
	privClass  *uint16
	privProc   = syscall.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
		if msg == 0x2 { // WM_DESTROY
			pPostQuit.Call(0)
			return 0
		}
		r, _, _ := pDefWindowProc.Call(hwnd, msg, wp, lp)
		return r
	})
)

type wndClassEx struct {
	size       uint32
	style      uint32
	proc       uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menu       *uint16
	className  *uint16
	iconSm     uintptr
}

func privacyMode(on bool) error {
	privMu.Lock()
	defer privMu.Unlock()
	if !on {
		if privHwnd != 0 {
			pPostMessage.Call(privHwnd, 0x10, 0, 0) // WM_CLOSE: o padrão destrói a janela
			privHwnd = 0
		}
		return nil
	}
	if privHwnd != 0 {
		return nil
	}
	type result struct {
		hwnd uintptr
		err  error
	}
	res := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		privOnce.Do(func() {
			privClass, _ = syscall.UTF16PtrFromString("CDeskPrivacy")
			inst, _, _ := pGetModuleHandle.Call(0)
			black, _, _ := pGetStockObject.Call(4) // BLACK_BRUSH
			wc := wndClassEx{proc: privProc, instance: inst, background: black, className: privClass}
			wc.size = uint32(unsafe.Sizeof(wc))
			pRegisterClass.Call(uintptr(unsafe.Pointer(&wc)))
		})
		x, _, _ := pGetMetrics.Call(76) // SM_XVIRTUALSCREEN
		y, _, _ := pGetMetrics.Call(77)
		w, _, _ := pGetMetrics.Call(78)
		h, _, _ := pGetMetrics.Call(79)
		b := image.Rect(int(int32(x)), int(int32(y)), int(int32(x))+int(int32(w)), int(int32(y))+int(int32(h)))
		if privBounds != nil {
			b = *privBounds
		}
		// TOPMOST | TOOLWINDOW | LAYERED | TRANSPARENT | NOACTIVATE
		hwnd, _, _ := pCreateWindowEx.Call(0x8|0x80|0x80000|0x20|0x08000000, uintptr(unsafe.Pointer(privClass)), 0, 0x80000000,
			uintptr(b.Min.X), uintptr(b.Min.Y), uintptr(b.Dx()), uintptr(b.Dy()), 0, 0, 0, 0)
		if hwnd == 0 {
			res <- result{err: errors.New("não foi possível criar a janela de privacidade")}
			return
		}
		pSetLayeredAttrs.Call(hwnd, 0, 255, 2)                  // LWA_ALPHA
		if ok, _, _ := pSetAffinity.Call(hwnd, 0x11); ok == 0 { // WDA_EXCLUDEFROMCAPTURE (Windows 10 2004 ou mais novo)
			pDestroyWindow.Call(hwnd)
			res <- result{err: errors.New("o modo privacidade precisa do Windows 10 versão 2004 ou mais novo")}
			return
		}
		pShowWindow.Call(hwnd, 4) // SW_SHOWNOACTIVATE
		res <- result{hwnd: hwnd}
		var msg [48]byte
		for {
			if r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0); int32(r) <= 0 {
				break
			}
			pTranslateMsg.Call(uintptr(unsafe.Pointer(&msg[0])))
			pDispatchMsg.Call(uintptr(unsafe.Pointer(&msg[0])))
		}
	}()
	r := <-res
	privHwnd = r.hwnd
	return r.err
}

// ---- papel de parede ----

func wallpaper() string {
	var buf [520]uint16
	pSysParams.Call(0x73, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), 0) // SPI_GETDESKWALLPAPER
	return syscall.UTF16ToString(buf[:])
}

// setWallpaper troca o papel de parede sem gravar na configuração do usuário; "" deixa só a cor de fundo.
func setWallpaper(path string) {
	p, _ := syscall.UTF16PtrFromString(path)
	pSysParams.Call(0x14, 0, uintptr(unsafe.Pointer(p)), 0) // SPI_SETDESKWALLPAPER
}

// ---- arquivos copiados (CF_HDROP) ----

const cfHDrop = 15

func clipSeq() uint32 { r, _, _ := pClipSeq.Call(); return uint32(r) }

func openClip() bool {
	for i := 0; i < 10; i++ {
		if r, _, _ := pOpenClipboard.Call(0); r != 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// clipFiles devolve os arquivos que o usuário copiou (Ctrl+C no Explorer), ou nil.
func clipFiles() []string {
	if r, _, _ := pClipFormatAvail.Call(cfHDrop); r == 0 || !openClip() {
		return nil
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipData.Call(cfHDrop)
	if h == 0 {
		return nil
	}
	n, _, _ := pDragQueryFile.Call(h, 0xFFFFFFFF, 0, 0)
	var out []string
	for i := uintptr(0); i < n; i++ {
		l, _, _ := pDragQueryFile.Call(h, i, 0, 0)
		buf := make([]uint16, l+1)
		pDragQueryFile.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), l+1)
		out = append(out, syscall.UTF16ToString(buf))
	}
	return out
}

// dropFiles monta o DROPFILES do Windows: cabeçalho de 20 bytes e os caminhos em UTF-16, um após o outro.
func dropFiles(paths []string) []byte {
	b := make([]byte, 20)
	b[0] = 20 // pFiles: onde começam os nomes
	b[16] = 1 // fWide: UTF-16
	for _, p := range paths {
		for _, u := range syscall.StringToUTF16(p) { // inclui o 0 final de cada nome
			b = append(b, byte(u), byte(u>>8))
		}
	}
	return append(b, 0, 0) // 0 duplo termina a lista
}

// setClipFiles põe arquivos na área de transferência como se tivessem sido copiados no Explorer.
func setClipFiles(paths []string) error {
	data := dropFiles(paths)
	h, _, _ := pGlobalAlloc.Call(0x42, uintptr(len(data))) // GHND
	if h == 0 {
		return errors.New("sem memória")
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		pGlobalFree.Call(h)
		return errors.New("sem memória")
	}
	copy(unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), len(data)), data)
	pGlobalUnlock.Call(h)
	if !openClip() {
		pGlobalFree.Call(h)
		return errors.New("a área de transferência está em uso")
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	if r, _, _ := pSetClipData.Call(cfHDrop, h); r == 0 {
		pGlobalFree.Call(h)
		return errors.New("não foi possível usar a área de transferência")
	}
	return nil
}
