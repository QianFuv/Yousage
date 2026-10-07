package service

import (
	"context"
	"reflect"
	"testing"
	"yousage/internal/accounting"
)

// TestSavedRatesReuseAndInvalidate avoids repeated price decoding without hiding updates.
func TestSavedRatesReuseAndInvalidate(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	value := RateSnapshot{Rates: accounting.RateTable{"first": {TokenRates: accounting.TokenRates{Input: 1}}}}
	revision, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, value)
	if err != nil {
		test.Fatal(err)
	}
	first, err := service.Rates(ctx)
	if err != nil {
		test.Fatal(err)
	}
	for range 5 {
		warm, err := service.Rates(ctx)
		if err != nil || reflect.ValueOf(warm.Rates).Pointer() != reflect.ValueOf(first.Rates).Pointer() {
			test.Fatal("unchanged prices were decoded again", err)
		}
	}
	value.Rates = accounting.RateTable{"second": {TokenRates: accounting.TokenRates{Input: 2}}}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", revision, value); err != nil {
		test.Fatal(err)
	}
	updated, err := service.Rates(ctx)
	if err != nil || updated.Rates["second"].Input != 2 || len(updated.Rates) != 1 {
		test.Fatal("price revision was ignored", err)
	}
	if first.Rates["first"].Input != 1 {
		test.Fatal("updating prices mutated a previous snapshot")
	}
}
