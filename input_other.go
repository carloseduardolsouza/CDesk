//go:build !windows

package main

import "image"

// shortcut: injeção de input só no Windows, implementar Linux/macOS depois.
func inject(Event, image.Rectangle) {}

func releaseKey(int) {}
