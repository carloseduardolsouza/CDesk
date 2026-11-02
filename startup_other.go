//go:build !windows

package main

import "fmt"

// shortcut: iniciar com o sistema e som só no Windows.
func (a *App) StartupEnabled() bool { return false }

func (a *App) SetStartup(bool) error { return fmt.Errorf("Disponível só no Windows.") }

func beep() {}
