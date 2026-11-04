package main

import "encoding/json"

// Opções de uma sessão escolhidas pelo viewer (menu da barra da sessão). "clip" vale só neste
// computador; "lockEnd" e "clipFiles" avisam o host. Os outros recursos (áudio, bloquear
// entradas, privacidade, resolução, cursor...) o frontend manda direto como eventos (hostfx.go).

// SetSessionOption liga ou desliga a opção key da sessão com o computador id.
func (a *App) SetSessionOption(id, key string, on bool) {
	a.mu.Lock()
	v := a.out[onlyDigits(id)]
	a.mu.Unlock()
	if v == nil {
		return
	}
	k := 0
	if on {
		k = 1
	}
	send := func(t string) {
		b, _ := json.Marshal(Event{T: t, K: k})
		v.write(append(b, '\n'))
	}
	switch key {
	case "clip":
		v.noClip.Store(!on)
	case "clipFiles":
		v.fileClip.Store(on)
		send("clipfilesopt")
	case "lockEnd":
		send("lockend")
	}
}
