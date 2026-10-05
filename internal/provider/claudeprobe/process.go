package claudeprobe

import "yousage/internal/managedprocess"

func startProcess(options Options) (process, error) {
	managed, err := managedprocess.Start(managedprocess.Options{Executable: options.Executable, Arguments: launchArguments(options), Directory: options.Directory, Environment: launchEnvironment(options)})
	if err != nil {
		return process{}, err
	}
	return process{input: managed.Input, output: managed.Output, errors: managed.Errors, stop: managed.Stop, wait: managed.Wait}, nil
}
