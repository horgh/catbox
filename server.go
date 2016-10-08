package main

import "fmt"

// Server holds information about a linked server. Local and remote.
type Server struct {
	// Each server has a unique ID. SID. This is part of TS6. 3 characters.
	SID TS6SID

	// Each server has a unique name. e.g., irc.example.com.
	Name string

	// Each server has a one line description.
	Description string

	// Number of hops from us to this server.
	HopCount int

	// Capabilities. TS6 servers must support at least QS (quit storm) and
	// ENCAP. There are several others. ratbox servers offer for example:
	// QS EX CHW IE GLN KNOCK TB ENCAP SAVE SAVETS_100
	// Primarily I record this as on link to a server we pass along the capabs
	// of all known servers to the other side (as part of TS6 burst). This ensures
	// all servers know each other's capabilities.
	Capabs map[string]struct{}

	// If this server is directly connected to us (local), then LocalServer is
	// set.
	LocalServer *LocalServer

	// This is the server we heard about the server through.
	// If the server is not directly connected to us (remote), then we know how
	// it is connected to us. Through this LocalServer.
	ClosestServer *LocalServer

	// We know what server it is linked to. The SID message tells us.
	LinkedTo *Server
}

func (s *Server) String() string {
	return fmt.Sprintf("%s %s", s.SID, s.Name)
}

func (s *Server) isLocal() bool {
	return s.LocalServer != nil
}

func (s *Server) isRemote() bool {
	return !s.isLocal()
}

// Turn our capabilities into a single space separated string.
func (s *Server) capabsString() string {
	str := ""
	for capab := range s.Capabs {
		if len(str) > 0 {
			str += " " + capab
		} else {
			str += capab
		}
	}
	return str
}

// Check if the server supports a given capability.
func (s *Server) hasCapability(cap string) bool {
	_, exists := s.Capabs[cap]
	return exists
}

// Find all servers linked to us, directly or not.
func (s *Server) getLinkedServers(allServers map[TS6SID]*Server) []*Server {
	linkedServers := []*Server{}

	for _, server := range allServers {
		if server == s {
			continue
		}

		if server.LinkedTo != s {
			continue
		}

		// It's linked to us.
		linkedServers = append(linkedServers, server)

		// Find any servers linked to it. They are linked to us too (indirectly).
		linkedServers = append(linkedServers,
			server.getLinkedServers(allServers)...)
	}

	return linkedServers
}

// Count how many users are on this server.
func (s *Server) getLocalUserCount(users map[TS6UID]*User) int {
	count := 0
	for _, u := range users {
		if u.Server == s {
			count++
		}
	}
	return count
}
