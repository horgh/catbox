package main

import (
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

	// Ignore PONG. Just accept it.
	if m.Command == "PONG" {
		return
	}

	// 421 ERR_UNKNOWNCOMMAND
	c.messageFromServer("421", []string{m.Command, "Unknown command"})
}
