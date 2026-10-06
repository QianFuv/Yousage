package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

// ratesUrl is the models.dev catalog, which also prices models served through gateways and other providers.
const ratesUrl = "https://models.dev/api.json"

type RateSnapshot struct {
	Rates       accounting.RateTable `json:"rates"`
	FetchedAtMs int64                `json:"fetchedAtMs"`
	Source      string               `json:"source"`
	IsFresh     bool                 `json:"-"`
}

func (snapshot RateSnapshot) Provenance(now time.Time) remote.Pricing {
	value := remote.Pricing{Status: "unavailable", Source: ratesUrl, KnownModels: len(snapshot.Rates)}
	if len(snapshot.Rates) > 0 {
		value.Status = "cached"
		if snapshot.IsFresh && now.Sub(time.UnixMilli(snapshot.FetchedAtMs)) < 24*time.Hour {
			value.Status = "fresh"
		}
		instant := time.UnixMilli(snapshot.FetchedAtMs).UTC().Format(time.RFC3339Nano)
		value.FetchedAt = &instant
	}
	return value
}
func (service *Service) Rates(ctx context.Context) (RateSnapshot, error) {
	if current := service.rates.Snapshot("public"); current.HasValue {
		value := current.Value
		value.IsFresh = current.Error == nil
		return value, nil
	}
	value := RateSnapshot{Rates: accounting.RateTable{}}
	_, err := service.Store.ReadSetting(ctx, "public-model-rates", &value)
	if err != nil {
		return RateSnapshot{Rates: accounting.RateTable{}}, nil
	}
	return value, nil
}

// RefreshRates honors persisted freshness after restart and retains the previous rates on failure.
func (service *Service) RefreshRates(ctx context.Context, request refresh.Request) error {
	current, err := service.Rates(ctx)
	if err != nil {
		return err
	}
	age := service.Now().Sub(time.UnixMilli(current.FetchedAtMs))
	if len(current.Rates) > 0 && current.Source == ratesUrl && ((!request.IsManual && age < 24*time.Hour) || (request.IsManual && age < time.Minute)) {
		return nil
	}
	_, err = service.rates.Run(ctx, "public", request, func(owner context.Context) (RateSnapshot, error) {
		deadline, cancel := context.WithTimeout(owner, 10*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(deadline, http.MethodGet, ratesUrl, nil)
		if err != nil {
			return RateSnapshot{}, err
		}
		client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("price redirects are not allowed") }}
		if service.RateClient != nil {
			client = *service.RateClient
		}
		response, err := client.Do(request)
		if err != nil {
			return RateSnapshot{}, errors.New("Public model prices could not be refreshed. Cached prices are retained.")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return RateSnapshot{}, errors.New("Public model prices are unavailable.")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024+1))
		if err != nil || len(data) > 16*1024*1024 {
			return RateSnapshot{}, errors.New("Public model price document could not be read.")
		}
		rates, err := accounting.ParseModelsDevCatalog(data)
		if err != nil || len(rates) == 0 {
			return RateSnapshot{}, errors.New("Public model price document is invalid.")
		}
		value := RateSnapshot{Rates: rates, FetchedAtMs: service.Now().UnixMilli(), Source: ratesUrl, IsFresh: true}
		var previous RateSnapshot
		revision, _ := service.Store.ReadSetting(owner, "public-model-rates", &previous)
		_, _ = service.Store.WriteSetting(owner, "public-model-rates", revision, value)
		service.emit("usage-invalidated", nil)
		return value, nil
	})
	if errors.Is(err, refresh.ErrSkipped) {
		return nil
	}
	return err
}
