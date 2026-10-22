package controller

import "testing"

func TestJSStringLengthCountsUTF16CodeUnits(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int
	}{{"hello", 5}, {"é", 1}, {"🎮", 2}, {"a🎮b", 4}} {
		if got := JSStringLength(test.value); got != test.want {
			t.Errorf("JSStringLength(%q) = %d, want %d", test.value, got, test.want)
		}
	}
}
