// Package secrets keeps credentials outside application statistics and diagnostics.
package secrets

import (
	"context"
	"sync"
	"time"
)

type tokenResult struct {
	token string
	err   error
}
type pendingToken struct {
	done   chan struct{}
	result tokenResult
}
type CachedReader struct {
	mutex      sync.Mutex
	owner      context.Context
	cancel     context.CancelFunc
	read       func(context.Context) (string, error)
	now        func() time.Time
	cacheUntil time.Time
	token      string
	pending    *pendingToken
	workers    sync.WaitGroup
}

// NewCachedReader shares a native credential prompt; caller timeouts do not start another prompt.
func NewCachedReader(read func(context.Context) (string, error)) *CachedReader {
	owner, cancel := context.WithCancel(context.Background())
	return &CachedReader{owner: owner, cancel: cancel, read: read, now: time.Now}
}

func (reader *CachedReader) Read(ctx context.Context) (string, error) {
	reader.mutex.Lock()
	if reader.owner.Err() != nil {
		reader.mutex.Unlock()
		return "", context.Canceled
	}
	if reader.token != "" && reader.now().Before(reader.cacheUntil) {
		token := reader.token
		reader.mutex.Unlock()
		return token, nil
	}
	if reader.pending == nil {
		pending := &pendingToken{done: make(chan struct{})}
		reader.pending = pending
		reader.workers.Add(1)
		go func() {
			defer reader.workers.Done()
			token, err := reader.read(reader.owner)
			reader.mutex.Lock()
			pending.result = tokenResult{token, err}
			if err == nil && token != "" && reader.owner.Err() == nil {
				reader.token = token
				reader.cacheUntil = reader.now().Add(5 * time.Minute)
			}
			reader.pending = nil
			close(pending.done)
			reader.mutex.Unlock()
		}()
	}
	pending := reader.pending
	reader.mutex.Unlock()
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case <-pending.done:
		return pending.result.token, pending.result.err
	case <-wait.Done():
		return "", wait.Err()
	}
}

// Close cancels the owned native read and waits for credential workers before shutdown.
func (reader *CachedReader) Close() {
	reader.mutex.Lock()
	reader.cancel()
	reader.token = ""
	reader.mutex.Unlock()
	reader.workers.Wait()
}
