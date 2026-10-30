// This executable emits owned account/limit fixtures and never sends model turns.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func recordPid() {
	file, err := os.OpenFile(os.Getenv("YOUSAGE_PID_FILE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		fmt.Fprintln(file, os.Getpid())
		file.Close()
	}
}
func child(argument string) {
	executable, _ := os.Executable()
	command := exec.Command(executable, argument)
	command.Env = os.Environ()
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	_ = command.Start()
}

// main serves configurable native quota responses without sending model turns.
func main() {
	recordPid()
	if len(os.Args) > 1 && os.Args[1] == "grandchild" {
		time.Sleep(time.Hour)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "child" {
		child("grandchild")
		time.Sleep(time.Hour)
		return
	}
	child("child")
	for attempt := 0; attempt < 100; attempt++ {
		data, _ := os.ReadFile(os.Getenv("YOUSAGE_PID_FILE"))
		if len(strings.Fields(string(data))) >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	scenario := os.Getenv("YOUSAGE_CODEX_SCENARIO")
	resetAt := int64(1800000000)
	if configured, err := strconv.ParseInt(os.Getenv("YOUSAGE_CODEX_RESET_AT"), 10, 64); err == nil {
		resetAt = configured
	}
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	quotaRequests := 0
	for scanner.Scan() {
		file, _ := os.OpenFile(os.Getenv("YOUSAGE_REQUEST_FILE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if file != nil {
			fmt.Fprintln(file, scanner.Text())
			file.Close()
		}
		var request map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &request)
		id := request["id"]
		method := request["method"]
		if id == nil {
			continue
		}
		var result any
		switch method {
		case "initialize":
			result = map[string]string{"userAgent": "owned-fixture"}
		case "account/read":
			kind := "chatgpt"
			if scenario == "api-key" {
				kind = "apiKey"
			}
			result = map[string]any{"account": map[string]string{"type": kind, "email": "fixture@example.invalid", "planType": "plus"}}
		case "account/rateLimits/read":
			quotaRequests++
			if scenario == "quota-hang" || (scenario == "rpc-then-hang" && quotaRequests > 1) {
				time.Sleep(time.Hour)
				return
			}
			if scenario == "rpc-error" || ((scenario == "rpc-recover" || scenario == "rpc-then-hang") && quotaRequests == 1) {
				_ = encoder.Encode(map[string]any{"id": id, "error": map[string]any{"code": -32603, "message": "internal server error secret fixture@example.invalid https://user:secret@example.invalid C:/private/account"}})
				continue
			}
			_ = encoder.Encode(map[string]any{"id": 9999, "result": map[string]bool{"unmatched": true}})
			_ = encoder.Encode(map[string]any{"method": "account/rateLimits/updated", "params": map[string]any{"rateLimits": map[string]any{"limitId": "spark", "primary": map[string]any{"usedPercent": 99}}}})
			result = map[string]any{"rateLimits": map[string]any{"limitId": "spark", "primary": map[string]any{"usedPercent": 90}}, "rateLimitsByLimitId": map[string]any{"codex": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": 25, "resetsAt": resetAt}}}, "rateLimitResetCredits": map[string]any{"availableCount": 2, "credits": []any{map[string]any{"status": "available", "expiresAt": 1800000300}}}}
			result.(map[string]any)["accountId"] = os.Getenv("YOUSAGE_CODEX_WORKSPACE")
		case "account/rateLimitResetCredit/consume":
			if scenario == "reset-hang" {
				time.Sleep(time.Hour)
				return
			}
			result = map[string]string{"outcome": "reset"}
			if scenario == "reset-invalid" {
				result = map[string]string{"outcome": "unexpected"}
			}
		default:
			_ = encoder.Encode(map[string]any{"id": id, "error": map[string]any{"code": -32601}})
			continue
		}
		data, _ := json.Marshal(map[string]any{"id": id, "result": result})
		cut := len(data) / 2
		_, _ = os.Stdout.Write(data[:cut])
		time.Sleep(time.Millisecond)
		_, _ = os.Stdout.Write(append(data[cut:], '\n'))
	}
}
