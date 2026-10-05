package history

import (
	"path/filepath"
	"yousage/internal/domain"
)

// PhysicalFingerprint uses the same host, canonical path and filesystem identity for all readers.
func PhysicalFingerprint(host string, provider domain.Provider, path string) domain.SourceFingerprint {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return domain.SourceFingerprint{HostId: host, Provider: provider, ResolvedHomePath: path, VolumeId: directoryVolumeId(path)}
}
