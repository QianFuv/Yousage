package managedprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunChild(test *testing.T) {
	scenario := os.Getenv("YOUSAGE_MANAGED_TEST_CHILD")
	if scenario == "" {
		return
	}
	switch scenario {
	case "echo":
		input, _ := io.ReadAll(os.Stdin)
		_, _ = os.Stdout.Write(input)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 1024*1024))
		time.Sleep(time.Hour)
	case "hang":
		time.Sleep(time.Hour)
	case "failure":
		fmt.Fprint(os.Stderr, "sensitive error text")
		os.Exit(1)
	}
	os.Exit(0)
}
func childOptions(test *testing.T, scenario string) Options {
	test.Helper()
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	return Options{Executable: executable, Arguments: []string{"-test.run=TestRunChild"}, Environment: append(os.Environ(), "YOUSAGE_MANAGED_TEST_CHILD="+scenario)}
}
func TestRunBoundedStdoutCancellationAndRedactedFailure(test *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := Run(ctx, childOptions(test, "echo"), []byte("owned fixture input"), 1024)
	if err != nil || string(output) != "owned fixture input" {
		test.Fatal(string(output), err)
	}
	if _, err = Run(ctx, childOptions(test, "large"), nil, 1024); err == nil || !strings.Contains(err.Error(), "exceeded limit") {
		test.Fatal(err)
	}
	if _, err = Run(ctx, childOptions(test, "failure"), nil, 1024); err == nil || strings.Contains(err.Error(), "sensitive") {
		test.Fatal(err)
	}
	deadline, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	if _, err = Run(deadline, childOptions(test, "hang"), nil, 1024); !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal(err)
	}
}
