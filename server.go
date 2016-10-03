package main

import "fmt"

// Server holds information about a linked server. Local and remote.
type Server struct {
	SID         TS6SID
	Name        string
	Description string
	HopCount    int

	// If this server is directly connected to us (local), then LocalServer is
	// set.
	LocalServer *LocalServer

	// If the server is not directly connected to us (remote), then we know how
	// it is connected to us. Through this LocalServer.
	Link *LocalServer
}

func (s *Server) String() string {
	return fmt.Sprintf("%s %s", s.SID, s.Name)
}

func (s *Server) isLocal() bool {
	return s.LocalServer != nil
}
