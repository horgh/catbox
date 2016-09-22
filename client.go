package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"summercat.com/irc"
)

// Client holds state about a single client connection.
type Client struct {
	Conn Conn

	WriteChan chan irc.Message

	// A unique id.
	ID uint64

	IP net.IP

	// Whether it completed connection registration.
	Registered bool

	// Nick. Not canonicalized.
	DisplayNick string

	User string

	RealName string

	// Channel name (canonicalized) to Channel.
	Channels map[string]*Channel

	Server *Server

	// The last time we heard anything from the client.
	LastActivityTime time.Time

	// The last time the client sent a PRIVMSG/NOTICE. We use this to decide
	// idle time.
	LastMessageTime time.Time

	// The last time we sent the client a PING.
	LastPingTime time.Time

	// User modes
	Modes map[byte]struct{}
}

// NewClient creates a Client
func NewClient(s *Server, id uint64, conn net.Conn) *Client {
	tcpAddr, err := net.ResolveTCPAddr("tcp", conn.RemoteAddr().String())
	// This shouldn't happen.
	if err != nil {
		log.Fatalf("Unable to resolve TCP address: %s", err)
	}

	return &Client{
		Conn:      NewConn(conn),
		WriteChan: make(chan irc.Message),
		ID:        id,
		Channels:  make(map[string]*Channel),
		Server:    s,
		Modes:     make(map[byte]struct{}),
		IP:        tcpAddr.IP,
	}
}

// Send an IRC message to a client from another client.
// The server is the one sending it, but it appears from the client through use
// of the prefix.
//
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
func (c *Client) messageClient(to *Client, command string, params []string) {
	to.WriteChan <- irc.Message{
		Prefix:  c.nickUhost(),
		Command: command,
		Params:  params,
	}
}

func (c *Client) onChannel(channel *Channel) bool {
	_, exists := c.Channels[channel.Name]
	return exists
}

// readLoop endlessly reads from the client's TCP connection. It parses each
// IRC protocol message and passes it to the server through the server's
// channel.
func (c *Client) readLoop() {
	defer c.Server.WG.Done()

	for {
		if c.Server.shuttingDown() {
			log.Printf("Client %s: Read goroutine shutting down", c)
			return
		}

		message, err := c.Conn.ReadMessage()
		if err != nil {
			log.Printf("Client %s: %s", c, err)
			c.Server.newDeadClient(c)
			return
		}

		c.Server.newClientMessage(c, message)
	}
}

// writeLoop endlessly reads from the client's channel, encodes each message,
// and writes it to the client's TCP connection.
func (c *Client) writeLoop() {
	defer c.Server.WG.Done()

	for message := range c.WriteChan {
		err := c.Conn.WriteMessage(message)
		if err != nil {
			log.Printf("Client %s: %s", c, err)
			c.Server.newDeadClient(c)
			break
		}
	}

	log.Printf("Client %s write goroutine terminating.", c)
}

func (c *Client) String() string {
	return fmt.Sprintf("%d %s", c.ID, c.Conn.RemoteAddr())
}

func (c *Client) nickUhost() string {
	return fmt.Sprintf("%s!~%s@%s", c.DisplayNick, c.User, c.IP)
}

// part tries to remove the client from the channel.
//
// We send a reply to the client. We also inform any other clients that need to
// know.
//
// NOTE: Only the server goroutine should call this (as we interact with its
//   member variables).
func (c *Client) part(channelName, message string) {
	// NOTE: Difference from RFC 2812: I only accept one channel at a time.
	channelName = canonicalizeChannel(channelName)

	if !isValidChannel(channelName) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.Server.messageClient(c, "403", []string{channelName, "Invalid channel name"})
		return
	}

	// Find the channel.
	channel, exists := c.Server.Channels[channelName]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.Server.messageClient(c, "403", []string{channelName, "No such channel"})
		return
	}

	// Are they on the channel?
	if !c.onChannel(channel) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.Server.messageClient(c, "403", []string{channelName, "You are not on that channel"})
		return
	}

	// Tell everyone (including the client) about the part.
	for _, member := range channel.Members {
		params := []string{channelName}

		// Add part message.
		if len(message) > 0 {
			params = append(params, message)
		}

		// From the client to each member.
		c.messageClient(member, "PART", params)
	}

	// Remove the client from the channel.
	delete(channel.Members, c.ID)
	delete(c.Channels, channel.Name)

	// If they are the last member, then drop the channel completely.
	if len(channel.Members) == 0 {
		delete(c.Server.Channels, channel.Name)
	}
}

// Note: Only the server goroutine should call this (due to closing channel).
func (c *Client) quit(msg string) {
	if c.Registered {
		// Tell all clients the client is in the channel with.
		// Also remove the client from each channel.
		toldClients := map[uint64]struct{}{}
		for _, channel := range c.Channels {
			for _, client := range channel.Members {
				_, exists := toldClients[client.ID]
				if exists {
					continue
				}

				c.messageClient(client, "QUIT", []string{msg})

				toldClients[client.ID] = struct{}{}
			}

			delete(channel.Members, c.ID)
			if len(channel.Members) == 0 {
				delete(c.Server.Channels, channel.Name)
			}
		}

		// Ensure we tell the client (e.g., if in no channels).
		_, exists := toldClients[c.ID]
		if !exists {
			c.messageClient(c, "QUIT", []string{msg})
		}

		delete(c.Server.Nicks, canonicalizeNick(c.DisplayNick))
	} else {
		// May have set a nick.
		if len(c.DisplayNick) > 0 {
			delete(c.Server.Nicks, canonicalizeNick(c.DisplayNick))
		}
	}

	// messageClient blocks on sending to the client's channel.
	c.Server.messageClient(c, "ERROR", []string{msg})

	// Close the channel to write to the client's connection.
	close(c.WriteChan)

	// Close the client's TCP connection.
	err := c.Conn.Close()
	if err != nil {
		log.Printf("Client %s: Problem closing connection: %s", c, err)
	}

	delete(c.Server.Clients, c.ID)
}

func (c *Client) isOperator() bool {
	_, exists := c.Modes['o']
	return exists
}
