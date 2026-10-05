//go:build windows

package history

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"strconv"
)

func directoryVolumeId(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) != nil {
		return ""
	}
	return fmt.Sprintf("%d:%s", info.VolumeSerialNumber, strconv.FormatFloat(float64(uint64(info.FileIndexHigh)<<32|uint64(info.FileIndexLow)), 'f', -1, 64))
}
