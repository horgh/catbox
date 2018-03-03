package irc

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// MaxLineLength is the maximum protocol message line length. It includes
	// CRLF.
	MaxLineLength = 512

	// ReplyWelcome is the RPL_WELCOME response numeric.
	ReplyWelcome = "001"

	// ReplyYoureOper is the RPL_YOUREOPER response numeric.
	ReplyYoureOper = "381"
)

// ErrTruncated is the error returned by Encode if the message gets truncated
// due to encoding to more than MaxLineLength bytes.
var ErrTruncated = errors.New("message truncated")

// It is not always valid for there to be a parameter with zero characters. If
// there is one, it should have a ':' prefix.
var errEmptyParam = errors.New("parameter with zero characters")

// Message holds a protocol message. See section 2.3.1 in RFC 1459/2812.
type Message struct {
	// Prefix may be blank. It's optional.
	Prefix string

	// Command is the IRC command. For example, PRIVMSG. It may be a numeric.
	Command string

	// There are at most 15 parameters.
	Params []string
}

func (m Message) String() string {
	return fmt.Sprintf("Prefix [%s] Command [%s] Params%q", m.Prefix, m.Command,
		m.Params)
}

// SourceNick retrieves the nickname portion of the prefix. It is valid for
// this to be blank as not all messages have prefixes.
func (m Message) SourceNick() string {
	idx := strings.Index(m.Prefix, "!")
	if idx == -1 {
		return ""
	}
	return m.Prefix[:idx]
}
