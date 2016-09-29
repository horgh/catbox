package main

// Server holds information about a linked server. Local and remote.
type Server struct {
	SID         TS6SID
	Name        string
	Description string
	Hopcount    int
	LocalServer *LocalServer
}
