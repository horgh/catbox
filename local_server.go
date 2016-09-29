package main

import (
	"fmt"
	"time"

	"summercat.com/irc"
)

// LocalServer means the client registered as a server. This holds its info.
type LocalServer struct {
	*LocalClient

	Server *Server

	Capabs map[string]struct{}

	// The last time we heard anything from it.
	LastActivityTime time.Time

	// The last time we sent it a PING.
	LastPingTime time.Time

	// Flags to know about our bursting state.
	GotPING  bool
	GotPONG  bool
	Bursting bool
}

// NewLocalServer upgrades a LocalClient to a LocalServer.
func NewLocalServer(c *LocalClient) *LocalServer {
	now := time.Now()

	s := &LocalServer{
		LocalClient:      c,
		Capabs:           c.PreRegCapabs,
		LastActivityTime: now,
		LastPingTime:     now,
		GotPING:          false,
		GotPONG:          false,
		Bursting:         true,
	}

	return s
}

func (s *LocalServer) String() string {
	return s.Server.String()
}

func (s *LocalServer) getLastActivityTime() time.Time {
	return s.LastActivityTime
}

func (s *LocalServer) getLastPingTime() time.Time {
	return s.LastPingTime
}

func (s *LocalServer) setLastPingTime(t time.Time) {
	s.LastPingTime = t
}

func (s *LocalServer) quit(msg string) {
	// May already be cleaning up.
	_, exists := s.Catbox.LocalServers[s.Server.SID]
	if !exists {
		return
	}

	s.messageFromServer("ERROR", []string{msg})

	close(s.WriteChan)

	delete(s.Catbox.LocalServers, s.Server.SID)

	// TODO: Make all clients quit that are on the other side.
}

func (s *LocalServer) sendBurst() {
	// TODO
}

func (s *LocalServer) sendPING() {
	// PING <My SID>
	s.maybeQueueMessage(irc.Message{
		Command: "PING",
		Params: []string{
			s.Catbox.Config.TS6SID,
		},
	})
}

func (s *LocalServer) handleMessage(m irc.Message) {
	// Record that client said something to us just now.
	s.LastActivityTime = time.Now()

	if m.Command == "PING" {
		s.pingCommand(m)
		return
	}

	if m.Command == "PONG" {
		s.pongCommand(m)
		return
	}

	if m.Command == "ERROR" {
		s.errorCommand(m)
		return
	}

	if m.Command == "UID" {
		s.uidCommand(m)
		return
	}

	// 421 ERR_UNKNOWNCOMMAND
	s.messageFromServer("421", []string{m.Command, "Unknown command"})
}

func (s *LocalServer) pingCommand(m irc.Message) {
	// We expect a PING from server as part of burst end.
	// PING <Remote SID>
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		s.messageFromServer("461", []string{"PING", "Not enough parameters"})
		return
	}

	// Allow multiple pings.

	if TS6SID(m.Params[0]) != s.Server.SID {
		s.quit("Unexpected SID")
		return
	}

	s.maybeQueueMessage(irc.Message{
		Prefix:  s.Catbox.Config.TS6SID,
		Command: "PONG",
		Params: []string{
			s.Catbox.Config.ServerName,
			string(s.Server.SID),
		},
	})

	s.GotPING = true

	if s.Bursting && s.GotPONG {
		s.Catbox.noticeOpers(fmt.Sprintf("Burst with %s over.", s.Server.Name))
		s.Bursting = false
	}
}

func (s *LocalServer) pongCommand(m irc.Message) {
	// We expect this at end of server link burst.
	// :<Remote SID> PONG <Remote server name> <My SID>
	if len(m.Params) < 2 {
		// 461 ERR_NEEDMOREPARAMS
		s.messageFromServer("461", []string{"SVINFO", "Not enough parameters"})
		return
	}

	if TS6SID(m.Prefix) != s.Server.SID {
		s.quit("Unknown prefix")
		return
	}

	if m.Params[0] != s.Server.Name {
		s.quit("Unknown server name")
		return
	}

	if m.Params[1] != s.Catbox.Config.TS6SID {
		s.quit("Unknown SID")
		return
	}

	// No reply.

	s.GotPONG = true

	if s.Bursting && s.GotPING {
		s.Catbox.noticeOpers(fmt.Sprintf("Burst with %s over.", s.Server.Name))
		s.Bursting = false
	}
}

func (s *LocalServer) errorCommand(m irc.Message) {
	s.quit("Bye")
}

// UID command introduces a client. It is on the server that is the source.
func (s *LocalServer) uidCommand(m irc.Message) {
	// Parameters: <nick> <hopcount> <nick TS> <umodes> <username> <hostname> <IP> <UID> :<real name>
	// :8ZZ UID will 1 1475024621 +i will blashyrkh. 0 8ZZAAAAAB :will
}
