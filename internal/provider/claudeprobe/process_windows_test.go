package claudeprobe

import (
	"testing"

	"golang.org/x/sys/windows"
)

func processExitWaiters(test *testing.T, pids []int) []func() {
	var waiters []func()
	for _, pid := range pids {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if err == windows.ERROR_INVALID_PARAMETER {
			continue
		}
		if err != nil {
			test.Fatal(err)
		}
		waiters = append(waiters, func() {
			defer windows.CloseHandle(handle)
			state, err := windows.WaitForSingleObject(handle, 2000)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				test.Errorf("managed process %d remains alive: %v state=%d", pid, err, state)
			}
		})
	}
	return waiters
}
