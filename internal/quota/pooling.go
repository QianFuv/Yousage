package quota

import (
	"math"
	"sort"
	"strings"
	"time"
)

type RedeemRoute struct {
	EnvironmentId string `json:"environmentId"`
	InstanceId    string `json:"instanceId,omitempty"`
	SourceId      string `json:"sourceId,omitempty"`
	AccountId     string `json:"accountId,omitempty"`
	CreditId      string `json:"creditId,omitempty"`
}
type Candidate struct {
	Driver           string `json:"driver"`
	EnvironmentId    string `json:"environmentId"`
	EnvironmentLabel string `json:"environmentLabel"`
	InstanceId       string `json:"instanceId,omitempty"`
	SourceId         string `json:"sourceId,omitempty"`
	SourceLabel      string `json:"sourceLabel,omitempty"`
	AccountId        string `json:"accountId,omitempty"`
	DisplayName      string `json:"displayName,omitempty"`
	Account
}
type PooledAccount struct {
	Key          string       `json:"key"`
	Driver       string       `json:"driver"`
	DisplayName  string       `json:"displayName,omitempty"`
	Email        string       `json:"email,omitempty"`
	Plan         string       `json:"plan,omitempty"`
	Environments []string     `json:"environments"`
	SourceLabel  string       `json:"sourceLabel,omitempty"`
	Redeem       *RedeemRoute `json:"redeem"`
	Limits       Limits       `json:"limits"`
}

// MergeAccounts counts a subscription once, independently selecting fresh windows, credits and hub redemption.
func MergeAccounts(candidates []Candidate) []PooledAccount {
	accounts := []PooledAccount{}
	positions := map[string]int{}
	creditsAt, hubsAt := map[string]string{}, map[string]string{}
	for _, candidate := range candidates {
		if candidate.Limits.Unavailable != nil || len(candidate.Limits.Windows) == 0 {
			continue
		}
		key := candidate.Driver + ":" + strings.ToLower(strings.TrimSpace(candidate.Email))
		if strings.TrimSpace(candidate.Email) == "" {
			if candidate.Limits.CredentialFingerprint != "" {
				key = candidate.Driver + ":credential:" + candidate.Limits.CredentialFingerprint
			} else if candidate.SourceId != "" {
				key = candidate.SourceId + ":" + candidate.AccountId
			} else {
				key = candidate.EnvironmentId + ":" + candidate.InstanceId
			}
		}
		route := &RedeemRoute{EnvironmentId: candidate.EnvironmentId, InstanceId: candidate.InstanceId}
		if candidate.SourceId != "" {
			route = nil
			if credit := candidate.Limits.ResetCredits; credit != nil && credit.NextCreditId != "" {
				route = &RedeemRoute{EnvironmentId: candidate.EnvironmentId, SourceId: candidate.SourceId, AccountId: candidate.AccountId, CreditId: credit.NextCreditId}
			}
		}
		position, exists := positions[key]
		if !exists {
			position = len(accounts)
			positions[key] = position
			accounts = append(accounts, PooledAccount{Key: key, Driver: candidate.Driver, Email: candidate.Email, Plan: candidate.Plan, DisplayName: candidate.DisplayName, Environments: []string{}, SourceLabel: candidate.SourceLabel, Redeem: route, Limits: candidate.Limits})
		}
		account := &accounts[position]
		if candidate.SourceId == "" {
			if !contains(account.Environments, candidate.EnvironmentId) {
				account.Environments = append(account.Environments, candidate.EnvironmentId)
			}
			account.SourceLabel = ""
			if account.DisplayName == "" {
				account.DisplayName = candidate.DisplayName
			}
		}
		if account.Plan == "" {
			account.Plan = candidate.Plan
		}
		previousCredits := account.Limits.ResetCredits
		if isFresher(candidate.Limits.CheckedAt, account.Limits.CheckedAt) {
			account.Limits = candidate.Limits
			account.Limits.ResetCredits = previousCredits
			if hubsAt[key] == "" && route != nil {
				account.Redeem = route
			}
		}
		if candidate.Limits.ResetCredits != nil && isFresher(candidate.Limits.CheckedAt, creditsAt[key]) {
			creditsAt[key] = candidate.Limits.CheckedAt
			account.Limits.ResetCredits = candidate.Limits.ResetCredits
			if hubsAt[key] == "" {
				account.Redeem = route
			}
		}
		if route != nil && route.SourceId != "" && isFresher(candidate.Limits.CheckedAt, hubsAt[key]) {
			hubsAt[key] = candidate.Limits.CheckedAt
			account.Redeem = route
		}
	}
	return accounts
}

func isFresher(next, previous string) bool {
	nextAt, nextError := time.Parse(time.RFC3339Nano, next)
	previousAt, previousError := time.Parse(time.RFC3339Nano, previous)
	return nextError == nil && (previousError != nil || nextAt.After(previousAt))
}
func contains(values []string, value string) bool {
	for _, entry := range values {
		if entry == value {
			return true
		}
	}
	return false
}

