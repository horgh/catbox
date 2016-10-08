package main

// Channel holds everything to do with a channel.
type Channel struct {
	// Canonicalized name.
	Name string

	// Members in the channel.
	// If we have zero members, we should not exist.
	Members map[TS6UID]struct{}

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
