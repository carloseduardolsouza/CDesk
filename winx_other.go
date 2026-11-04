//go:build !windows

package main

import (
	"errors"
	"image"
)

// shortcut: os recursos do computador controlado (winx_windows.go) só existem no Windows.

var onEmergency func()

var errNoWin = errors.New("Disponível só no Windows.")

func cursorPos() (int, int, bool)             { return 0, 0, false }
func foregroundRect() (image.Rectangle, bool) { return image.Rectangle{}, false }
func mouseMoveRel(int, int)                   {}
func rescueWindows([]image.Rectangle)         {}
func setResolution(int, int) error            { return errNoWin }
func blockInput(bool) error                   { return errNoWin }
func privacyMode(bool) error                  { return errNoWin }
func wallpaper() string                       { return "" }
func setWallpaper(string)                     {}
func clipSeq() uint32                         { return 0 }
func clipFiles() []string                     { return nil }
func setClipFiles([]string) error             { return errNoWin }
