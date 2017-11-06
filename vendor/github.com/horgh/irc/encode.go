package irc

import (
	"fmt"
	"strings"
)

// Encode encodes the Message into a raw protocol message string.
//
// The resulting string will have a trailing CRLF.
//
// If encoding the message would exceed the allowed maximum length (more than
// MaxLineLength bytes), we truncate and return as much as we can and return
// ErrTruncated. This truncated message may still be usable.
//
// It does not enforce command specific semantics.
func (m Message) Encode() (string, error) {
	s := ""

	if len(m.Prefix) > 0 {
		s += ":" + m.Prefix + " "
	}

	s += m.Command

	if len(s)+2 > MaxLineLength {
		return "", fmt.Errorf("message with only prefix/command is too long")
	}

	truncated := false

	// Both RFC 1459 and RFC 2812 limit us to 15 parameters.
	if len(m.Params) > 15 {
		return "", fmt.Errorf("too many parameters")
	}

	for i, param := range m.Params {
		// We need to prefix the parameter with a colon in a few cases:
		//
		// 1) When there is a space in the parameter
		//
		// 2) When the first character is a colon
		//
		// 3) When this is the last parameter and it is empty. We do this to ensure
		// it is visible. This is important e.g. in a TOPIC unset command (TS6
		// server protocol). Also, RFC 1459/2812's grammar permits this.
		//
		// RFC 2812 differs from RFC 1459 by saying that ":" is optional for the
		// 15th parameter, but we ignore that.
		if idx := strings.IndexAny(param, " "); idx != -1 ||
			(param != "" && param[0] == ':') ||
			param == "" {
			param = ":" + param

			// This must be the last parameter. There can only be one <trailing>.
			if i+1 != len(m.Params) {
				return "", fmt.Errorf(
					"parameter problem: ':' or ' ' outside last parameter")
			}
		}

		// If we add the parameter as is, do we exceed the maximum length?
		if len(s)+1+len(param)+2 > MaxLineLength {
			// Either we can truncate the parameter and include a portion of it, or
			// the parameter is too short to include at all. If it is too short to
			// include, then don't add the space separator either.

			// Claim the space separator (1) and CRLF (2) as used. Then we can tell
			// how many bytes are available for the parameter as it is.
			lengthUsed := len(s) + 1 + 2
			lengthAvailable := MaxLineLength - lengthUsed

			// If we prefixed the parameter with : then it's possible we include
			// only the : here (if length available is 1). This is perhaps a little
			// odd but I don't think problematic.

			if lengthAvailable > 0 {
				s += " " + param[0:lengthAvailable]
			}

			truncated = true
			break
		}

		s += " " + param
	}

	s += "\r\n"

	if truncated {
		return s, ErrTruncated
	}

	return s, nil
}
