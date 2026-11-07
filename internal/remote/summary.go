package remote

import (
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

type Source struct {
	Fingerprint      domain.SourceFingerprint `json:"fingerprint"`
	Status           string                   `json:"status"`
	ScannedFiles     int                      `json:"scannedFiles"`
	SkippedFiles     int                      `json:"skippedFiles"`
	MalformedRecords int                      `json:"malformedRecords"`
	DistinctSessions int                      `json:"distinctSessions"`
	Message          *string                  `json:"message"`
}
type Pricing struct {
	Status      string  `json:"status"`
	Source      string  `json:"source"`
	FetchedAt   *string `json:"fetchedAt"`
	KnownModels int     `json:"knownModels"`
}
type Summary struct {
	ContractVersion int                 `json:"contractVersion"`
	ReadAt          string              `json:"readAt"`
	TimeZone        string              `json:"timeZone"`
	SinceDay        string              `json:"sinceDay"`
	UntilDay        string              `json:"untilDay"`
	Buckets         []accounting.Bucket `json:"buckets"`
	Sources         []Source            `json:"sources"`
	Pricing         Pricing             `json:"pricing"`
	ScanDurationMs  int64               `json:"scanDurationMs"`
}
