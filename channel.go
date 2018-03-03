package main

import "github.com/horgh/irc"

// Channel holds everything to do with a channel.
type Channel struct {
	// Canonicalized name.
	Name string

	// Members in the channel.
	// If we have zero members, we should not exist.
	Members map[TS6UID]struct{}

	// Ops tracks users who have ops in the channel.
	Ops map[TS6UID]*User

	// Current topic. May be blank.
	Topic string

	// Topic TS. Changes on TOPIC command (or if server tells us one).
	TopicTS int64

	// The person who set the topic. nick!user@host
	TopicSetter string

	// Modes set on the channel.
	Modes map[byte]struct{}

	// Channel TS. Changes on channel creation (or if another server tells us
	// a different TS).
	TS int64
}

// Check if a user has operator status in the channel.
func (c *Channel) userHasOps(u *User) bool {
	_, exists := c.Ops[u.UID]
	return exists
}

// Remove a user from the channel.
func (c *Channel) removeUser(u *User) {
	_, exists := c.Members[u.UID]
	if exists {
		delete(c.Members, u.UID)
	}

	_, exists = c.Ops[u.UID]
	if exists {
		delete(c.Ops, u.UID)
	}

	_, exists = u.Channels[c.Name]
	if exists {
		delete(u.Channels, c.Name)
	}
}

// Grant a user ops.
func (c *Channel) grantOps(u *User) {
	c.Ops[u.UID] = u
}

// Remove ops from a user
func (c *Channel) removeOps(u *User) {
	_, exists := c.Ops[u.UID]
	if exists {
		delete(c.Ops, u.UID)
	}
}

// Remove all modes from the channel, and all ops/voices.
//
// This informs local users about the mode changes, but no one else.
func (c *Channel) clearModes(cb *Catbox) {
	// Build all the messages we need prior to sending.
	var msgs []irc.Message

	// Clear things like +ns

	modeStr := ""
	for k := range c.Modes {
		delete(c.Modes, k)
		modeStr += string(k)
	}
	if len(modeStr) > 0 {
		msgs = append(msgs, irc.Message{
			Prefix:  cb.Config.ServerName,
			Command: "MODE",
			Params:  []string{c.Name, "-" + modeStr},
		})
	}

	// Clear ops.

	var ops []string
	for _, op := range c.Ops {
		ops = append(ops, op.DisplayNick)

		if len(ops) == ChanModesPerCommand {
			modeStr := "-"
			for i := 0; i < ChanModesPerCommand; i++ {
				modeStr += "o"
			}

			params := []string{c.Name, modeStr}
			params = append(params, ops...)

			msgs = append(msgs, irc.Message{
				Prefix:  cb.Config.ServerName,
				Command: "MODE",
				Params:  params,
			})

			ops = nil
		}
	}

	if len(ops) > 0 {
		modeStr := "-"
		for range ops {
			modeStr += "o"
		}

		params := []string{c.Name, modeStr}
		params = append(params, ops...)

		msgs = append(msgs, irc.Message{
			Prefix:  cb.Config.ServerName,
			Command: "MODE",
			Params:  params,
		})
	}

	// Fire off the messages.
	for _, msg := range msgs {
		cb.messageLocalUsersOnChannel(c, msg)
	}
}
