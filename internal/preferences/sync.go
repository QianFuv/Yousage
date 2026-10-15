package preferences

import "context"

// SyncValue validates a sync record without coupling preferences to the sync engine.
type SyncValue interface{ Validate() error }

// Sync reads the versioned non-secret sync record into the supplied settings.
func (service *Service) Sync(ctx context.Context, settings any) (int64, error) {
	return service.repository.ReadSetting(ctx, "sync", settings)
}

// SaveSync validates and atomically replaces the non-secret sync record.
func (service *Service) SaveSync(ctx context.Context, settings SyncValue, revision int64) (int64, error) {
	if err := settings.Validate(); err != nil {
		return revision, err
	}
	return service.repository.WriteSetting(ctx, "sync", revision, settings)
}
