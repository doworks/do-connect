package utils

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		raw, country, want string
	}{
		{"3311 2233", "973", "97333112233"},
		{"33112233", "973", "97333112233"},
		{"+973 3311 2233", "973", "97333112233"},
		{"0097333112233", "973", "97333112233"},
		{"٣٣١١٢٢٣٣", "973", "97333112233"},
		{"۳۳۱۱۲۲۳۳", "973", "97333112233"},
		{"+44 7700 900123", "973", "447700900123"},
		{"33112233", "", "33112233"},
		{"+1234567890", "", "1234567890"},
		{"Fatima", "973", ""},
		{"", "973", ""},
	}
	for _, c := range cases {
		if got := NormalizePhone(c.raw, c.country); got != c.want {
			t.Errorf("NormalizePhone(%q, %q) = %q, want %q", c.raw, c.country, got, c.want)
		}
	}
}
