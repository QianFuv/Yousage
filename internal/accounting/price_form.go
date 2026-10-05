package accounting

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"yousage/internal/domain"
)

var priceNumberPattern = regexp.MustCompile(`^(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$`)

// ParsePriceField preserves omitted optional cache rates and accepts finite scientific notation.
func ParsePriceField(text string, isRequired bool) (*float64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		if isRequired {
			return nil, errors.New("price is required")
		}
		return nil, nil
	}
	if !priceNumberPattern.MatchString(text) {
		return nil, errors.New("price must be a nonnegative decimal")
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || !domain.Finite(value) {
		return nil, errors.New("price exceeds numeric range")
	}
	return &value, nil
}
