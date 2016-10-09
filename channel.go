package main

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

	// Channel TS. Changes on channel creation (or if another server tells us
	// a different TS).
	TS int64
}

// Check if a user has operator status in the channel.
func (c Channel) userHasOps(u *User) bool {
	_, exists := c.Ops[u.UID]
	return exists
}

// Remove a user from the channel.
func (c Channel) removeUser(u *User) {
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
