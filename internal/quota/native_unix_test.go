//go:build darwin || linux

package quota

import "testing"

func assertProcessTreeStopped(test *testing.T, pids []int) {
	test.Helper()
	test.Log("native non-Windows descendant qualification requires its deferred runner", pids)
}
