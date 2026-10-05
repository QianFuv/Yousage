//go:build darwin || linux

package managedprocess

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
)

func start(options Options) (Process, error) {
	command := exec.Command(options.Executable, options.Arguments...)
	command.Env = options.Environment
	command.Dir = options.Directory
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	childInput, input, err := os.Pipe()
	if err != nil {
		return Process{}, err
	}
	output, childOutput, err := os.Pipe()
	if err != nil {
		input.Close()
		childInput.Close()
		return Process{}, err
	}
	stderr, childError, err := os.Pipe()
	if err != nil {
		input.Close()
		childInput.Close()
		output.Close()
		childOutput.Close()
		return Process{}, err
	}
	command.Stdin, command.Stdout, command.Stderr = childInput, childOutput, childError
	err = command.Start()
	childInput.Close()
	childOutput.Close()
	childError.Close()
	if err != nil {
		input.Close()
		output.Close()
		stderr.Close()
		return Process{}, err
	}
	var stopOnce, waitOnce sync.Once
	var waitError error
	return Process{Input: input, Output: output, Errors: stderr, Stop: func() { stopOnce.Do(func() { syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }) }, Wait: func() error { waitOnce.Do(func() { waitError = command.Wait() }); return waitError }}, nil
}
