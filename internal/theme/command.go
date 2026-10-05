package theme

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RunCommand handles theme subcommands before any native application is constructed.
func RunCommand(ctx context.Context, arguments []string, output, diagnostics io.Writer) (bool, int) {
	if len(arguments) == 0 || arguments[0] != "theme" {
		return false, 0
	}
	if len(arguments) < 2 {
		fmt.Fprintln(diagnostics, "Usage: yousage theme set <id-or-file> | clear | show [--state-dir <directory>] [--id <id>]")
		return true, 2
	}
	operation := arguments[1]
	var target, explicitId, directory string
	var failure error
	for index := 2; index < len(arguments); index++ {
		argument := arguments[index]
		if strings.HasPrefix(argument, "--") {
			key, value, hasValue := strings.Cut(argument, "=")
			if key != "--state-dir" && key != "--id" {
				failure = fmt.Errorf("unknown flag %s", key)
				break
			}
			if !hasValue {
				index++
				if index == len(arguments) {
					failure = fmt.Errorf("missing value for %s", key)
					break
				}
				value = arguments[index]
			}
			if value == "" {
				failure = errors.New("empty flag value")
				break
			}
			if key == "--state-dir" {
				directory = value
			} else {
				explicitId = value
			}
		} else if target == "" {
			target = argument
		} else {
			failure = errors.New("too many targets")
			break
		}
	}
	if failure == nil && directory == "" {
		var config string
		config, failure = os.UserConfigDir()
		directory = filepath.Join(config, "Yousage")
	}
	if failure == nil {
		directory, failure = filepath.Abs(directory)
	}
	if failure == nil {
		store := Store{Directory: directory}
		switch operation {
		case "set":
			if target == "" {
				failure = errors.New("set requires an id or theme file")
				break
			}
			var identity string
			identity, failure = store.Set(ctx, target, explicitId)
			if failure == nil {
				_, failure = fmt.Fprintf(output, "Theme set to %q.\n", identity)
			}
		case "clear":
			if target != "" || explicitId != "" {
				failure = errors.New("clear accepts no target or id")
				break
			}
			failure = store.Clear(ctx)
			if failure == nil {
				_, failure = fmt.Fprintln(output, "Published default cleared; client selections are unchanged.")
			}
		case "show":
			if target != "" || explicitId != "" {
				failure = errors.New("show accepts no target or id")
				break
			}
			var snapshot Snapshot
			snapshot, failure = store.Show(ctx)
			if failure == nil {
				failure = json.NewEncoder(output).Encode(snapshot)
			}
		default:
			failure = fmt.Errorf("unknown theme command %q", operation)
		}
	}
	if failure != nil {
		fmt.Fprintln(diagnostics, failure)
		return true, 1
	}
	return true, 0
}
