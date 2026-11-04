//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"math"
	"runtime"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// Captura o que está tocando no computador (WASAPI em modo loopback da saída padrão) e entrega
// em blocos de PCM de 16 bits, estéreo, a audioRate Hz. Sem som tocando o Windows não gera
// dados, então não sai nada (e nada trafega) até voltar a tocar.
// shortcut: PCM sem compressão (~96 KB/s) e reamostragem simples; subir para Opus se a banda apertar.

const audioRate = 24000

// audioCapture roda até stop fechar (ou dar erro) e chama out a cada bloco de áudio.
func audioCapture(stop <-chan struct{}, out func(pcm []byte)) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		return err
	}
	defer ole.CoUninitialize()

	var mmde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &mmde); err != nil {
		return err
	}
	defer mmde.Release()
	var mmd *wca.IMMDevice
	if err := mmde.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &mmd); err != nil {
		return errors.New("sem dispositivo de áudio")
	}
	defer mmd.Release()
	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		return err
	}
	defer ac.Release()
	var wfx *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&wfx); err != nil {
		return err
	}
	if wfx.WBitsPerSample != 32 && wfx.WBitsPerSample != 16 { // o formato de mixagem do Windows é float de 32 bits ou PCM de 16
		return errors.New("formato de áudio não suportado")
	}
	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, wca.AUDCLNT_STREAMFLAGS_LOOPBACK, 2000000, 0, wfx, nil); err != nil {
		return err
	}
	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		return err
	}
	defer acc.Release()
	if err := ac.Start(); err != nil {
		return err
	}
	defer ac.Stop()

	ch, sr, bits, align := int(wfx.NChannels), int(wfx.NSamplesPerSec), int(wfx.WBitsPerSample), int(wfx.NBlockAlign)
	if ch < 1 || sr < 8000 || align < 1 {
		return errors.New("formato de áudio inválido")
	}
	sample := func(buf []byte, frame, c int) float64 {
		o := frame*align + c*bits/8
		if bits == 32 {
			return float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[o:])))
		}
		return float64(int16(binary.LittleEndian.Uint16(buf[o:]))) / 32768
	}
	pcm16 := func(v float64) []byte {
		v = max(-1, min(1, v))
		return binary.LittleEndian.AppendUint16(nil, uint16(int16(v*32767)))
	}
	pos := 0.0 // posição fracionária no fluxo de entrada, em quadros
	step := float64(sr) / audioRate
	for {
		select {
		case <-stop:
			return nil
		case <-time.After(20 * time.Millisecond):
		}
		for {
			var n uint32
			if err := acc.GetNextPacketSize(&n); err != nil || n == 0 {
				break
			}
			var data *byte
			var frames, flags uint32
			if err := acc.GetBuffer(&data, &frames, &flags, nil, nil); err != nil {
				return err
			}
			buf := unsafe.Slice(data, int(frames)*align)
			var block []byte
			for ; int(pos) < int(frames); pos += step {
				f := int(pos)
				l, r := 0.0, 0.0
				if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT == 0 {
					l = sample(buf, f, 0)
					r = l
					if ch > 1 {
						r = sample(buf, f, 1)
					}
				}
				block = append(block, pcm16(l)...)
				block = append(block, pcm16(r)...)
			}
			pos -= float64(frames)
			acc.ReleaseBuffer(frames)
			if len(block) > 0 {
				out(block)
			}
		}
	}
}