type PoolColumn struct {
	AccountKey string  `json:"accountKey"`
	Window     *Window `json:"window"`
}
type PoolReset struct {
	AccountKey      string `json:"accountKey"`
	At              string `json:"at"`
	RestoresPercent int    `json:"restoresPercent"`
}
type PoolWindow struct {
	Id               string       `json:"id"`
	Kind             string       `json:"kind"`
	Label            string       `json:"label"`
	UsedPercent      int          `json:"usedPercent"`
	RemainingPercent int          `json:"remainingPercent"`
	Pace             string       `json:"pace,omitempty"`
	Columns          []PoolColumn `json:"columns"`
	Resets           []PoolReset  `json:"resets"`
}
type Pool struct {
	Driver   string          `json:"driver"`
	Accounts []PooledAccount `json:"accounts"`
	Windows  []PoolWindow    `json:"windows"`
}

// CollectPools uses equal account shares, stable sparse columns and each window's own reset clock.
func CollectPools(accounts []PooledAccount, now time.Time) []Pool {
	result := []Pool{}
	positions := map[string]int{}
	order := map[string]int{"session": 0, "weekly": 1, "monthly": 2, "other": 3}
	for _, account := range accounts {
		position, exists := positions[account.Driver]
		if !exists {
			position = len(result)
			positions[account.Driver] = position
			result = append(result, Pool{Driver: account.Driver, Accounts: []PooledAccount{}, Windows: []PoolWindow{}})
		}
		result[position].Accounts = append(result[position].Accounts, account)
	}
	for poolIndex := range result {
		pool := &result[poolIndex]
		all := []Window{}
		for _, account := range pool.Accounts {
			all = append(all, account.Limits.Windows...)
		}
		sort.SliceStable(all, func(first, second int) bool { return order[all[first].Kind] < order[all[second].Kind] })
		if len(all) == 0 {
			continue
		}
		orderWindow := all[0]
		orderReset := func(account PooledAccount) string {
			for _, window := range account.Limits.Windows {
				if window.Id == orderWindow.Id && window.Kind == orderWindow.Kind && iso(window.ResetsAt) != "" {
					return iso(window.ResetsAt)
				}
			}
			return "~"
		}
		sort.SliceStable(pool.Accounts, func(first, second int) bool {
			left, right := pool.Accounts[first], pool.Accounts[second]
			if orderReset(left) != orderReset(right) {
				return orderReset(left) < orderReset(right)
			}
			leftName, rightName := accountSortName(left), accountSortName(right)
			if leftName != rightName {
				return leftName < rightName
			}
			return left.Key < right.Key
		})
		keys := []string{}
		byKey := map[string]Window{}
		for _, account := range pool.Accounts {
			for _, window := range account.Limits.Windows {
				key := window.Kind + ":" + window.Id
				if _, exists := byKey[key]; !exists {
					keys = append(keys, key)
					byKey[key] = window
				}
			}
		}
		sort.SliceStable(keys, func(first, second int) bool { return order[byKey[keys[first]].Kind] < order[byKey[keys[second]].Kind] })
		for _, key := range keys {
			first := byKey[key]
			window := PoolWindow{Id: first.Id, Kind: first.Kind, Label: first.Label, Columns: []PoolColumn{}, Resets: []PoolReset{}}
			used, memberCount, timedUsed, elapsed, timedCount := 0.0, 0, 0.0, 0.0, 0
			for _, account := range pool.Accounts {
				var selected *Window
				for _, candidate := range account.Limits.Windows {
					if candidate.Kind+":"+candidate.Id == key {
						copy := candidate
						selected = &copy
						break
					}
				}
				window.Columns = append(window.Columns, PoolColumn{account.Key, selected})
				if selected == nil {
					continue
				}
				memberCount++
				used += selected.UsedPercent
				reset, err := time.Parse(time.RFC3339Nano, selected.ResetsAt)
				if err == nil {
					window.Resets = append(window.Resets, PoolReset{account.Key, checked(reset), int(math.Floor(selected.UsedPercent + 0.5))})
					if selected.WindowDurationMins != nil && *selected.WindowDurationMins > 0 {
						length := time.Duration(*selected.WindowDurationMins) * time.Minute
						elapsed += max(0, min(1, float64(length-reset.Sub(now))/float64(length)))
						timedUsed += selected.UsedPercent
						timedCount++
					}
				}
			}
			window.UsedPercent = int(math.Floor(used/float64(memberCount) + 0.5))
			window.RemainingPercent = int(math.Floor(100 - used/float64(memberCount) + 0.5))
			for resetIndex := range window.Resets {
				for _, column := range window.Columns {
					if column.AccountKey == window.Resets[resetIndex].AccountKey {
						window.Resets[resetIndex].RestoresPercent = int(math.Floor(column.Window.UsedPercent/float64(memberCount) + 0.5))
						break
					}
				}
			}
			sort.SliceStable(window.Resets, func(first, second int) bool { return window.Resets[first].At < window.Resets[second].At })
			if timedCount > 0 {
				difference := timedUsed/float64(timedCount) - 100*elapsed/float64(timedCount)
				window.Pace = "on"
				if difference > 5 {
					window.Pace = "ahead"
				}
				if difference < -5 {
					window.Pace = "under"
				}
			}
			pool.Windows = append(pool.Windows, window)
		}
	}
	return result
}

func accountSortName(account PooledAccount) string {
	if account.DisplayName != "" {
		return strings.ToLower(account.DisplayName)
	}
	if account.Email != "" {
		return strings.ToLower(account.Email)
	}
	return strings.ToLower(account.Key)
}
