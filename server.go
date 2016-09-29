package main

import "fmt"

// Server holds information about a linked server. Local and remote.
type Server struct {
	SID         TS6SID
	Name        string
	Description string
	HopCount    int
	LocalServer *LocalServer
}

func (s *Server) String() string {
	return fmt.Sprintf("%s %s", s.SID, s.Name)
}
