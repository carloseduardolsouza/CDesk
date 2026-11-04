package main

import (
	"encoding/json"
	"errors"
	"time"
)

// Chat da sessão: mensagens de texto nos dois sentidos pela mesma conexão. O viewer manda uma
// linha {"t":"chat"} e o host responde com uma mensagem msgChat. No viewer o histórico fica no
// frontend; no host ele fica aqui (hostChat), porque quem o mostra é a janela da sessão, que é
// outro processo (session_win.go).

const maxChat = 2000

// chatMsg é uma linha do chat (Who: me | them | sys) ou do registro de arquivos (só Text e At).
type chatMsg struct {
	Who  string `json:"who"`
	Text string `json:"text"`
	At   int64  `json:"at"` // ms desde 1970
}

func nowMs() int64 { return time.Now().UnixMilli() }

// chatReceived avisa o frontend de uma mensagem vinda do host id (lado viewer). O texto vem de
// fora, então perde caracteres de controle e é limitado.
func (a *App) chatReceived(id, text string) {
	if text = cleanText(text, maxChat); text != "" {
		a.emit("chat", id, text)
	}
}

// hostChatAdd guarda uma mensagem na conversa da sessão em que este computador é o host.
func (a *App) hostChatAdd(who, text string) {
	if text = cleanText(text, maxChat); text == "" {
		return
	}
	a.mu.Lock()
	a.hostChat = append(a.hostChat, chatMsg{Who: who, Text: text, At: nowMs()})
	a.mu.Unlock()
}

// hostLogAdd registra um arquivo ou operação feita pelo viewer (aba Transferência de Arquivos).
func (a *App) hostLogAdd(text string) {
	if text == "" {
		return
	}
	a.mu.Lock()
	a.hostLog = append(a.hostLog, chatMsg{Text: cleanText(text, 300), At: nowMs()})
	a.mu.Unlock()
}

// SendChat manda text ao computador id: como viewer (aba aberta) ou como host (quem nos controla).
func (a *App) SendChat(id, text string) error {
	id = onlyDigits(id)
	text = cleanText(text, maxChat)
	if text == "" {
		return nil
	}
	a.mu.Lock()
	v, hc := a.out[id], a.in
	a.mu.Unlock()
	if v != nil {
		b, _ := json.Marshal(Event{T: "chat", S: text})
		v.write(append(b, '\n'))
		return nil
	}
	if hc != nil && numID(hc.s.Conn().RemotePeer()) == id {
		if err := hc.sendMsg(msgChat, []byte(text)); err != nil {
			return err
		}
		a.hostChatAdd("me", text)
		return nil
	}
	return errors.New("Sessão encerrada.")
}
