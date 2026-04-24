//go:build !windows

package main

import (
	"bytes"
	"os"
	"os/exec"
)

// playWAV plays raw WAV bytes asynchronously.
// It tries aplay (Linux/ALSA) first, then writes a temp file for afplay (macOS).
func playWAV(wav []byte) {
	go func() {
		cmd := exec.Command("aplay", "-q", "-")
		cmd.Stdin = bytes.NewReader(wav)
		if cmd.Run() == nil {
			return
		}
		f, err := os.CreateTemp("", "ghgh-*.wav")
		if err != nil {
			return
		}
		name := f.Name()
		defer os.Remove(name)
		if _, err := f.Write(wav); err != nil {
			f.Close()
			return
		}
		f.Close()
		exec.Command("afplay", name).Run() //nolint:errcheck
	}()
}
