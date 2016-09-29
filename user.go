package main

import (
	"fmt"

	"summercat.com/irc"
)

// User holds information about a user. It may be remote or local.
type User struct {
	DisplayNick string

	// I don't track hopcount.

	NickTS   int64
	Modes    map[byte]struct{}
	Username string
	Hostname string
	IP       string
	UID      TS6UID
	RealName string

	// Channel name (canonicalized) to Channel.
	Channels  map[string]*Channel
	LocalUser *LocalUser
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

func (u *User) messageUser(to *User, command string,
	params []string) {
	if to.LocalUser != nil {
		to.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  u.nickUhost(),
			Command: command,
			Params:  params,
		})
		return
	}

	// TODO: Remote users
}
