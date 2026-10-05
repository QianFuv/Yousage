// Command theme verifies isolated headless operations against a built native GUI executable.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"yousage/internal/theme"
)

func main() {
	app := flag.String("app", "", "native application path")
	flag.Parse()
	if *app == "" {
		panic("--app is required")
	}
	executable, err := filepath.Abs(*app)
	if err != nil {
		panic(err)
	}
	directory, err := os.MkdirTemp("", "yousage-theme-cli-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(directory)
	run := func(arguments ...string) []byte {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		arguments = append(arguments, "--state-dir", directory)
		command := exec.CommandContext(ctx, executable, arguments...)
		output, err := command.CombinedOutput()
		if err != nil {
			panic(fmt.Sprintf("CLI failure: %v %s", err, output))
		}
		return output
	}
	show := func() theme.Snapshot {
		var snapshot theme.Snapshot
		if err := json.Unmarshal(run("theme", "show"), &snapshot); err != nil {
			panic(err)
		}
		return snapshot
	}
	run("theme", "set", "ocean")
	first := show()
	run("theme", "set", "ocean")
	second := show()
	if first.Generation == second.Generation || second.DefaultTheme != "ocean" {
		panic("same-id generation did not change")
	}
	fixture := filepath.Join(directory, "headless.json")
	if err := os.WriteFile(fixture, []byte(`{"name":"Headless fixture","appearance":"dark","canvas":"#172554","accent":"#f97316"}`), 0600); err != nil {
		panic(err)
	}
	run("theme", "set", fixture)
	snapshot := show()
	if len(snapshot.Themes) != 1 || len(snapshot.Themes[0].Colors) != 57 || snapshot.DefaultTheme != "headless" {
		panic("published palette did not resolve")
	}
	cssFixture := filepath.Join(directory, "css-literal.json")
	if err := os.WriteFile(cssFixture, []byte(`{"version":1,"name":"CSS literal","appearance":"dark","colors":{"canvas":"rebeccapurple","accent":"color(display-p3 1 .2 .3 / .5)"}}`), 0600); err != nil {
		panic(err)
	}
	run("theme", "set", cssFixture)
	cssSnapshot := show()
	if len(cssSnapshot.Themes) != 2 || cssSnapshot.DefaultTheme != "css-literal" || cssSnapshot.Themes[0].Colors["canvas"] != "oklch(0.440272 0.160296 303.373)" {
		panic("full CSS literal or redirected native output contract failed")
	}
	before, err := os.ReadFile(filepath.Join(directory, "themes", "headless.json"))
	if err != nil {
		panic(err)
	}
	run("theme", "clear")
	snapshot = show()
	after, err := os.ReadFile(filepath.Join(directory, "themes", "headless.json"))
	if err != nil {
		panic(err)
	}
	if snapshot.DefaultTheme != "" || snapshot.Generation != "" || !bytes.Equal(before, after) {
		panic("clear removed files or retained published default")
	}
	fmt.Println("Native headless set / same-id set / file publishing / show / clear passed; 57 roles preserved; isolated state removed.")
}
