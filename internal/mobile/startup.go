package mobile

import "sync/atomic"

// startupClaim admits one process startup without waiting for its native event loop.
type startupClaim struct {
	hasStarted atomic.Bool
}

// tryClaim returns immediately, including while the first invocation is still running.
func (claim *startupClaim) tryClaim() bool {
	return claim.hasStarted.CompareAndSwap(false, true)
}
