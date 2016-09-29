package main

import (
	"fmt"
	"time"

	"summercat.com/irc"
)

// LocalUser holds information relevant only to a regular user (non-server)
// client.
type LocalUser struct {
	*LocalClient
	*User

	// Technically a server client does not need this. But we have one assigned
	// as every client gets a unique number.
	UID TS6UID

	// The last time we heard anything from the client.
	LastActivityTime time.Time

	// The last time we sent the client a PING.
	LastPingTime time.Time

	// The last time the client sent a PRIVMSG/NOTICE. We use this to decide
	// idle time.
	LastMessageTime time.Time
}

// NewLocalUser makes a LocalUser from a LocalClient.
func NewLocalUser(c *LocalClient) *LocalUser {
	now := time.Now()

	return &LocalUser{
		LocalClient:      c,
		LastActivityTime: now,
		LastPingTime:     now,
		LastMessageTime:  now,
	}
}

func (u *LocalUser) String() string {
	return u.User.String()
}

func (u *LocalUser) getLastActivityTime() time.Time {
	return u.LastActivityTime
}

func (u *LocalUser) getLastPingTime() time.Time {
	return u.LastPingTime
}

func (u *LocalUser) setLastPingTime(t time.Time) {
	u.LastPingTime = t
}

func (u *LocalUser) onChannel(channel *Channel) bool {
	_, exists := u.Channels[channel.Name]
	return exists
}

func (u *LocalUser) notice(s string) {
	u.messageFromServer("NOTICE", []string{
		u.DisplayNick,
		fmt.Sprintf("*** Notice --- %s", s),
	})
}

// Send an IRC message to a client. Appears to be from the server.
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
func (u *LocalUser) messageFromServer(command string, params []string) {
	// For numeric messages, we need to prepend the nick.
	// Use * for the nick in cases where the client doesn't have one yet.
	// This is what ircd-ratbox does. Maybe not RFC...
	if isNumericCommand(command) {
		newParams := []string{u.DisplayNick}
		newParams = append(newParams, params...)
		params = newParams
	}

	u.maybeQueueMessage(irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
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
func (u *LocalUser) messageClient(to *LocalUser, command string,
	params []string) {
	to.maybeQueueMessage(irc.Message{
		Prefix:  u.nickUhost(),
		Command: command,
		Params:  params,
	})
}

func (u *LocalUser) messageUser(to *User, command string,
	params []string) {
	if to.LocalUser != nil {
		to.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  u.nickUhost(),
			Command: command,
			Params:  params,
		})
	}
}

// handleMessage takes action based on a client's IRC message.
func (u *LocalUser) handleMessage(m irc.Message) {
	// Record that client said something to us just now.
	u.LastActivityTime = time.Now()

	// Clients SHOULD NOT (section 2.3) send a prefix. I'm going to disallow it
	// completely for all commands.
	if m.Prefix != "" {
		u.messageFromServer("ERROR", []string{"Do not send a prefix"})
		return
	}

	// Non-RFC command that appears to be widely supported. Just ignore it for
	// now.
	if m.Command == "CAP" {
		return
	}

	if m.Command == "NICK" {
		u.nickCommand(m)
		return
	}

	if m.Command == "USER" {
		u.userCommand(m)
		return
	}

	if m.Command == "JOIN" {
		u.joinCommand(m)
		return
	}

	if m.Command == "PART" {
		u.partCommand(m)
		return
	}

	// Per RFC these commands are near identical.
	if m.Command == "PRIVMSG" || m.Command == "NOTICE" {
		u.privmsgCommand(m)
		return
	}

	if m.Command == "LUSERS" {
		u.lusersCommand()
		return
	}

	if m.Command == "MOTD" {
		u.motdCommand()
		return
	}

	if m.Command == "QUIT" {
		u.quitCommand(m)
		return
	}

	if m.Command == "PONG" {
		// Not doing anything with this. Just accept it.
		return
	}

	if m.Command == "PING" {
		u.pingCommand(m)
		return
	}

	if m.Command == "DIE" {
		u.dieCommand(m)
		return
	}

	if m.Command == "WHOIS" {
		u.whoisCommand(m)
		return
	}

	if m.Command == "OPER" {
		u.operCommand(m)
		return
	}

	if m.Command == "MODE" {
		u.modeCommand(m)
		return
	}

	if m.Command == "WHO" {
		u.whoCommand(m)
		return
	}

	if m.Command == "TOPIC" {
		u.topicCommand(m)
		return
	}

	if m.Command == "CONNECT" {
		u.connectCommand(m)
		return
	}

	if m.Command == "LINKS" {
		u.linksCommand(m)
		return
	}

	// Unknown command. We don't handle it yet anyway.

	// 421 ERR_UNKNOWNCOMMAND
	u.messageFromServer("421", []string{m.Command, "Unknown command"})
}

// part tries to remove the client from the channel.
//
// We send a reply to the client. We also inform any other clients that need to
// know.
//
// NOTE: Only the server goroutine should call this (as we interact with its
//   member variables).
func (u *LocalUser) part(channelName, message string) {
	// NOTE: Difference from RFC 2812: I only accept one channel at a time.
	channelName = canonicalizeChannel(channelName)

	if !isValidChannel(channelName) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		u.messageFromServer("403", []string{channelName, "Invalid channel name"})
		return
	}

	// Find the channel.
	channel, exists := u.Catbox.Channels[channelName]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		u.messageFromServer("403", []string{channelName, "No such channel"})
		return
	}

	// Are they on the channel?
	if !u.onChannel(channel) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		u.messageFromServer("403", []string{channelName, "You are not on that channel"})
		return
	}

	// Tell everyone (including the client) about the part.
	for memberUID := range channel.Members {
		params := []string{channelName}

		// Add part message.
		if len(message) > 0 {
			params = append(params, message)
		}

		member := u.Catbox.Users[memberUID]

		// From the client to each member.
		u.messageUser(member, "PART", params)
	}

	// Remove the client from the channel.
	delete(channel.Members, u.UID)
	delete(u.Channels, channel.Name)

	// If they are the last member, then drop the channel completely.
	if len(channel.Members) == 0 {
		delete(u.Catbox.Channels, channel.Name)
	}
}

// Note: Only the server goroutine should call this (due to closing channel).
func (u *LocalUser) quit(msg string) {
	// May already be cleaning up.
	_, exists := u.Catbox.LocalUsers[u.UID]
	if !exists {
		return
	}

	// Tell all clients the client is in the channel with, and remove the client
	// from each channel it is in.

	// Tell each client only once.

	toldClients := map[TS6UID]struct{}{}

	for _, channel := range u.Channels {
		for memberUID := range channel.Members {
			_, exists := toldClients[memberUID]
			if exists {
				continue
			}

			member := u.Catbox.Users[memberUID]

			u.messageUser(member, "QUIT", []string{msg})

			toldClients[memberUID] = struct{}{}
		}

		delete(channel.Members, u.UID)
		if len(channel.Members) == 0 {
			delete(u.Catbox.Channels, channel.Name)
		}
	}

	// Ensure we tell the client (e.g., if in no channels).
	_, exists = toldClients[u.UID]
	if !exists {
		u.messageUser(u.User, "QUIT", []string{msg})
	}

	u.messageFromServer("ERROR", []string{msg})

	close(u.WriteChan)

	delete(u.Catbox.Nicks, canonicalizeNick(u.DisplayNick))

	delete(u.Catbox.LocalUsers, u.UID)

	if u.isOperator() {
		delete(u.Catbox.Opers, u.UID)
	}
}
