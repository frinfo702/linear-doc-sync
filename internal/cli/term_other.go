//go:build !darwin && !linux

package cli

import "errors"

func isTerminal(_ uintptr) bool {
	return false
}

func makeRaw(_ uintptr) (func(), error) {
	return func() {}, errors.New("raw terminal mode is not supported on this platform")
}

func terminalSize(_ uintptr) (width, height int) {
	return 80, 24
}
