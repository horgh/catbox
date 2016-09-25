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

	ConnectionStartTime time.Time

	// Server references the main server the client is connected to (local
	// client).
	// It's helpful to have to avoid passing server all over the place.
	Server *Server

	// Track if we overflow our send queue. If we do, we'll kill the client.
	SendQueueExceeded bool

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
	return &Client{
		Conn: NewConn(conn, s.Config.DeadTime),

		// Buffered channel. We don't want to block sending to the client from the
		// server. The client may be stuck. Make the buffer large enough that it
		// should only max out in case of connection issues.
		WriteChan: make(chan irc.Message, 32768),

		ID:                  id,
		ConnectionStartTime: time.Now(),
		Server:              s,
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
//
// When the channel is closed, or if we have a write error, close the TCP
// connection. I have this here so that we try to deliver messages to the
// client before closing its socket and giving up.
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

	err := c.Conn.Close()
	if err != nil {
		log.Printf("Client %s: Problem closing connection: %s", c, err)
	}

	log.Printf("Client %s: Writer shutting down.", c)
}

// quit means the client is quitting. Tell it why and clean up.
func (c *Client) quit(msg string) {
	c.messageFromServer("ERROR", []string{msg})
	close(c.WriteChan)

	if len(c.PreRegDisplayNick) > 0 {
		delete(c.Server.Nicks, canonicalizeNick(c.PreRegDisplayNick))
	}

	delete(c.Server.UnregisteredClients, c.ID)
}

func (c *Client) handleMessage(m irc.Message) {
	// Clients SHOULD NOT (section 2.3) send a prefix. I'm going to disallow it
	// completely for all commands.
	// TODO: May need to allow it for pre-registered servers.
	if m.Prefix != "" {
		c.messageFromServer("ERROR", []string{"Do not send a prefix"})
		return
	}

	// Non-RFC command that appears to be widely supported. Just ignore it for
	// now.
	if m.Command == "CAP" {
		return
	}

	if m.Command == "NICK" {
		c.nickCommand(m)
		return
	}

	if m.Command == "USER" {
		c.userCommand(m)
		return
	}

	// Let's say *all* other commands require you to be registered.
	// This is likely stricter than RFC.
	// 451 ERR_NOTREGISTERED
	c.messageFromServer("451", []string{fmt.Sprintf("You have not registered.")})
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

// Send an IRC message to a client. Appears to be from the server.
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
func (c *Client) messageFromServer(command string, params []string) {
	// For numeric messages, we need to prepend the nick.
	// Use * for the nick in cases where the client doesn't have one yet.
	// This is what ircd-ratbox does. Maybe not RFC...
	if isNumericCommand(command) {
		nick := "*"
		if len(c.PreRegDisplayNick) > 0 {
			nick = c.PreRegDisplayNick
		}
		newParams := []string{nick}
		newParams = append(newParams, params...)
		params = newParams
	}

	c.maybeQueueMessage(irc.Message{
		Prefix:  c.Server.Config.ServerName,
		Command: command,
		Params:  params,
	})
}

// Send a message to the client. We send it to its write channel, which in turn
// leads to writing it to its TCP socket.
//
// This function won't block. If the client's queue is full, we flag it as
// having a full send queue.
//
// Not blocking is important because the server sends the client messages this
// way, and if we block on a problem client, everything would grind to a halt.
func (c *Client) maybeQueueMessage(m irc.Message) {
	if c.SendQueueExceeded {
		return
	}

	select {
	case c.WriteChan <- m:
	default:
		c.SendQueueExceeded = true
	}
}
