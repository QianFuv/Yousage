// Package managedprocess owns native children and their complete descendant tree.
package managedprocess

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Options struct {
	Executable  string
	Arguments   []string
	Environment []string
	Directory   string
}
type Process struct {
	Input  io.WriteCloser
	Output io.ReadCloser
	Errors io.ReadCloser
	Stop   func()
	Wait   func() error
}

// Start launches an absolute native executable with deterministic descendant cleanup.
func Start(options Options) (Process, error) {
	if !filepath.IsAbs(options.Executable) {
		return Process{}, errors.New("native executable must be an absolute path")
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	return start(options)
}

// Run captures bounded stdout, drains stderr and closes the whole tree on exit or cancellation.
func Run(ctx context.Context, options Options, input []byte, outputLimit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if outputLimit <= 0 {
		return nil, errors.New("stdout limit must be positive")
	}
	managed, err := Start(options)
	if err != nil {
		return nil, err
	}
	var workers sync.WaitGroup
	workers.Add(3)
	type output struct {
		bytes []byte
		err   error
	}
	stdout := make(chan output, 1)
	waited := make(chan error, 1)
	go func() {
		defer workers.Done()
		bytes, err := io.ReadAll(io.LimitReader(managed.Output, outputLimit+1))
		if int64(len(bytes)) > outputLimit {
			err = errors.New("native stdout exceeded limit")
		}
		stdout <- output{bytes, err}
	}()
	go func() { defer workers.Done(); _, _ = io.Copy(io.Discard, managed.Errors) }()
	go func() { defer workers.Done(); _, _ = managed.Input.Write(input); _ = managed.Input.Close() }()
	go func() { waited <- managed.Wait() }()
	defer func() {
		managed.Stop()
		_ = managed.Wait()
		_ = managed.Input.Close()
		_ = managed.Output.Close()
		_ = managed.Errors.Close()
		workers.Wait()
	}()
	var value output
	select {
	case value = <-stdout:
		if value.err != nil {
			return nil, value.err
		}
		select {
		case err = <-waited:
			if err != nil {
				return nil, errors.New("native helper failed")
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case err = <-waited:
		managed.Stop()
		if err != nil {
			return nil, errors.New("native helper failed")
		}
		select {
		case value = <-stdout:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if value.err != nil {
		return nil, value.err
	}
	return value.bytes, nil
}
