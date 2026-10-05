// Package theme implements native palette generation and version-one theme publishing.
// Palette derivation is adapted from t3code 51f7f77 (MIT); see THIRD_PARTY_NOTICES.md.
package theme

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type rgb struct{ red, green, blue float64 }
type oklch struct{ lightness, chroma, hue float64 }

func clamp(value, low, high float64) float64 { return math.Min(high, math.Max(low, value)) }

func linearChannel(value float64) float64 {
	value /= 255
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

func srgbChannel(value float64) float64 {
	if value <= 0.0031308 {
		value *= 12.92
	} else {
		value = 1.055*math.Pow(value, 1/2.4) - 0.055
	}
	return math.Floor(clamp(value, 0, 1)*255 + 0.5)
}

func rgbToOklch(color rgb) oklch {
	red, green, blue := linearChannel(color.red), linearChannel(color.green), linearChannel(color.blue)
	long := math.Cbrt(0.4122214708*red + 0.5363325363*green + 0.0514459929*blue)
	medium := math.Cbrt(0.2119034982*red + 0.6806995451*green + 0.1073969566*blue)
	short := math.Cbrt(0.0883024619*red + 0.2817188376*green + 0.6299787005*blue)
	lightness := 0.2104542553*long + 0.793617785*medium - 0.0040720468*short
	axisA := 1.9779984951*long - 2.428592205*medium + 0.4505937099*short
	axisB := 0.0259040371*long + 0.7827717662*medium - 0.808675766*short
	return oklch{lightness, math.Hypot(axisA, axisB), math.Atan2(axisB, axisA) * 180 / math.Pi}
}

func canonicalRgbToOklch(color rgb) oklch {
	red, green, blue := linearChannel(color.red), linearChannel(color.green), linearChannel(color.blue)
	long := math.Cbrt(0.412221469470763*red + 0.5363325372617348*green + 0.0514459932675022*blue)
	medium := math.Cbrt(0.2119034958178252*red + 0.6806995506452344*green + 0.1073969535369406*blue)
	short := math.Cbrt(0.0883024591900564*red + 0.2817188391361215*green + 0.6299787016738222*blue)
	lightness := 0.210454268309314*long + 0.7936177747023054*medium - 0.0040720430116193*short
	axisA := 1.9779985324311684*long - 2.4285922420485799*medium + 0.450593709617411*short
	axisB := 0.0259040424655478*long + 0.7827717124575296*medium - 0.8086757549230774*short
	if color.red == color.green && color.green == color.blue {
		axisA, axisB = 0, 0
	}
	return oklch{lightness, math.Hypot(axisA, axisB), math.Atan2(axisB, axisA) * 180 / math.Pi}
}

func unclampedRgb(color oklch) rgb {
	hue := color.hue * math.Pi / 180
	axisA, axisB := color.chroma*math.Cos(hue), color.chroma*math.Sin(hue)
	long := math.Pow(color.lightness+0.3963377774*axisA+0.2158037573*axisB, 3)
	medium := math.Pow(color.lightness-0.1055613458*axisA-0.0638541728*axisB, 3)
	short := math.Pow(color.lightness-0.0894841775*axisA-1.291485548*axisB, 3)
	return rgb{4.0767416621*long - 3.3077115913*medium + 0.2309699292*short, -1.2684380046*long + 2.6097574011*medium - 0.3413193965*short, -0.0041960863*long - 0.7034186147*medium + 1.707614701*short}
}

func mapGamut(color oklch) oklch {
	isInGamut := func(chroma float64) bool {
		candidate := color
		candidate.chroma = chroma
		linear := unclampedRgb(candidate)
		return linear.red >= -0.0001 && linear.red <= 1.0001 && linear.green >= -0.0001 && linear.green <= 1.0001 && linear.blue >= -0.0001 && linear.blue <= 1.0001
	}
	if isInGamut(color.chroma) {
		return color
	}
	low, high := 0.0, color.chroma
	steps := int(math.Max(1, math.Ceil(math.Log2(math.Max(color.chroma, 0.000001))-math.Log2(0.000001))))
	for range steps {
		mid := (low + high) / 2
		if isInGamut(mid) {
			low = mid
		} else {
			high = mid
		}
	}
	color.chroma = low
	return color
}

func oklchToRgb(color oklch) rgb {
	linear := unclampedRgb(mapGamut(color))
	return rgb{srgbChannel(linear.red), srgbChannel(linear.green), srgbChannel(linear.blue)}
}

func formatNumber(value float64, precision int) string {
	if math.Abs(value) < math.Pow10(-precision)/2 {
		value = 0
	}
	return strings.TrimSuffix(strings.TrimRight(strconv.FormatFloat(value, 'f', precision, 64), "0"), ".")
}

func formatColor(color oklch, alpha float64) string {
	hue := 0.0
	if color.chroma >= 0.0000005 {
		hue = math.Mod(math.Mod(color.hue, 360)+360, 360)
	}
	body := formatNumber(color.lightness, 6) + " " + formatNumber(color.chroma, 6) + " " + formatNumber(hue, 3)
	if alpha < 1 {
		body += " / " + formatNumber(alpha, 4)
	}
	return "oklch(" + body + ")"
}

func rgbColor(color rgb) string          { return formatColor(rgbToOklch(color), 1) }
func perceptualColor(color oklch) string { return formatColor(mapGamut(color), 1) }

// CanonicalColor converts literal CSS colors through the pinned Culori parser.
func CanonicalColor(value string) (string, error) {
	color, alpha, err := parseCssColor(value)
	if err != nil {
		return "", err
	}
	return formatColor(color, alpha), nil
}

func parseColor(value string) (oklch, float64, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, "#") {
		hex := strings.TrimPrefix(value, "#")
		if len(hex) == 3 || len(hex) == 4 {
			var expanded strings.Builder
			for _, digit := range hex {
				expanded.WriteRune(digit)
				expanded.WriteRune(digit)
			}
			hex = expanded.String()
		}
		if len(hex) != 6 && len(hex) != 8 {
			return oklch{}, 0, errors.New("invalid hex color")
		}
		channels := []float64{0, 0, 0, 255}
		for index := 0; index < len(hex)/2; index++ {
			channel, err := strconv.ParseUint(hex[index*2:index*2+2], 16, 8)
			if err != nil {
				return oklch{}, 0, err
			}
			channels[index] = float64(channel)
		}
		return canonicalRgbToOklch(rgb{channels[0], channels[1], channels[2]}), channels[3] / 255, nil
	}
	if strings.HasPrefix(value, "oklch(") && strings.HasSuffix(value, ")") {
		body := strings.TrimSuffix(strings.TrimPrefix(value, "oklch("), ")")
		colorBody, alphaBody, hasAlpha := strings.Cut(body, "/")
		fields := strings.Fields(colorBody)
		if len(fields) != 3 {
			return oklch{}, 0, errors.New("invalid OKLCH channels")
		}
		components := []float64{0, 0, 0, 1}
		if hasAlpha {
			fields = append(fields, strings.TrimSpace(alphaBody))
		}
		for index, field := range fields {
			if field == "none" {
				components[index] = 0
				continue
			}
			isPercent := strings.HasSuffix(field, "%")
			field = strings.TrimSuffix(field, "%")
			if index == 2 {
				field = strings.TrimSuffix(field, "deg")
			}
			number, err := strconv.ParseFloat(field, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || isPercent && index == 2 {
				return oklch{}, 0, errors.New("invalid finite color channel")
			}
			if isPercent {
				number /= 100
				if index == 1 {
					number *= 0.4
				}
			}
			components[index] = number
		}
		return oklch{clamp(components[0], 0, 1), math.Max(0, components[1]), components[2]}, clamp(components[3], 0, 1), nil
	}
	return oklch{}, 0, fmt.Errorf("unsupported preflight color literal %q", value)
}

func parseRgb(value string, fallback rgb) rgb {
	color, _, err := parseColor(value)
	if err != nil {
		return fallback
	}
	return oklchToRgb(color)
}

func mixRgb(base, overlay rgb, amount float64) rgb {
	return rgb{base.red + (overlay.red-base.red)*amount, base.green + (overlay.green-base.green)*amount, base.blue + (overlay.blue-base.blue)*amount}
}

func luminance(color rgb) float64 {
	linearize := func(channel float64) float64 {
		channel /= 255
		if channel <= 0.03928 {
			return channel / 12.92
		}
		return math.Pow((channel+0.055)/1.055, 2.4)
	}
	return 0.2126*linearize(color.red) + 0.7152*linearize(color.green) + 0.0722*linearize(color.blue)
}

func contrast(first, second rgb) float64 {
	firstValue, secondValue := luminance(first), luminance(second)
	return (math.Max(firstValue, secondValue) + 0.05) / (math.Min(firstValue, secondValue) + 0.05)
}

func solveLightness(base oklch, against rgb, minimum float64, isLighter bool) oklch {
	low, high := 0.0, base.lightness
	if isLighter {
		low, high = base.lightness, 1
	}
	if contrast(oklchToRgb(base), against) >= minimum {
		return base
	}
	for range 18 {
		mid := (low + high) / 2
		candidate := base
		candidate.lightness = mid
		if (contrast(oklchToRgb(candidate), against) >= minimum) == isLighter {
			high = mid
		} else {
			low = mid
		}
	}
	base.lightness = low
	if isLighter {
		base.lightness = high
	}
	return base
}

func readableForeground(background rgb) rgb {
	light, dark := rgb{255, 250, 255}, rgb{36, 21, 35}
	lightContrast, darkContrast := contrast(background, light), contrast(background, dark)
	if math.Max(lightContrast, darkContrast) >= 4.5 {
		if lightContrast >= darkContrast {
			return light
		}
		return dark
	}
	if contrast(background, rgb{255, 255, 255}) >= contrast(background, rgb{}) {
		return rgb{255, 255, 255}
	}
	return rgb{}
}

func readableText(background, foreground rgb, amount, minimum float64) rgb {
	softened := mixRgb(foreground, background, amount)
	if contrast(softened, background) >= minimum {
		return softened
	}
	readable, low, high := foreground, 0.0, amount
	for range 12 {
		mid := (low + high) / 2
		candidate := mixRgb(foreground, background, mid)
		if contrast(candidate, background) >= minimum {
			readable, low = candidate, mid
		} else {
			high = mid
		}
	}
	return readable
}

func mutedText(background, foreground rgb) rgb {
	minimum := 4.705
	if luminance(background) < 0.179 {
		minimum = 5.082
	}
	return readableText(background, foreground, 1, minimum)
}
