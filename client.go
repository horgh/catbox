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

// UserClient holds information relevant only to a regular user (non-server)
// client.
type UserClient struct {
	Client

	// Nick. Not canonicalized.
	DisplayNick string

	// Sent by USER command
	User string

	// Sent by USER command
	RealName string

	// Channel name (canonicalized) to Channel.
	Channels map[string]*Channel

	// The last time the client sent a PRIVMSG/NOTICE. We use this to decide
	// idle time.
	LastMessageTime time.Time

	// User modes
	Modes map[byte]struct{}
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

// NewUserClient makes a UserClient from a Client.
func NewUserClient(c *Client) *UserClient {
	rc := &UserClient{
		// UserClient members.
		Channels:        make(map[string]*Channel),
		LastMessageTime: time.Now(),
		Modes:           make(map[byte]struct{}),
	}

	// Copy Client members. TODO: is there a nicer syntax?
	rc.Conn = c.Conn
	rc.WriteChan = c.WriteChan
	rc.ID = c.ID
	rc.Server = c.Server
	rc.LastActivityTime = c.LastActivityTime
	rc.LastPingTime = c.LastPingTime

	rc.DisplayNick = c.PreRegDisplayNick
	rc.User = c.PreRegUser
	rc.RealName = c.PreRegRealName

	return rc
}

// Send an IRC message to a client from another client.
// The server is the one sending it, but it appears from the client through use
// of the prefix.
//
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
func (c *UserClient) messageClient(to *UserClient, command string,
	params []string) {
	to.WriteChan <- irc.Message{
		Prefix:  c.nickUhost(),
		Command: command,
		Params:  params,
	}
}

func (c *UserClient) onChannel(channel *Channel) bool {
	_, exists := c.Channels[channel.Name]
	return exists
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

func (c *Client) String() string {
	return fmt.Sprintf("%d %s", c.ID, c.Conn.RemoteAddr())
}

func (c *UserClient) nickUhost() string {
	return fmt.Sprintf("%s!~%s@%s", c.DisplayNick, c.User, c.Conn.IP)
}

// part tries to remove the client from the channel.
//
// We send a reply to the client. We also inform any other clients that need to
// know.
//
// NOTE: Only the server goroutine should call this (as we interact with its
//   member variables).
func (c *UserClient) part(channelName, message string) {
	// NOTE: Difference from RFC 2812: I only accept one channel at a time.
	channelName = canonicalizeChannel(channelName)

	if !isValidChannel(channelName) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.messageFromServer("403", []string{channelName, "Invalid channel name"})
		return
	}

	// Find the channel.
	channel, exists := c.Server.Channels[channelName]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.messageFromServer("403", []string{channelName, "No such channel"})
		return
	}

	// Are they on the channel?
	if !c.onChannel(channel) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.messageFromServer("403", []string{channelName, "You are not on that channel"})
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
func (c *UserClient) quit(msg string) {
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

	// blocks on sending to the client's channel.
	c.messageFromServer("ERROR", []string{msg})

	c.destroy()

	delete(c.Server.UserClients, c.ID)
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

func (c *Client) destroy() {
	// Close the channel to write to the client's connection.
	close(c.WriteChan)

	// Close the client's TCP connection.
	err := c.Conn.Close()
	if err != nil {
		log.Printf("Client %s: Problem closing connection: %s", c, err)
	}
}

func (c *UserClient) isOperator() bool {
	_, exists := c.Modes['o']
	return exists
}

// TS6 ID. 6 characters long, [A-Z]{6}. Must be unique on this server.
// Digits are legal too (after position 0), but I'm not using them at this
// time.
// I already assign clients a unique integer ID per server. Use this to generate
// a TS6 ID.
// Take integer ID and convert it to base 26. (A-Z)
func (c *Client) getTS6ID() (string, error) {
	// Check the integer ID is < 26**6. If it's not then we've overflowed.
	// This means we can have at most 26**6 (308,915,776) connections.
	if c.ID >= 308915776 {
		return "", fmt.Errorf("TS6 ID overflow")
	}

	id := c.ID

	ts6id := []byte("AAAAAA")
	pos := 5

	for id >= 26 {
		rem := id % 26
		char := byte(rem) + 'A'

		ts6id[pos] = char
		pos--

		id = id / 26
	}
	char := byte(id + 'A')
	ts6id[pos] = char

	return string(ts6id), nil
}

// UID = SID concatenated with ID
func (c *Client) getTS6UID() (string, error) {
	id, err := c.getTS6ID()
	if err != nil {
		return "", err
	}

	return c.Server.Config.TS6SID + id, nil
}
