package main

import (
	"fmt"
	"regexp"
	"strings"
)

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

func isValidRealName(s string) bool {
	// Arbitrary. Length only for now.
	return len(s) <= 64
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

func isNumericCommand(command string) bool {
	for _, c := range command {
		if c < 48 || c > 57 {
			return false
		}
	}
	return true
}

func isValidUID(s string) bool {
	// SID + ID = UID
	if len(s) != 9 {
		return false
	}

	if !isValidSID(s[0:3]) {
		return false
	}
	return isValidID(s[3:])
}

func isValidID(s string) bool {
	matched, err := regexp.MatchString("^[A-Z][A-Z0-9]{5}$", s)
	if err != nil {
		return false
	}
	return matched
}

func isValidSID(s string) bool {
	matched, err := regexp.MatchString("^[0-9][0-9A-Z]{2}$", s)
	if err != nil {
		return false
	}
	return matched
}

// Make TS6 ID. 6 characters long, [A-Z][A-Z0-9]{5}. Must be unique on this
// server.
// I already assign clients a unique integer ID per server. Use this to generate
// a TS6 ID.
// Take integer ID and convert it to base 36. (A-Z and 0-9)
func makeTS6ID(id uint64) (TS6ID, error) {
	// Check the integer ID is < 26*36**5. That is as many valid TS6 IDs we can
	// have. The first character must be [A-Z], the remaining 5 are [A-Z0-9],
	// hence 36**5 vs. 26.
	// This is also the maximum number of connections we can have per run.
	// 1,572,120,576
	if id >= 1572120576 {
		return "", fmt.Errorf("TS6 ID overflow")
	}

	n := id

	ts6id := []byte("AAAAAA")

	for pos := 5; pos >= 0; pos-- {
		if n >= 36 {
			rem := n % 36

			// 0 to 25 are A to Z
			// 26 to 35 are 0 to 9
			if rem >= 26 {
				ts6id[pos] = byte(rem - 26 + '0')
			} else {
				ts6id[pos] = byte(rem + 'A')
			}

			n /= 36
			continue
		}

		if n >= 26 {
			ts6id[pos] = byte(n - 26 + '0')
		} else {
			ts6id[pos] = byte(n + 'A')
		}

		// Once we are < 36, we're done.
		break
	}

	return TS6ID(ts6id), nil
}
