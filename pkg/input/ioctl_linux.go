package input

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// File.Fd puts the descriptor in blocking mode, and Close no longer wakes a blocked Read.
func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno unix.Errno
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
