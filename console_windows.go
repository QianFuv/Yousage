package main

import (
	"golang.org/x/sys/windows"
	"os"
)

func attachCommandConsole() {
	windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole").Call(uintptr(^uint32(0)))
	for _, stream := range []struct {
		kind        uint32
		name        string
		destination **os.File
	}{{windows.STD_OUTPUT_HANDLE, "stdout", &os.Stdout}, {windows.STD_ERROR_HANDLE, "stderr", &os.Stderr}} {
		handle, err := windows.GetStdHandle(stream.kind)
		if err == nil && handle != 0 && handle != windows.InvalidHandle {
			*stream.destination = os.NewFile(uintptr(handle), stream.name)
		}
	}
}
