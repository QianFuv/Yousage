//go:build darwin || linux

package claudeprobe

import (
	"syscall"
	"testing"
	"time"
)

func processExitWaiters(test *testing.T, pids []int) []func() {
	var waiters []func()
	for _, pid := range pids {
		waiters = append(waiters, func() {
			deadline := time.Now().Add(2 * time.Second)
			for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if syscall.Kill(pid, 0) == nil {
				test.Errorf("managed process %d remains alive", pid)
			}
		})
	}
	return waiters
}
