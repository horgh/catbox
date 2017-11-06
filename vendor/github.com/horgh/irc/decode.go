// Package irc provides encoding and decoding of IRC protocol messages. It is
// useful for implementing clients and servers.
package irc

import (
	"fmt"
	"strings"
)

// ParseMessage parses a protocol message from the client/server. The message
// should include the trailing CRLF.
//
// See RFC 1459/2812 section 2.3.1.
func ParseMessage(line string) (Message, error) {
	line, err := fixLineEnding(line)
	if err != nil {
		return Message{}, fmt.Errorf("line does not have a valid ending: %s", line)
	}

	truncated := false

	if len(line) > MaxLineLength {
		truncated = true

		line = line[0:MaxLineLength-2] + "\r\n"
	}

	message := Message{}
	index := 0

	// It is optional to have a prefix.
	if line[0] == ':' {
		prefix, prefixIndex, err := parsePrefix(line)
		if err != nil {
			return Message{}, fmt.Errorf("problem parsing prefix: %s", err)
		}
		index = prefixIndex

		message.Prefix = prefix

		if index >= len(line) {
			return Message{}, fmt.Errorf("malformed message. Prefix only")
		}
	}

	// We've either parsed a prefix out or have no prefix.
	command, index, err := parseCommand(line, index)
	if err != nil {
		return Message{}, fmt.Errorf("problem parsing command: %s", err)
	}

	message.Command = command

	// May have params.
	params, index, err := parseParams(line, index)
	if err != nil {
		return Message{}, fmt.Errorf("problem parsing params: %s", err)
	}

	if len(params) > 15 {
		return Message{}, fmt.Errorf("too many parameters")
	}

	message.Params = params

	// We should now have CRLF.
	//
	// index should be pointing at the CR after parsing params.
	if index != len(line)-2 || line[index] != '\r' || line[index+1] != '\n' {
		return Message{}, fmt.Errorf("malformed message. No CRLF found. Looking for end at position %d", index)
	}

	if truncated {
		return message, ErrTruncated
	}

	return message, nil
}

// fixLineEnding tries to ensure the line ends with CRLF.
//
// If it ends with only LF, add a CR.
func fixLineEnding(line string) (string, error) {
	if len(line) == 0 {
		return "", fmt.Errorf("line is blank")
	}

	if len(line) == 1 {
		if line[0] == '\n' {
			return "\r\n", nil
		}

		return "", fmt.Errorf("line does not end with LF")
	}

	lastIndex := len(line) - 1
	secondLastIndex := lastIndex - 1

	if line[secondLastIndex] == '\r' && line[lastIndex] == '\n' {
		return line, nil
	}

	if line[lastIndex] == '\n' {
		return line[:lastIndex] + "\r\n", nil
	}

	return "", fmt.Errorf("line has no ending CRLF or LF")
}

// parsePrefix parses out the prefix portion of a string.
//
// line begins with : and ends with \n.
//
// If there is no error we return the prefix and the position after
// the SPACE.
// This means the index points to the first character of the command (in a well
// formed message). We do not confirm there actually is a character.
//
// We are parsing this:
// message    =  [ ":" prefix SPACE ] command [ params ] crlf
// prefix     =  servername / ( nickname [ [ "!" user ] "@" host ] )
//
// TODO: Enforce length limits
// TODO: Enforce character / format more strictly.
//   Right now I don't do much other than ensure there is no space.
func parsePrefix(line string) (string, int, error) {
	pos := 0

	if line[pos] != ':' {
		return "", -1, fmt.Errorf("line does not start with ':'")
	}

	for pos < len(line) {
		// Prefix ends with a space.
		if line[pos] == ' ' {
			break
		}

		// Basic character check.
		// I'm being very lenient here right now. Servername and hosts should only
		// allow [a-zA-Z0-9]. Nickname can have any except NUL, CR, LF, " ". I
		// choose to accept anything nicks can.
		if line[pos] == '\x00' || line[pos] == '\n' || line[pos] == '\r' {
			return "", -1, fmt.Errorf("invalid character found: %q", line[pos])
		}

		pos++
	}

	// We didn't find a space.
	if pos == len(line) {
		return "", -1, fmt.Errorf("no space found")
	}

	// Ensure we have at least one character in the prefix.
	if pos == 1 {
		return "", -1, fmt.Errorf("prefix is zero length")
	}

	// Return the prefix without the space.
	// New index is after the space.
	return line[1:pos], pos + 1, nil
}

