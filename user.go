package main

import (
	"fmt"

	"summercat.com/irc"
)

// User holds information about a user. It may be remote or local.
type User struct {
	DisplayNick string
	HopCount    int
	NickTS      int64
	Modes       map[byte]struct{}
	Username    string
	Hostname    string
	IP          string
	UID         TS6UID
	RealName    string

	// Channel name (canonicalized) to Channel.
	Channels map[string]*Channel

	// LocalUser set if this is a local user.
	LocalUser *LocalUser

	// Server set if this is a remote user.
	// This is the server we heard about the user from. It is not necessarily the
	// server they are on. It could be on a server linked to the one we are
	// linked to.
	Server *Server
}

func (u *User) String() string {
	return fmt.Sprintf("%s: %s", u.UID, u.nickUhost())
}
func (u *User) nickUhost() string {
	return fmt.Sprintf("%s!~%s@%s", u.DisplayNick, u.Username, u.Hostname)
}

func (u *User) isOperator() bool {
	_, exists := u.Modes['o']
	return exists
}

func (u *User) onChannel(channel *Channel) bool {
	_, exists := u.Channels[channel.Name]
	return exists
}

func (u *User) modesString() string {
	s := "+"
	for m := range u.Modes {
		s += string(m)
	}
	return s
}

func (u *User) messageUser(to *User, command string, params []string) {
	if to.LocalUser != nil {
		to.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  u.nickUhost(),
			Command: command,
			Params:  params,
		})
		return
	}

	to.Server.LocalServer.maybeQueueMessage(irc.Message{
		Prefix:  string(u.UID),
		Command: command,
		Params:  params,
	})
}
