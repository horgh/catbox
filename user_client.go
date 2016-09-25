package main

import (
	"fmt"
	"time"

	"summercat.com/irc"
)

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

// NewUserClient makes a UserClient from a Client.
func NewUserClient(c *Client) *UserClient {
	rc := &UserClient{
		Client: *c,
		// UserClient members.
		DisplayNick:     c.PreRegDisplayNick,
		User:            c.PreRegUser,
		RealName:        c.PreRegRealName,
		Channels:        make(map[string]*Channel),
		LastMessageTime: time.Now(),
		Modes:           make(map[byte]struct{}),
	}

	return rc
}

func (c *UserClient) String() string {
	return fmt.Sprintf("%d: %s!~%s@%s", c.ID, c.DisplayNick, c.User, c.Conn.IP)
}

func (c *UserClient) nickUhost() string {
	return fmt.Sprintf("%s!~%s@%s", c.DisplayNick, c.User, c.Conn.IP)
}

func (c *UserClient) onChannel(channel *Channel) bool {
	_, exists := c.Channels[channel.Name]
	return exists
}

func (c *UserClient) isOperator() bool {
	_, exists := c.Modes['o']
	return exists
}

// Send an IRC message to a client. Appears to be from the server.
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
func (c *UserClient) messageFromServer(command string, params []string) {
	// For numeric messages, we need to prepend the nick.
	// Use * for the nick in cases where the client doesn't have one yet.
	// This is what ircd-ratbox does. Maybe not RFC...
	if isNumericCommand(command) {
		newParams := []string{c.DisplayNick}
		newParams = append(newParams, params...)
		params = newParams
	}

	c.maybeQueueMessage(irc.Message{
		Prefix:  c.Server.Config.ServerName,
		Command: command,
		Params:  params,
	})
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
	to.maybeQueueMessage(irc.Message{
		Prefix:  c.nickUhost(),
		Command: command,
		Params:  params,
	})
}

// handleMessage takes action based on a client's IRC message.
func (c *UserClient) handleMessage(m irc.Message) {
	// Record that client said something to us just now.
	c.LastActivityTime = time.Now()

	// Clients SHOULD NOT (section 2.3) send a prefix. I'm going to disallow it
	// completely for all commands.
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

	if m.Command == "JOIN" {
		c.joinCommand(m)
		return
	}

	if m.Command == "PART" {
		c.partCommand(m)
		return
	}

	// Per RFC these commands are near identical.
	if m.Command == "PRIVMSG" || m.Command == "NOTICE" {
		c.privmsgCommand(m)
		return
	}

	if m.Command == "LUSERS" {
		c.lusersCommand()
		return
	}

	if m.Command == "MOTD" {
		c.motdCommand()
		return
	}

	if m.Command == "QUIT" {
		c.quitCommand(m)
		return
	}

	if m.Command == "PONG" {
		// Not doing anything with this. Just accept it.
		return
	}

	if m.Command == "PING" {
		c.pingCommand(m)
		return
	}

	if m.Command == "DIE" {
		c.dieCommand(m)
		return
	}

	if m.Command == "WHOIS" {
		c.whoisCommand(m)
		return
	}

	if m.Command == "OPER" {
		c.operCommand(m)
		return
	}

	if m.Command == "MODE" {
		c.modeCommand(m)
		return
	}

	if m.Command == "WHO" {
		c.whoCommand(m)
		return
	}

	if m.Command == "TOPIC" {
		c.topicCommand(m)
		return
	}

	// Unknown command. We don't handle it yet anyway.

	// 421 ERR_UNKNOWNCOMMAND
	c.messageFromServer("421", []string{m.Command, "Unknown command"})
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
	// Tell all clients the client is in the channel with, and remove the client
	// from each channel it is in.

	// Tell each client only once.

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

	c.messageFromServer("ERROR", []string{msg})
	close(c.WriteChan)

	delete(c.Server.Nicks, canonicalizeNick(c.DisplayNick))
	delete(c.Server.UserClients, c.ID)
}

// TS6 ID. 6 characters long, [A-Z]{6}. Must be unique on this server.
// Digits are legal too (after position 0), but I'm not using them at this
// time.
// I already assign clients a unique integer ID per server. Use this to generate
// a TS6 ID.
// Take integer ID and convert it to base 26. (A-Z)
func (c *UserClient) getTS6ID() (string, error) {
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
func (c *UserClient) getTS6UID() (string, error) {
	id, err := c.getTS6ID()
	if err != nil {
		return "", err
	}

	return c.Server.Config.TS6SID + id, nil
}
