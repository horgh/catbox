package main

import "fmt"

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

	// This is the server we heard about the user from. It is not necessarily the
	// server they are on. It could be on a server linked to the one we are
	// linked to.
	ClosestServer *LocalServer

	// This is the server the user is connected to.
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

func (u *User) isLocal() bool {
	return u.LocalUser != nil
}

func (u *User) isRemote() bool {
	return !u.isLocal()
}
