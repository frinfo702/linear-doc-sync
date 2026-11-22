//go:build linux

package cli

import (
	"syscall"
	"unsafe"
)

func isTerminal(fd uintptr) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&termios)),
	)
	return errno == 0
}

func makeRaw(fd uintptr) (func(), error) {
	var orig syscall.Termios
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&orig)),
	); errno != 0 {
		return nil, errno
	}

	raw := orig
	raw.Iflag &^= syscall.ICRNL | syscall.IXON
	raw.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TCSETS),
		uintptr(unsafe.Pointer(&raw)),
	); errno != 0 {
		return nil, errno
	}

	restore := func() {
		_, _, _ = syscall.Syscall(
			syscall.SYS_IOCTL,
			fd,
			uintptr(syscall.TCSETS),
			uintptr(unsafe.Pointer(&orig)),
		)
	}
	return restore, nil
}

func terminalSize(fd uintptr) (width, height int) {
	var ws struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 || ws.Col == 0 || ws.Row == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}
