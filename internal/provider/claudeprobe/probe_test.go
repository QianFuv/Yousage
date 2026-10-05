package claudeprobe

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var fixtureExecutable string

func TestMain(testRunner *testing.M) {
	directory, err := os.MkdirTemp("", "yousage-claude-test-")
	if err != nil {
		panic(err)
	}
	fixtureExecutable = filepath.Join(directory, "claude-fixture")
	if runtime.GOOS == "windows" {
		fixtureExecutable += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	command := exec.CommandContext(ctx, "go", "build", "-o", fixtureExecutable, "../../../tools/spikes/claude")
	output, err := command.CombinedOutput()
	cancel()
	if err != nil {
		panic(string(output) + err.Error())
	}
	exitCode := testRunner.Run()
	os.RemoveAll(directory)
	os.Exit(exitCode)
}

func fixtureOptions(test *testing.T, scenario string) Options {
	directory := test.TempDir()
	return Options{Executable: fixtureExecutable, ConfigDirectory: directory, Directory: directory, InitializeLimit: time.Second, UsageLimit: 200 * time.Millisecond, Environment: []string{"YOUSAGE_FIXTURE_SCENARIO=" + scenario, "YOUSAGE_FIXTURE_REQUEST_FILE=" + filepath.Join(directory, "requests.jsonl"), "YOUSAGE_FIXTURE_PID_FILE=" + filepath.Join(directory, "pids"), "NODE_OPTIONS=must-be-removed", "FORCE_CODE_TERMINAL=must-be-removed"}}
}

func TestControlReadPreservesAccountAndPercentages(test *testing.T) {
	for _, scenario := range []string{"success", "fragmented", "noise", "fallback", "unsupported", "invalid-window", "usage-error", "usage-hang", "fallback-hang", "unknown-method", "parent-exit"} {
		test.Run(scenario, func(test *testing.T) {
			options := fixtureOptions(test, scenario)
			started := time.Now()
			result, err := Probe(context.Background(), options)
			if err != nil {
				test.Fatal(err)
			}
			if !strings.Contains(string(result.Initialization), filepath.Base(options.ConfigDirectory)+"@example.invalid") {
				test.Fatalf("account initialization lost or crossed homes: %s", result.Initialization)
			}
			expectedState := "available"
			switch scenario {
			case "unsupported":
				expectedState = "unsupported"
			case "invalid-window", "usage-error", "usage-hang", "fallback-hang", "unknown-method", "parent-exit":
				expectedState = "failed"
			}
			if result.State != expectedState {
				test.Fatalf("state %s, want %s: %s", result.State, expectedState, result.Failure)
			}
			if result.State == "available" && !strings.Contains(string(result.Usage), `"utilization":12`) {
				test.Fatalf("percentages changed: %s", result.Usage)
			}
			if scenario == "fallback-hang" && time.Since(started) > 700*time.Millisecond {
				test.Fatal("fallback restarted the usage deadline")
			}
			if scenario == "unknown-method" && result.FailureKind != "unsupported_method" {
				test.Fatal("CLI incompatibility lost its capability classification")
			}
			if scenario == "usage-hang" && result.FailureKind != "timeout" {
				test.Fatal("usage deadline lost its failure classification")
			}
			requests, err := os.ReadFile(filepath.Join(options.ConfigDirectory, "requests.jsonl"))
			if err != nil {
				test.Fatal(err)
			}
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(string(requests)), "\n") {
				var message map[string]any
				if json.Unmarshal([]byte(line), &message) != nil || message["type"] != "control_request" {
					test.Fatal("model prompt or invalid request emitted")
				}
				count++
			}
			if scenario == "fallback" || scenario == "fallback-hang" {
				if count != 3 {
					test.Fatalf("fallback count %d", count)
				}
			} else if count > 2 {
				test.Fatal("unrelated error incorrectly triggered fallback")
			}
			assertRecordedProcessesExit(test, options.ConfigDirectory)
		})
	}
}

func TestInitializationFailuresStayBounded(test *testing.T) {
	for _, scenario := range []string{"early-exit", "init-error", "large", "init-hang"} {
		test.Run(scenario, func(test *testing.T) {
			options := fixtureOptions(test, scenario)
			options.InitializeLimit = 150 * time.Millisecond
			started := time.Now()
			result, err := Probe(context.Background(), options)
			if err == nil || len(result.Initialization) != 0 {
				test.Fatalf("initialization failure accepted: %+v %v", result, err)
			}
			if scenario == "init-error" {
				var failure *Failure
				if !errors.As(err, &failure) || failure.Kind != "authentication" {
					test.Fatal("authentication failure lost its classification")
				}
			}
			if time.Since(started) > time.Second {
				test.Fatal("initialization failure exceeded deadline plus cleanup")
			}
			assertRecordedProcessesExit(test, options.ConfigDirectory)
		})
	}
}

func TestCancellationCleansImmediateDescendants(test *testing.T) {
	options := fixtureOptions(test, "children")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := Probe(ctx, options)
		finished <- err
	}()
	deadline := time.Now().Add(time.Second)
	for len(recordedPids(options.ConfigDirectory)) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	pids := recordedPids(options.ConfigDirectory)
	if len(pids) < 3 {
		test.Fatal("fixture did not create the immediate child and grandchild")
	}
	waiters := processExitWaiters(test, pids)
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			test.Fatal("cancelled initialization succeeded")
		}
	case <-time.After(time.Second):
		test.Fatal("cancellation did not reap root")
	}
	for _, wait := range waiters {
		wait()
	}
}

func TestConcurrentHomesRemainIndependent(test *testing.T) {
	var workers sync.WaitGroup
	for range 2 {
		options := fixtureOptions(test, "success")
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := Probe(context.Background(), options)
			if err != nil || !strings.Contains(string(result.Initialization), filepath.Base(options.ConfigDirectory)+"@example.invalid") {
				test.Errorf("home isolation failed: %s %v", result.Initialization, err)
			}
		}()
	}
	workers.Wait()
}

func TestExecutableValidationAndCancelledStart(test *testing.T) {
	if _, err := Probe(context.Background(), Options{Executable: "claude"}); err == nil {
		test.Fatal("implicit PATH resolution accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Probe(ctx, fixtureOptions(test, "success")); err == nil {
		test.Fatal("already-cancelled process started")
	}
	options := fixtureOptions(test, "success")
	options.Executable = filepath.Join(options.ConfigDirectory, "missing.exe")
	if _, err := Probe(context.Background(), options); err == nil {
		test.Fatal("missing executable accepted")
	}
}

func recordedPids(directory string) []int {
	data, _ := os.ReadFile(filepath.Join(directory, "pids"))
	var pids []int
	for _, value := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(value)
		if err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func assertRecordedProcessesExit(test *testing.T, directory string) {
	for _, wait := range processExitWaiters(test, recordedPids(directory)) {
		wait()
	}
}
