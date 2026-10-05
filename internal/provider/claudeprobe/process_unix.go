//go:build darwin || linux

package claudeprobe

import (
	"os/exec"
	"syscall"
)

func startProcess(options Options) (process, error) {
	command := exec.Command(options.Executable, launchArguments(options)...)
	command.Env = launchEnvironment(options)
	command.Dir = options.Directory
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := command.StdinPipe()
	if err != nil {
		return process{}, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return process{}, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		input.Close()
		output.Close()
		return process{}, err
	}
	if err = command.Start(); err != nil {
		input.Close()
		output.Close()
		stderr.Close()
		return process{}, err
	}
	return process{input: input, output: output, errors: stderr, stop: func() { syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }, wait: command.Wait}, nil
}
