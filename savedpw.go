package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
)

// Senhas lembradas dos computadores que o usuário conecta (opção "Lembrar senha"). Ficam em
// saved.json, protegidas com a criptografia do Windows (DPAPI): só este usuário, neste PC, abre.

func (a *App) savedFile() string { return filepath.Join(a.dir, "saved.json") }

func (a *App) loadSaved() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(a.savedFile()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func (a *App) writeSaved(m map[string]string) error {
	b, _ := json.Marshal(m)
	return os.WriteFile(a.savedFile(), b, 0o600)
}

func (a *App) savedPassword(id string) string {
	a.savedMu.Lock()
	defer a.savedMu.Unlock()
	enc, err := base64.StdEncoding.DecodeString(a.loadSaved()[id])
	if err != nil || len(enc) == 0 {
		return ""
	}
	pw, err := unprotect(enc)
	if err != nil {
		return ""
	}
	return string(pw)
}

// SavePassword lembra a senha do computador id para as próximas conexões.
func (a *App) SavePassword(id, pw string) error {
	id = onlyDigits(id)
	enc, err := protect([]byte(pw))
	if err != nil {
		return err
	}
	a.savedMu.Lock()
	defer a.savedMu.Unlock()
	m := a.loadSaved()
	m[id] = base64.StdEncoding.EncodeToString(enc)
	return a.writeSaved(m)
}

// ForgetPassword esquece a senha lembrada do computador id.
func (a *App) ForgetPassword(id string) error {
	a.savedMu.Lock()
	defer a.savedMu.Unlock()
	m := a.loadSaved()
	delete(m, onlyDigits(id))
	return a.writeSaved(m)
}

// SavedPasswords lista os IDs com senha lembrada (o frontend mostra um cadeado neles).
func (a *App) SavedPasswords() []string {
	a.savedMu.Lock()
	defer a.savedMu.Unlock()
	ids := []string{}
	for id := range a.loadSaved() {
		ids = append(ids, id)
	}
	return ids
}
