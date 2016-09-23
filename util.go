package main

import "strings"

// 50 from RFC
const maxChannelLength = 50

// Arbitrary. Something low enough we won't hit message limit.
const maxTopicLength = 300

// canonicalizeNick converts the given nick to its canonical representation
// (which must be unique).
//
// Note: We don't check validity or strip whitespace.
func canonicalizeNick(n string) string {
	return strings.ToLower(n)
}

// canonicalizeChannel converts the given channel to its canonical
// representation (which must be unique).
//
// Note: We don't check validity or strip whitespace.
func canonicalizeChannel(c string) string {
	return strings.ToLower(c)
}

// isValidNick checks if a nickname is valid.
func isValidNick(maxLen int, n string) bool {
	if len(n) == 0 || len(n) > maxLen {
		return false
	}

	// TODO: For now I accept only a-z, 0-9, or _. RFC is more lenient.
	for i, char := range n {
		if char >= 'a' && char <= 'z' {
			continue
		}

		if char >= '0' && char <= '9' {
			// No digits in first position.
			if i == 0 {
				return false
			}
			continue
		}

		if char == '_' {
			continue
		}

		return false
	}

	return true
}

// isValidUser checks if a user (USER command) is valid
func isValidUser(maxLen int, u string) bool {
	if len(u) == 0 || len(u) > maxLen {
		return false
	}

	// TODO: For now I accept only a-z or 0-9. RFC is more lenient.
	for _, char := range u {
		if char >= 'a' && char <= 'z' {
			continue
		}

		if char >= '0' && char <= '9' {
			continue
		}

		return false
	}

	return true
}

// isValidChannel checks a channel name for validity.
//
// You should canonicalize it before using this function.
func isValidChannel(c string) bool {
	if len(c) == 0 || len(c) > maxChannelLength {
		return false
	}

	// TODO: I accept only a-z or 0-9 as valid characters right now. RFC
	//   accepts more.
	for i, char := range c {
		if i == 0 {
			// TODO: I only allow # channels right now.
			if char == '#' {
				continue
			}
			return false
		}

		if char >= 'a' && char <= 'z' {
			continue
		}

		if char >= '0' && char <= '9' {
			continue
		}

		return false
	}

	return true
}