// parseCommand parses the command portion of a message from the server.
//
// We start parsing at the given index in the string.
//
// We return the command portion and the index just after the command.
//
// ABNF:
// message    =  [ ":" prefix SPACE ] command [ params ] crlf
// command    =  1*letter / 3digit
// params     =  *14( SPACE middle ) [ SPACE ":" trailing ]
//            =/ 14( SPACE middle ) [ SPACE [ ":" ] trailing ]
func parseCommand(line string, index int) (string, int, error) {
	newIndex := index

	// Parse until we hit a non-letter or non-digit.
	for newIndex < len(line) {
		// Digit
		if line[newIndex] >= 48 && line[newIndex] <= 57 {
			newIndex++
			continue
		}

		// Letter
		if line[newIndex] >= 65 && line[newIndex] <= 122 {
			newIndex++
			continue
		}

		// Must be a space or CR.
		if line[newIndex] != ' ' &&
			line[newIndex] != '\r' {
			return "", -1, fmt.Errorf("unexpected character after command: %q",
				line[newIndex])
		}
		break
	}

	// 0 length command is not valid.
	if newIndex == index {
		return "", -1, fmt.Errorf("0 length command found")
	}

	// TODO: Enforce that we either have 3 digits or all letters.

	// Return command string without space or CR.
	// New index is at the CR or space.
	return strings.ToUpper(line[index:newIndex]), newIndex, nil
}

// parseParams parses the params part of a message.
//
// The given index points to the first character in the params.
//
// It is valid for there to be no params.
//
// We return each param (stripped of : in the case of 'trailing') and the index
// after the params end.
//
// Note there may be a blank parameter since trailing may be empty.
//
// See <params> in grammar.
func parseParams(line string, index int) ([]string, int, error) {
	newIndex := index
	var params []string

	for newIndex < len(line) {
		if line[newIndex] != ' ' {
			return params, newIndex, nil
		}

		// In theory we could treat the 15th parameter differently to account for
		// ":" being optional in RFC 2812. This is a difference from 1459 and I
		// suspect not seen in the wild, so I don't.

		param, paramIndex, err := parseParam(line, newIndex)
		if err != nil {
			// We should always have at least one character. However it is common in
			// the wild (ratbox, quassel) for there to be trailing space characters
			// before the CRLF. Permit this despite it arguably being invalid.
			//
			// We return the index pointing after the problem spaces as though we
			// consumed them. We will be pointing at the CR.
			if err == errEmptyParam {
				crIndex := isTrailingSpace(line, newIndex)
				if crIndex != -1 {
					return params, crIndex, nil
				}
			}

			return nil, -1, fmt.Errorf("problem parsing parameter: %s", err)
		}

		newIndex = paramIndex
		params = append(params, param)
	}

	return nil, -1, fmt.Errorf("malformed params. Not terminated properly")
}

// parseParam parses out a single parameter term.
//
// index points to a space.
//
// We return the parameter (stripped of : in the case of trailing) and the
// index after the parameter ends.
func parseParam(line string, index int) (string, int, error) {
	newIndex := index

	if line[newIndex] != ' ' {
		return "", -1, fmt.Errorf("malformed param. No leading space")
	}

	newIndex++

	if len(line) == newIndex {
		return "", -1, fmt.Errorf("malformed parameter. End of string after space")
	}

	// SPACE ":" trailing
	if line[newIndex] == ':' {
		newIndex++

		if len(line) == newIndex {
			return "", -1, fmt.Errorf("malformed parameter. End of string after ':'")
		}

		// It is valid for there to be no characters. Because: trailing   =  *( ":"
		// / " " / nospcrlfcl )

		paramIndexStart := newIndex

		for newIndex < len(line) {
			if line[newIndex] == '\x00' || line[newIndex] == '\r' ||
				line[newIndex] == '\n' {
				break
			}
			newIndex++
		}

		return line[paramIndexStart:newIndex], newIndex, nil
	}

	// We know we are parsing a <middle> and that we've dealt with :. This means
	// we accept any character except NUL, CR, or LF. A space means we're at the
	// end of the param.

	// paramIndexStart points at the character after the space.
	paramIndexStart := newIndex

	for newIndex < len(line) {
		if line[newIndex] == '\x00' || line[newIndex] == '\r' ||
			line[newIndex] == '\n' || line[newIndex] == ' ' {
			break
		}
		newIndex++
	}

	// Must have at least one character in this case. See grammar for 'middle'.
	if paramIndexStart == newIndex {
		return "", -1, errEmptyParam
	}

	return line[paramIndexStart:newIndex], newIndex, nil
}

// If the string from the given position to the end contains nothing but spaces
// until we reach CRLF, return the position of CR.
//
// This is so we can recognize stray trailing spaces and discard them. They are
// arguably invalid, but we want to be liberal in what we accept.
func isTrailingSpace(line string, index int) int {
	for i := index; i < len(line); i++ {
		if line[i] == ' ' {
			continue
		}

		if line[i] == '\r' {
			return i
		}

		return -1
	}

	// We didn't hit \r. Line was all spaces.
	return -1
}
