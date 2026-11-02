package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func blobBytes(b windows.DataBlob) []byte {
	out := make([]byte, b.Size)
	copy(out, unsafe.Slice(b.Data, b.Size))
	windows.LocalFree(windows.Handle(unsafe.Pointer(b.Data)))
	return out
}

func protect(b []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	return blobBytes(out), nil
}

func unprotect(b []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	return blobBytes(out), nil
}
