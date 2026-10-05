package remote

import (
	"context"
	"encoding/json"
	"errors"
	"yousage/internal/quota"
)

// ConsumeResetCredit delegates to the owning environment and retains ambiguous write outcomes.
func (client *Client) ConsumeResetCredit(ctx context.Context, input map[string]string) (quota.ResetResult, error) {
	if input["instanceId"] == "" && (input["sourceId"] == "" || input["accountId"] == "" || input["creditId"] == "") {
		return quota.ResetResult{}, errors.New("missing reset target")
	}
	data, err := client.Call(ctx, "provider.consumeResetCredit", input, true)
	if err != nil {
		var failure *CallError
		if errors.As(err, &failure) && failure.IsOutcomeUnknown {
			return quota.ResetResult{Outcome: "unknown", Warning: "The environment reset outcome is unknown."}, nil
		}
		return quota.ResetResult{}, err
	}
	var result quota.ResetResult
	if json.Unmarshal(data, &result) != nil {
		return quota.ResetResult{Outcome: "unknown", Warning: "The environment returned an unexpected reset response."}, nil
	}
	switch result.Outcome {
	case "reset", "nothingToReset", "noCredit", "alreadyRedeemed":
		return result, nil
	default:
		return quota.ResetResult{Outcome: "unknown", Warning: "The environment returned an unexpected reset response."}, nil
	}
}
