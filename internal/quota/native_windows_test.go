package quota

import (
	"golang.org/x/sys/windows"
	"testing"
)

func assertProcessTreeStopped(test *testing.T, pids []int) {
	test.Helper()
	for _, pid := range pids {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if err == windows.ERROR_INVALID_PARAMETER {
			continue
		}
		if err != nil {
			test.Fatal(err)
		}
		state, err := windows.WaitForSingleObject(handle, 2000)
		windows.CloseHandle(handle)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			test.Fatal("managed process still running", pid, state, err)
		}
	}
}
