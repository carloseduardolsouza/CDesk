package main

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// StartupEnabled diz se o CDesk inicia junto com o Windows.
func (a *App) StartupEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("CDesk")
	return err == nil
}

// SetStartup liga ou desliga o início junto com o Windows (só para este usuário).
func (a *App) SetStartup(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("CDesk"); err != nil && err != syscall.ERROR_FILE_NOT_FOUND {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("CDesk", `"`+exe+`" --hidden`)
}

var messageBeep = user32.NewProc("MessageBeep")

// beep toca o som de aviso do Windows.
func beep() { messageBeep.Call(0x40) } // MB_ICONASTERISK
