package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCredentialHelperRejectsUnknownAndUnsafeInputs(t *testing.T) {
	for _, input := range []string{`{"operation":"unknown","value":"secret"}`, `{"operation":"get","id":"../outside"}`, `{"operation":"cursor"}`, `invalid`} {
		var output bytes.Buffer
		if RunHelper(strings.NewReader(input), &output) != 0 || strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "outside") {
			t.Fatal(output.String())
		}
		var response helperResponse
		if json.Unmarshal(output.Bytes(), &response) != nil || response.Error == "" {
			t.Fatal(output.String())
		}
	}
}

func TestPackagedHeadlessHelperUsesInheritedPipesWithoutStartingGui(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native packaging qualification first")
	}
	executable := filepath.Join(t.TempDir(), "helper.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-ldflags=-H windowsgui", "-o", executable, "../..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	store := &OwnedStore{executable: executable}
	id := "helper-fixture-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() { _ = store.native.Delete(context.Background(), id) })
	if _, err := store.helper(ctx, helperRequest{Operation: "put", Id: id, Value: "owned-helper-fixture"}); err != nil {
		t.Fatal(err)
	}
	value, err := store.helper(ctx, helperRequest{Operation: "get", Id: id})
	if err != nil || value != "owned-helper-fixture" {
		t.Fatal("helper roundtrip", err)
	}
	if _, err = store.helper(ctx, helperRequest{Operation: "delete", Id: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.helper(ctx, helperRequest{Operation: "get", Id: id}); err != ErrMissing {
		t.Fatal(err)
	}
}
