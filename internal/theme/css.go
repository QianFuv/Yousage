package theme

import (
	_ "embed"
	"errors"
	"github.com/dop251/goja"
	"sync"
)

//go:embed vendor/culori-4.0.2.js
var cssParserSource string

const cssAdapter = `
const convertYousageColor = culori.converter('oklch');
function parseYousageColor(input) {
  const parsed = culori.parse(input);
  if (!parsed) return null;
  const color = convertYousageColor(parsed);
  const values = [color.l ?? 0, color.c ?? 0, color.h ?? 0,
    /\/\s*none\s*\)$/i.test(input) ? 0 : (color.alpha ?? 1)];
  if (!values.every(Number.isFinite)) return null;
  return [Math.min(1, Math.max(0, values[0])), Math.max(0, values[1]),
    values[2], Math.min(1, Math.max(0, values[3]))];
}`

var cssState struct {
	sync.Mutex
	once    sync.Once
	runtime *goja.Runtime
	parse   goja.Callable
	err     error
}

func parseCssColor(input string) (oklch, float64, error) {
	if len(input) > 256*1024 {
		return oklch{}, 0, errors.New("color literal is too long")
	}
	cssState.once.Do(func() {
		cssState.runtime = goja.New()
		_, cssState.err = cssState.runtime.RunString(cssParserSource + cssAdapter)
		if cssState.err == nil {
			var ok bool
			cssState.parse, ok = goja.AssertFunction(cssState.runtime.Get("parseYousageColor"))
			if !ok {
				cssState.err = errors.New("CSS parser is unavailable")
			}
		}
	})
	if cssState.err != nil {
		return oklch{}, 0, errors.New("CSS parser is unavailable")
	}
	cssState.Lock()
	defer cssState.Unlock()
	value, err := cssState.parse(goja.Undefined(), cssState.runtime.ToValue(input))
	if err != nil || goja.IsNull(value) || goja.IsUndefined(value) {
		return oklch{}, 0, errors.New("invalid CSS color literal")
	}
	var components []float64
	if cssState.runtime.ExportTo(value, &components) != nil || len(components) != 4 {
		return oklch{}, 0, errors.New("invalid CSS color literal")
	}
	return oklch{components[0], components[1], components[2]}, components[3], nil
}
