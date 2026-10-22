package controllers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func parseOptionalPositive(query map[string][]string, key string, fallback, max int) (int, error) {
	values, present := query[key]
	if !present {
		return fallback, nil
	}
	if len(values) == 0 {
		return 0, fmt.Errorf("invalid")
	}
	raw := strings.TrimSpace(values[0])
	number := 0.0
	if raw != "" {
		parsed, ok := parseJSNumber(raw)
		if !ok {
			return 0, fmt.Errorf("invalid")
		}
		number = parsed
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 1 || number >= float64(int(^uint(0)>>1)) || (max > 0 && number > float64(max)) {
		return 0, fmt.Errorf("invalid")
	}
	return int(number), nil
}

func parseJSNumber(raw string) (float64, bool) {
	if len(raw) > 2 && raw[0] == '0' {
		base := 0
		switch raw[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			value, err := strconv.ParseUint(raw[2:], base, 64)
			return float64(value), err == nil
		}
	}
	value, err := strconv.ParseFloat(raw, 64)
	return value, err == nil
}
