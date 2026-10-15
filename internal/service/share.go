package service

import (
	"context"
	"rsc.io/qr"
	"yousage/internal/accounting"
)

// QrModules encodes text or a URL using the receipt's binary row representation.
func QrModules(text string) ([]string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return nil, err
	}
	modules := make([]string, code.Size)
	for row := range code.Size {
		pixels := make([]byte, code.Size)
		for column := range code.Size {
			pixels[column] = '0'
			if code.Black(column, row) {
				pixels[column] = '1'
			}
		}
		modules[row] = string(pixels)
	}
	return modules, nil
}

// ReceiptData returns the local receipt and its identically bounded priced buckets.
func (service *Service) ReceiptData(ctx context.Context, days int, timeZone string) (Receipt, []accounting.Bucket, error) {
	receipt, err := service.Receipt(ctx, days, timeZone)
	if err != nil {
		return receipt, nil, err
	}
	window := receipt.Window
	window.SinceTimeMs, window.UntilTimeMs = receipt.StartsAtMs, receipt.EndsAtMs
	summary, err := service.localSummary(ctx, window)
	return receipt, summary.Buckets, err
}
