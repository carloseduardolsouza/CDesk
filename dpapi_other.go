//go:build !windows

package main

// shortcut: fora do Windows a senha lembrada fica sem criptografia (só o arquivo 0600); usar o
// chaveiro do sistema quando houver suporte a Linux/macOS.
func protect(b []byte) ([]byte, error)   { return b, nil }
func unprotect(b []byte) ([]byte, error) { return b, nil }
