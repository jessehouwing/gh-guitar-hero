//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

var (
	winmm     = syscall.NewLazyDLL("winmm.dll")
	playSound = winmm.NewProc("PlaySoundA")
)

const (
	sndSync      = 0x0000 // play synchronously (blocks until done)
	sndNoDefault = 0x0002 // no default sound if data is invalid
	sndMemory    = 0x0004 // pszSound points to WAV data in memory
)

// playWAV plays raw WAV bytes via winmm.dll PlaySound.
// SND_MEMORY lets Windows read directly from the in-process byte slice,
// so no temp file and no external process are needed.
func playWAV(wav []byte) {
	if len(wav) == 0 {
		return
	}
	go func() {
		playSound.Call( //nolint:errcheck
			uintptr(unsafe.Pointer(&wav[0])),
			0,
			uintptr(sndSync|sndMemory|sndNoDefault),
		)
		// Keep wav alive until PlaySound returns (SND_SYNC blocks, but be explicit).
		runtime.KeepAlive(wav)
	}()
}
