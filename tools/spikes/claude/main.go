// Command claude is a synthetic control-protocol fixture, never a model client.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// main serves synthetic control responses selected solely by fixture environment variables.
func main() {
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "--hold-") {
		recordProcess()
		if os.Args[1] == "--hold-child" {
			spawnChild("--hold-grandchild")
		}
		time.Sleep(5 * time.Minute)
		return
	}
	recordProcess()
	for _, argument := range os.Args[1:] {
		if argument == "--print" || argument == "-p" || argument == "--allowedTools" {
			os.Exit(20)
		}
	}
	if os.Getenv("ENABLE_CLAUDEAI_MCP_SERVERS") != "false" || os.Getenv("CLAUDE_CODE_AUTO_CONNECT_IDE") != "0" || os.Getenv("NODE_OPTIONS") != "" {
		os.Exit(21)
	}
	scenario := os.Getenv("YOUSAGE_FIXTURE_SCENARIO")
	secret := os.Getenv("YOUSAGE_FIXTURE_SECRET")
	if scenario == "early-exit" {
		os.Exit(3)
	}
	if scenario == "children" || scenario == "parent-exit" {
		spawnChild("--hold-child")
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message struct {
			Type      string `json:"type"`
			RequestId string `json:"request_id"`
			Request   struct {
				Subtype       string `json:"subtype"`
				SkipBehaviors bool   `json:"skip_behaviors"`
			} `json:"request"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil || message.Type != "control_request" {
			os.Exit(22)
		}
		if path := os.Getenv("YOUSAGE_FIXTURE_REQUEST_FILE"); path != "" {
			appendFile(path, string(scanner.Bytes())+"\n")
		}
		if message.Request.Subtype == "initialize" {
			if scenario == "init-hang" || scenario == "children" {
				time.Sleep(5 * time.Minute)
			}
			if scenario == "init-error" {
				send(message.RequestId, nil, "authentication required "+secret, false)
				continue
			}
			if scenario == "init-protocol" {
				send(message.RequestId, []any{}, "", false)
				continue
			}
			if scenario == "large" {
				fmt.Println(strings.Repeat("x", 2*1024*1024))
				continue
			}
			if scenario == "noise" {
				fmt.Fprintln(os.Stderr, strings.Repeat("stderr", 20000))
				fmt.Println("startup banner")
				fmt.Println(`{"type":"keep_alive"}`)
				send("wrong-id", map[string]any{"wrong": true}, "", false)
			}
			send(message.RequestId, map[string]any{"account": map[string]any{"email": filepath.Base(os.Getenv("CLAUDE_CONFIG_DIR")) + "@example.invalid", "subscriptionType": "pro", "tokenSource": "oauth"}, "commands": []any{}, "agents": []any{}, "models": []any{}}, "", scenario == "fragmented")
			if scenario == "parent-exit" {
				return
			}
			continue
		}
		if message.Request.Subtype != "get_usage" {
			os.Exit(23)
		}
		if scenario == "usage-hang" || scenario == "fallback-hang" && !message.Request.SkipBehaviors {
			time.Sleep(5 * time.Minute)
		}
		if (scenario == "fallback" || scenario == "fallback-hang") && message.Request.SkipBehaviors {
			send(message.RequestId, nil, "unknown field skip_behaviors", false)
			continue
		}
		if scenario == "usage-error" || scenario == "unknown-method" {
			send(message.RequestId, nil, "unknown control method get_usage "+secret, false)
			continue
		}
		if scenario == "usage-auth" {
			send(message.RequestId, nil, "unauthorized "+secret, false)
			continue
		}
		if scenario == "usage-network-error" {
			send(message.RequestId, nil, "network unknown failure for get_usage "+secret, false)
			continue
		}
		if scenario == "usage-protocol" {
			fmt.Printf("{\"type\":\"control_response\",\"response\":{\"subtype\":\"invalid\",\"request_id\":%q,\"error\":%q}}\n", message.RequestId, secret)
			continue
		}
		available := scenario != "unsupported"
		var utilization any = 12
		if scenario == "invalid-window" {
			utilization = "not-a-number"
		}
		limits := map[string]any{"five_hour": map[string]any{"utilization": utilization, "resets_at": "2026-10-06T12:00:00Z"}, "seven_day": nil}
		if scenario == "model-scoped" {
			limits["model_scoped"] = []any{map[string]any{"display_name": "Fable 5.1", "utilization": 40, "resets_at": "2026-10-10T12:00:00Z"}}
		}
		if scenario == "missing-utilization" {
			limits["five_hour"] = map[string]any{"resets_at": "2026-10-06T12:00:00Z"}
		}
		if !available || scenario == "null-limits" {
			limits = nil
		}
		send(message.RequestId, map[string]any{"session": map[string]any{}, "subscription_type": "pro", "rate_limits_available": available, "rate_limits": limits, "behaviors": nil}, "", scenario == "fragmented")
	}
}

func send(identity string, body any, failure string, isFragmented bool) {
	subtype := "success"
	if failure != "" {
		subtype = "error"
	}
	data, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": subtype, "request_id": identity, "response": body, "error": failure}})
	if isFragmented {
		for _, value := range data {
			os.Stdout.Write([]byte{value})
		}
		fmt.Println()
		return
	}
	fmt.Println(string(data))
}

func recordProcess() {
	if path := os.Getenv("YOUSAGE_FIXTURE_PID_FILE"); path != "" {
		appendFile(path, fmt.Sprintf("%d\n", os.Getpid()))
	}
}

func appendFile(path, text string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(24)
	}
	defer file.Close()
	file.WriteString(text)
}

func spawnChild(argument string) {
	executable, err := os.Executable()
	if err != nil {
		os.Exit(25)
	}
	child := exec.Command(executable, argument)
	child.Env = os.Environ()
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if child.Start() != nil {
		os.Exit(26)
	}
}
