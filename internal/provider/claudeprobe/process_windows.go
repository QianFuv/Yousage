package claudeprobe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const processAttributeJobList = uintptr(0x0002000d)

func startProcess(options Options) (managed process, failure error) {
	if !strings.EqualFold(filepath.Ext(options.Executable), ".exe") {
		return process{}, errors.New("Windows native probe requires an executable, not a shell shim")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return process{}, err
	}
	isStarted := false
	defer func() {
		if !isStarted {
			windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		return process{}, err
	}
	var pipes []*os.File
	for range 3 {
		reader, writer, err := os.Pipe()
		if err != nil {
			for _, file := range pipes {
				file.Close()
			}
			return process{}, err
		}
		pipes = append(pipes, reader, writer)
	}
	defer func() {
		for index, file := range pipes {
			if !isStarted || index == 0 || index == 3 || index == 5 {
				file.Close()
			}
		}
	}()
	childHandles := make([]windows.Handle, 3)
	defer func() {
		for _, handle := range childHandles {
			if handle != 0 {
				windows.CloseHandle(handle)
			}
		}
	}()
	for index, file := range []*os.File{pipes[0], pipes[3], pipes[5]} {
		if err = windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &childHandles[index], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return process{}, err
		}
	}
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return process{}, err
	}
	defer attributes.Delete()
	jobs := []windows.Handle{job}
	if err = attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&childHandles[0]), uintptr(len(childHandles))*unsafe.Sizeof(childHandles[0])); err != nil {
		return process{}, err
	}
	if err = attributes.Update(processAttributeJobList, unsafe.Pointer(&jobs[0]), unsafe.Sizeof(job)); err != nil {
		return process{}, err
	}
	startup := windows.StartupInfoEx{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW
	startup.ShowWindow = windows.SW_HIDE
	startup.StdInput, startup.StdOutput, startup.StdErr = childHandles[0], childHandles[1], childHandles[2]
	startup.ProcThreadAttributeList = attributes.List()
	application, err := windows.UTF16PtrFromString(options.Executable)
	if err != nil {
		return process{}, err
	}
	arguments := append([]string{options.Executable}, launchArguments(options)...)
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(arguments))
	if err != nil {
		return process{}, err
	}
	var directory *uint16
	if options.Directory != "" {
		directory, err = windows.UTF16PtrFromString(options.Directory)
		if err != nil {
			return process{}, err
		}
	}
	environment := launchEnvironment(options)
	sort.Strings(environment)
	block := utf16.Encode([]rune(strings.Join(environment, "\x00") + "\x00\x00"))
	information := windows.ProcessInformation{}
	err = windows.CreateProcess(application, commandLine, nil, nil, true, windows.CREATE_NO_WINDOW|windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT, &block[0], directory, &startup.StartupInfo, &information)
	runtime.KeepAlive(attributes)
	runtime.KeepAlive(jobs)
	runtime.KeepAlive(childHandles)
	if err != nil {
		return process{}, err
	}
	windows.CloseHandle(information.Thread)
	isStarted = true
	var stopOnce, waitOnce sync.Once
	var waitError error
	return process{
		input: pipes[1], output: pipes[2], errors: pipes[4],
		stop: func() {
			stopOnce.Do(func() {
				windows.TerminateJobObject(job, 1)
				windows.CloseHandle(job)
			})
		},
		wait: func() error {
			waitOnce.Do(func() {
				defer windows.CloseHandle(information.Process)
				_, waitError = windows.WaitForSingleObject(information.Process, windows.INFINITE)
				if waitError == nil {
					var exitCode uint32
					waitError = windows.GetExitCodeProcess(information.Process, &exitCode)
					if waitError == nil && exitCode != 0 {
						waitError = fmt.Errorf("control process exit status %d", exitCode)
					}
				}
			})
			return waitError
		},
	}, nil
}
