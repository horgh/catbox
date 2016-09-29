package main

// Channel holds everything to do with a channel.
type Channel struct {
	// Canonicalized.
	Name    string
	Members map[TS6UID]struct{}
	Topic   string
	TS      int64
}
