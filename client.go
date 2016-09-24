package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"summercat.com/irc"
)

// Client holds state about a single client connection.
// All clients are in this state until they register as either a user client
// or as a server.
type Client struct {
	// Conn holds the TCP connection to the client.
	Conn Conn

	// WriteChan is the channel to send to to write to the client.
	WriteChan chan irc.Message

	// A unique id. Internal to this server only.
	ID uint64

	// Server references the main server the client is connected to (local
	// client).
	// It's helpful to have to avoid passing server all over the place.
	Server *Server

	// The last time we heard anything from the client.
	LastActivityTime time.Time

	// The last time we sent the client a PING.
	LastPingTime time.Time

	// Info client may send us before we complete its registration and promote it
	// to UserClient or ServerClient.
	PreRegDisplayNick string
	PreRegUser        string
	PreRegRealName    string
}

// ServerClient means the client registered as a server. This holds its info.
type ServerClient struct {
	Client

	// Its TS6 SID
	TS6SID string
}

// NewClient creates a Client
func NewClient(s *Server, id uint64, conn net.Conn) *Client {
	now := time.Now()

	return &Client{
		Conn:             NewConn(conn),
		WriteChan:        make(chan irc.Message),
		ID:               id,
		Server:           s,
		LastActivityTime: now,
		LastPingTime:     now,
	}
}

func (c *Client) String() string {
	return fmt.Sprintf("%d %s", c.ID, c.Conn.RemoteAddr())
}

// readLoop endlessly reads from the client's TCP connection. It parses each
// IRC protocol message and passes it to the server through the server's
// channel.
func (c *Client) readLoop() {
	defer c.Server.WG.Done()

	for {
		if c.Server.isShuttingDown() {
			break
		}

		// This means if a client sends us an invalid message that we cut them off.
		message, err := c.Conn.ReadMessage()
		if err != nil {
			log.Printf("Client %s: %s", c, err)
			c.Server.newEvent(Event{Type: DeadClientEvent, Client: c})
			break
		}

		c.Server.newEvent(Event{
			Type:    MessageFromClientEvent,
			Client:  c,
			Message: message,
		})
	}

	log.Printf("Client %s: Reader shutting down.", c)
}

// writeLoop endlessly reads from the client's channel, encodes each message,
// and writes it to the client's TCP connection.
func (c *Client) writeLoop() {
	defer c.Server.WG.Done()

	for message := range c.WriteChan {
		err := c.Conn.WriteMessage(message)
		if err != nil {
			log.Printf("Client %s: %s", c, err)
			c.Server.newEvent(Event{Type: DeadClientEvent, Client: c})
			break
		}
	}

	log.Printf("Client %s: Writer shutting down.", c)
}

func (c *Client) quit(msg string) {
	// May have set a nick.
	if len(c.PreRegDisplayNick) > 0 {
		delete(c.Server.Nicks, canonicalizeNick(c.PreRegDisplayNick))
	}

	// blocks on sending to the client's channel.
	c.messageFromServer("ERROR", []string{msg})

	delete(c.Server.UnregisteredClients, c.ID)

	c.destroy()
}

// destroy closes the client's channel and TCP connection.
func (c *Client) destroy() {
	// Close the channel to write to the client's connection.
	close(c.WriteChan)

	// Close the client's TCP connection.
	err := c.Conn.Close()
	if err != nil {
		log.Printf("Client %s: Problem closing connection: %s", c, err)
	}
}

func (c *Client) completeRegistration() {
	// RFC 2813 specifies messages to send upon registration.

	userClient := NewUserClient(c)

	// 001 RPL_WELCOME
	c.messageFromServer("001", []string{
		fmt.Sprintf("Welcome to the Internet Relay Network %s",
			userClient.nickUhost()),
	})

	// 002 RPL_YOURHOST
	c.messageFromServer("002", []string{
		fmt.Sprintf("Your host is %s, running version %s",
			c.Server.Config.ServerName,
			c.Server.Config.Version),
	})

	// 003 RPL_CREATED
	c.messageFromServer("003", []string{
		fmt.Sprintf("This server was created %s", c.Server.Config.CreatedDate),
	})

	// 004 RPL_MYINFO
	// <servername> <version> <available user modes> <available channel modes>
	c.messageFromServer("004", []string{
		// It seems ambiguous if these are to be separate parameters.
		c.Server.Config.ServerName,
		c.Server.Config.Version,
		"o",
		"n",
	})

	userClient.lusersCommand()

	userClient.motdCommand()

	delete(c.Server.UnregisteredClients, c.ID)

	c.Server.UserClients[c.ID] = userClient
}
