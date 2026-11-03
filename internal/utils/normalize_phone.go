package utils

import "strings"

// NormalizePhone turns a number typed in any common shape ("3311 2233",
// "+973 33112233", "0097333112233", Arabic digits) into digits with the
// country code and no "+". Numbers of 8 digits or fewer get defaultCountryCode
// when one is set. Anything that isn't a phone number comes back as "".
func NormalizePhone(raw, defaultCountryCode string) string {
	var digits strings.Builder
	for _, char := range strings.TrimSpace(raw) {
		switch {
		case char >= '0' && char <= '9':
			digits.WriteRune(char)
		case char >= '٠' && char <= '٩':
			digits.WriteRune('0' + char - '٠')
		case char >= '۰' && char <= '۹':
			digits.WriteRune('0' + char - '۰')
		case char == '+' || char == ' ' || char == '-' || char == '(' || char == ')':
			// Formatting only.
		default:
			return ""
		}
	}
	number := strings.TrimPrefix(digits.String(), "00")
	if number != "" && len(number) <= 8 && defaultCountryCode != "" {
		return defaultCountryCode + number
	}
	return number
}
