//go:build darwin || linux

package theme

import (
	"errors"
	"os"
	"syscall"
)

func openRegularFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
func replaceFile(source, destination string) error { return os.Rename(source, destination) }
func tryLockFile(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
func unlockFile(file *os.File) { syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }
