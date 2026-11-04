package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
)

const version = "0.1.0"

// Allowed é um computador na lista de permitidos.
type Allowed struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// Settings são as configurações do usuário, salvas em settings.json na pasta de dados.
type Settings struct {
	Name           string    `json:"name"`           // nome deste computador, mostrado a quem recebe o pedido
	AskSeconds     int       `json:"askSeconds"`     // 15, 30 ou 60
	AskOnTop       bool      `json:"askOnTop"`       // janela do pedido sempre à frente
	AskSound       bool      `json:"askSound"`       // toca um som ao receber pedido
	AllowControl   bool      `json:"allowControl"`   // false: quem se conecta só vê a tela
	AllowClipboard bool      `json:"allowClipboard"` // copiar e colar entre os computadores
	AllowFiles     bool      `json:"allowFiles"`     // transferir arquivos durante a sessão
	OnlyAllowed    bool      `json:"onlyAllowed"`    // só IDs da lista Allowed podem pedir acesso
	Allowed        []Allowed `json:"allowed"`
	MDNS           bool      `json:"mdns"`          // achar PCs na rede local (vale após reiniciar)
	UPnP           bool      `json:"upnp"`          // abrir portas no roteador (vale após reiniciar)
	HolePunch      bool      `json:"holePunch"`     // atravessar NAT (vale após reiniciar)
	Port           int       `json:"port"`          // 0 = automática (vale após reiniciar)
	Priority       string    `json:"priority"`      // velocidade | equilibrada | qualidade
	FPS            int       `json:"fps"`           // 5..30
	TrayOnClose    bool      `json:"trayOnClose"`   // fechar a janela esconde o CDesk na bandeja
	CheckUpdates   bool      `json:"checkUpdates"`  // ao abrir, procurar versão nova
	ShowCursor     bool      `json:"showCursor"`    // sessões novas já mostram o cursor do computador remoto
	HideWallpaper  bool      `json:"hideWallpaper"` // pedir ao host para esconder o papel de parede na sessão
	Relay          string    `json:"relay"`         // relay próprio (endereço completo, com /p2p/ID); vale após reiniciar
}

func defaultSettings() Settings {
	return Settings{
		AskSeconds: 30, AskOnTop: true, AskSound: true, AllowControl: true, AllowClipboard: true, AllowFiles: true,
		Allowed: []Allowed{},
		MDNS:    true, UPnP: true, HolePunch: true,
		Priority: "equilibrada", FPS: 15, TrayOnClose: true,
	}
}

func (a *App) loadSettings() {
	s := defaultSettings()
	if b, err := os.ReadFile(filepath.Join(a.dir, "settings.json")); err == nil {
		json.Unmarshal(b, &s) // campos ausentes ficam com o padrão
	}
	if s.Allowed == nil {
		s.Allowed = []Allowed{}
	}
	a.cfg = s
}

func (a *App) settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.cfg
	s.Allowed = append([]Allowed{}, s.Allowed...)
	return s
}

func onlyDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// cleanText tira caracteres de controle e limita o tamanho (nomes vêm de outros computadores).
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// GetSettings devolve as configurações atuais ao frontend.
func (a *App) GetSettings() Settings { return a.settings() }

// SaveSettings valida e salva as configurações. As de rede só valem depois de reiniciar o CDesk.
func (a *App) SaveSettings(s Settings) error {
	s.Name = cleanText(s.Name, 40)
	if s.AskSeconds != 15 && s.AskSeconds != 30 && s.AskSeconds != 60 {
		return fmt.Errorf("Tempo do pedido inválido.")
	}
	if s.Priority != "velocidade" && s.Priority != "equilibrada" && s.Priority != "qualidade" {
		return fmt.Errorf("Prioridade de imagem inválida.")
	}
	if s.FPS < 5 || s.FPS > 30 {
		return fmt.Errorf("Quadros por segundo precisa ficar entre 5 e 30.")
	}
	s.Relay = strings.TrimSpace(s.Relay)
	if _, err := peer.AddrInfoFromString(s.Relay); s.Relay != "" && err != nil {
		return fmt.Errorf("Endereço do relay inválido. Use o endereço completo, como /dns4/relay.exemplo.com/tcp/4001/p2p/12D3Koo...")
	}
	if s.Port != 0 && (s.Port < 1024 || s.Port > 65535) {
		return fmt.Errorf("A porta precisa ficar entre 1024 e 65535, ou vazia para automática.")
	}
	allowed := []Allowed{}
	for _, p := range s.Allowed {
		id := onlyDigits(p.ID)
		if len(id) != 9 {
			return fmt.Errorf("ID inválido na lista de permitidos: %q.", p.ID)
		}
		allowed = append(allowed, Allowed{Name: cleanText(p.Name, 40), ID: id})
	}
	s.Allowed = allowed

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(a.dir, "settings.json"), b, 0o600); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = s
	a.mu.Unlock()
	return nil
}

// ConfigDir é a pasta onde ficam a chave, a senha e as configurações.
func (a *App) ConfigDir() string { return a.dir }

// OpenConfigDir abre a pasta de dados no Explorer.
func (a *App) OpenConfigDir() error { return exec.Command("explorer", a.dir).Start() }

// Version é a versão do aplicativo.
func (a *App) Version() string { return version }
