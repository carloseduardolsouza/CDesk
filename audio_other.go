//go:build !windows

package main

const audioRate = 24000

func audioCapture(stop <-chan struct{}, out func(pcm []byte)) error { return errNoWin }
