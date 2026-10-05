// Vivid palette derivation is adapted from t3code 51f7f77 (MIT).
package theme

import (
	"errors"
	"math"
)

// Colors retains every product role, including roles absent from this application's screens.
type Colors map[string]string

// Generate derives the pinned 57-role palette from canvas and accent seeds.
func Generate(appearance, backgroundValue, accentValue string) (Colors, error) {
	if appearance != "light" && appearance != "dark" {
		return nil, errors.New("invalid theme appearance")
	}
	fallback := rgb{250, 245, 250}
	if appearance == "dark" {
		fallback = rgb{24, 15, 27}
	}
	canvasRgb := parseRgb(backgroundValue, fallback)
	accentRgb := parseRgb(accentValue, rgb{168, 67, 112})
	canvas, accent := rgbToOklch(canvasRgb), rgbToOklch(accentRgb)
	isDark := luminance(canvasRgb) < 0.179
	choose := func(darkValue, lightValue float64) float64 {
		if isDark {
			return darkValue
		}
		return lightValue
	}
	hue := accent.hue
	if accent.chroma < 0.02 {
		hue = canvas.hue
	}
	tintChroma := clamp(accent.chroma*0.22, 0.008, 0.045)
	step := choose(1, -1)
	surfaceAt := func(delta, chroma float64) oklch {
		return oklch{clamp(canvas.lightness+step*delta, 0.05, 0.98), chroma, hue}
	}
	textBase := oklch{choose(0.95, 0.2), math.Min(0.035, accent.chroma*0.25), hue}
	text := solveLightness(textBase, canvasRgb, 7, isDark)
	textRgb := oklchToRgb(text)
	textMutedRgb := mutedText(canvasRgb, textRgb)
	action := oklch{clamp(accent.lightness+choose(0.06, -0.02), 0.35, 0.85), math.Max(accent.chroma*0.9, 0.06), math.Mod(hue+50, 360)}
	actionRgb := oklchToRgb(action)
	sidebar := surfaceAt(0.045, tintChroma*1.4)
	sidebarRgb := oklchToRgb(sidebar)
	surface := surfaceAt(0.015, tintChroma)
	surfaceRaised := surfaceAt(0.05, tintChroma)
	surfaceRaisedRgb := oklchToRgb(surfaceRaised)
	surfaceOverlay := surfaceAt(0.075, tintChroma)
	border := surfaceAt(choose(0.16, 0.12), math.Min(0.07, accent.chroma*0.35))
	input := surfaceAt(choose(0.21, 0.16), math.Min(0.08, accent.chroma*0.4))
	secondary := surfaceAt(choose(0.1, 0.06), math.Min(0.09, accent.chroma*0.5))
	muted := surfaceAt(choose(0.06, 0.04), math.Min(0.06, accent.chroma*0.35))
	accentSurface := surfaceAt(choose(0.13, 0.08), math.Min(0.11, accent.chroma*0.55))
	messageSurface := surfaceAt(choose(0.16, 0.1), math.Min(0.13, accent.chroma*0.6))
	codeBackground := surfaceAt(0.035, tintChroma*0.8)
	updateSurface := surfaceAt(choose(0.14, 0.09), math.Min(0.12, accent.chroma*0.55))
	foregroundOn := func(surface rgb) string {
		return perceptualColor(solveLightness(textBase, surface, 4.6, isDark))
	}
	actionHover := action
	actionHover.lightness += choose(0.06, -0.06)
	colors := Colors{
		"canvas": rgbColor(canvasRgb), "chrome": rgbColor(canvasRgb), "toolbar": rgbColor(canvasRgb),
		"toolbarForeground":        rgbColor(textRgb),
		"toolbarBorder":            perceptualColor(surfaceAt(choose(0.14, 0.1), math.Min(0.08, accent.chroma*0.4))),
		"toolbarControl":           perceptualColor(surfaceAt(choose(0.09, 0.05), tintChroma*1.3)),
		"toolbarControlForeground": rgbColor(textRgb),
		"toolbarControlHover":      perceptualColor(surfaceAt(choose(0.14, 0.09), tintChroma*1.6)),
		"surface":                  perceptualColor(surface), "surfaceRaised": perceptualColor(surfaceRaised), "surfaceOverlay": perceptualColor(surfaceOverlay),
		"text": rgbColor(textRgb), "textMuted": rgbColor(textMutedRgb), "border": perceptualColor(border), "input": perceptualColor(input),
		"focus": rgbColor(accentRgb), "accent": rgbColor(accentRgb), "accentForeground": rgbColor(readableForeground(accentRgb)),
		"secondary": perceptualColor(secondary), "secondaryForeground": foregroundOn(oklchToRgb(secondary)),
		"muted": perceptualColor(muted), "mutedForeground": rgbColor(readableText(oklchToRgb(muted), textRgb, 1, 4.6)),
		"placeholder":    rgbColor(readableText(surfaceRaisedRgb, textRgb, 1, 4.6)),
		"secondaryLabel": rgbColor(textMutedRgb), "iconMuted": rgbColor(textMutedRgb),
		"update": rgbColor(accentRgb), "updateForeground": foregroundOn(oklchToRgb(updateSurface)), "updateSurface": perceptualColor(updateSurface),
		"accentSurface": perceptualColor(accentSurface), "accentSurfaceForeground": foregroundOn(oklchToRgb(accentSurface)),
		"messageSurface": perceptualColor(messageSurface), "messageForeground": foregroundOn(oklchToRgb(messageSurface)),
		"messageAction": rgbColor(actionRgb), "messageActionForeground": rgbColor(readableForeground(actionRgb)), "messageActionHover": perceptualColor(actionHover),
		"codeBackground": perceptualColor(codeBackground), "codeForeground": rgbColor(textRgb),
		"sidebar": perceptualColor(sidebar), "sidebarForeground": foregroundOn(sidebarRgb), "sidebarMutedForeground": rgbColor(mutedText(sidebarRgb, textRgb)),
		"sidebarControlSurface": perceptualColor(surfaceAt(choose(0.1, 0.07), tintChroma*1.5)),
		"sidebarRowHover":       perceptualColor(surfaceAt(choose(0.08, 0.06), math.Min(0.08, accent.chroma*0.45))),
		"sidebarRowActive":      perceptualColor(surfaceAt(choose(0.12, 0.09), math.Min(0.1, accent.chroma*0.55))),
		"sidebarRowSelected":    perceptualColor(surfaceAt(choose(0.14, 0.1), math.Min(0.11, accent.chroma*0.6))),
		"sidebarBorder":         perceptualColor(surfaceAt(choose(0.17, 0.12), math.Min(0.08, accent.chroma*0.4))),
		"terminalBackground":    rgbColor(canvasRgb), "terminalForeground": rgbColor(textRgb), "terminalCursor": rgbColor(accentRgb),
		"terminalSelection":      perceptualColor(surfaceAt(choose(0.18, 0.12), math.Min(0.12, accent.chroma*0.55))),
		"terminalScrollbar":      perceptualColor(surfaceAt(choose(0.22, 0.16), tintChroma)),
		"terminalScrollbarHover": perceptualColor(surfaceAt(choose(0.3, 0.22), tintChroma)),
	}
	errorColor, errorForeground := "#fb2c36", "#c10007"
	warningColor, warningForeground := "#fe9a00", "#bb4d00"
	if isDark {
		errorColor, errorForeground, warningForeground = "#fb414a", "#ff6467", "#ffb900"
	}
	for _, status := range []struct{ role, signal, foreground string }{{"error", errorColor, errorForeground}, {"warning", warningColor, warningForeground}} {
		surfaceRgb := mixRgb(canvasRgb, parseRgb(status.signal, canvasRgb), choose(0.16, 0.08))
		colors[status.role], _ = CanonicalColor(status.signal)
		colors[status.role+"Surface"] = rgbColor(surfaceRgb)
		colors[status.role+"Foreground"] = perceptualColor(solveLightness(rgbToOklch(parseRgb(status.foreground, canvasRgb)), surfaceRgb, 4.6, isDark))
	}
	return colors, nil
}
