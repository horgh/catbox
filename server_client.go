package main

import (
	"fmt"
	"time"

	"summercat.com/irc"
)

// ServerClient means the client registered as a server. This holds its info.
type ServerClient struct {
	Client

	// Its TS6 SID
	TS6SID string

	Name string

	Description string

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

// NewServerClient upgrades a Client to a ServerClient
func NewServerClient(c *Client) *ServerClient {
	now := time.Now()
	return &ServerClient{
		Client:           *c,
		TS6SID:           c.PreRegTS6SID,
		Capabs:           c.PreRegCapabs,
		Name:             c.PreRegServerName,
		Description:      c.PreRegServerDesc,
		LastActivityTime: now,
		LastPingTime:     now,
		GotPING:          false,
		GotPONG:          false,
		Bursting:         true,
	}
}

func (c *ServerClient) getLastActivityTime() time.Time {
	return c.LastActivityTime
}

func (c *ServerClient) getLastPingTime() time.Time {
	return c.LastPingTime
}

func (c *ServerClient) setLastPingTime(t time.Time) {
	c.LastPingTime = t
}

func (c *ServerClient) quit(msg string) {
	c.messageFromServer("ERROR", []string{msg})
	close(c.WriteChan)

	delete(c.Server.ServerClients, c.ID)

	// TODO: Make all clients quit that are on the other side.
}

func (c *ServerClient) handleMessage(m irc.Message) {
	// Record that client said something to us just now.
	c.LastActivityTime = time.Now()

	if m.Command == "PING" {
		c.pingCommand(m)
		return
	}

	if m.Command == "PONG" {
		c.pongCommand(m)
		return
	}

	if m.Command == "ERROR" {
		c.errorCommand(m)
		return
	}

	// 421 ERR_UNKNOWNCOMMAND
	c.messageFromServer("421", []string{m.Command, "Unknown command"})
}

func (c *ServerClient) sendBurst() {
}

func (c *ServerClient) sendPING() {
	// PING <My SID>
	c.maybeQueueMessage(irc.Message{
		Command: "PING",
		Params: []string{
			c.Server.Config.TS6SID,
		},
	})
}

func (c *ServerClient) pingCommand(m irc.Message) {
	// We expect a PING from server as part of burst end.
	// PING <Remote SID>
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"PING", "Not enough parameters"})
		return
	}

	// Allow multiple pings.

	if m.Params[0] != c.TS6SID {
		c.quit("Unexpected SID")
		return
	}

	c.maybeQueueMessage(irc.Message{
		Prefix:  c.Server.Config.TS6SID,
		Command: "PONG",
		Params: []string{
			c.Server.Config.ServerName,
			c.TS6SID,
		},
	})

	c.GotPING = true

	if c.Bursting && c.GotPONG {
		c.Server.noticeOpers(fmt.Sprintf("Burst with %s over.", c.Name))
		c.Bursting = false
	}
}

func (c *ServerClient) pongCommand(m irc.Message) {
	// We expect this at end of server link burst.
	// :<Remote SID> PONG <Remote server name> <My SID>
	if len(m.Params) < 2 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"SVINFO", "Not enough parameters"})
		return
	}

	if m.Prefix != c.TS6SID {
		c.quit("Unknown prefix")
		return
	}

	if m.Params[0] != c.Name {
		c.quit("Unknown server name")
		return
	}

	if m.Params[1] != c.Server.Config.TS6SID {
		c.quit("Unknown SID")
		return
	}

	// No reply.

	c.GotPONG = true

	if c.Bursting && c.GotPING {
		c.Server.noticeOpers(fmt.Sprintf("Burst with %s over.", c.Name))
		c.Bursting = false
	}
}

func (c *ServerClient) errorCommand(m irc.Message) {
	c.quit("Bye")
}
