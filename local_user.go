package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"summercat.com/irc"
)

// LocalUser holds information relevant only to a regular user (non-server)
// client.
type LocalUser struct {
	*LocalClient

	User *User

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

	u := &LocalUser{
		LocalClient:      c,
		LastActivityTime: now,
		LastPingTime:     now,
		LastMessageTime:  now,
	}

	return u
}

func (u *LocalUser) String() string {
	return fmt.Sprintf("%s %s", u.User.String(), u.Conn.RemoteAddr())
}

// Message from this local user to another user, remote or local.
func (u *LocalUser) messageUser(to *User, command string, params []string) {
	if to.isLocal() {
		to.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  u.User.nickUhost(),
			Command: command,
			Params:  params,
		})
		return
	}

	to.ClosestServer.maybeQueueMessage(irc.Message{
		Prefix:  string(u.User.UID),
		Command: command,
		Params:  params,
	})
}

func (u *LocalUser) serverNotice(s string) {
	u.messageFromServer("NOTICE", []string{
		u.User.DisplayNick,
		fmt.Sprintf("*** Notice --- %s", s),
	})
}

// Make TS6 UID. UID = SID concatenated with ID
func (u *LocalUser) makeTS6UID(id uint64) (TS6UID, error) {
	ts6id, err := makeTS6ID(u.ID)
	if err != nil {
		return TS6UID(""), err
	}

	return TS6UID(string(u.Catbox.Config.TS6SID) + string(ts6id)), nil
}

// Send an IRC message to a client. Appears to be from the server.
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
func (u *LocalUser) messageFromServer(command string, params []string) {
	// For numeric messages, we need to prepend the nick.
	if isNumericCommand(command) {
		newParams := []string{u.User.DisplayNick}
		newParams = append(newParams, params...)
		params = newParams
	}

	u.maybeQueueMessage(irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
		Command: command,
		Params:  params,
	})
}

// join tries to join the client to a channel.
//
// We've validated the name is valid and have canonicalized it.
func (u *LocalUser) join(channelName string) {
	// Is the client in the channel already? Ignore it if so.
	if u.User.onChannel(&Channel{Name: channelName}) {
		return
	}

	// Look up the channel. Create it if necessary.
	channel, exists := u.Catbox.Channels[channelName]
	if !exists {
		channel = &Channel{
			Name:    channelName,
			Members: make(map[TS6UID]struct{}),
			TS:      time.Now().Unix(),
		}
		u.Catbox.Channels[channelName] = channel
	}

	// Add them to the channel.
	channel.Members[u.User.UID] = struct{}{}
	u.User.Channels[channelName] = channel

	// Tell the client about the join.
	// This is what RFC says to send: JOIN, RPL_TOPIC, and RPL_NAMREPLY.

	// JOIN comes from the client, to the client.
	u.messageUser(u.User, "JOIN", []string{channel.Name})

	// If this is a new channel, send them the modes we set by default.
	if !exists {
		u.messageFromServer("MODE", []string{channel.Name, "+ns"})
	}

	// It appears RPL_TOPIC is optional, at least ircd-ratbox does always send it.
	// Presumably if there is no topic.
	if len(channel.Topic) > 0 {
		// 332 RPL_TOPIC
		u.messageFromServer("332", []string{channel.Name, channel.Topic})
	}

	// Channel flag: = (public), * (private), @ (secret)
	// When we have more chan modes (-s / +p) this needs to vary
	channelFlag := "@"

	// RPL_NAMREPLY: This tells the client about who is in the channel
	// (including itself).
	// It ends with RPL_ENDOFNAMES.
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]
		// 353 RPL_NAMREPLY
		u.messageFromServer("353", []string{
			// We need to include @ / + for each nick opped/voiced (when we have
			// ops/voices).
			// TODO: Multiple nicks per RPL_NAMREPLY.
			channelFlag, channel.Name, member.DisplayNick,
		})
	}

	// 366 RPL_ENDOFNAMES
	u.messageFromServer("366", []string{channel.Name, "End of NAMES list"})

	// Tell each member in the channel about the client.
	// Only local clients. Servers will tell their own clients.
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]
		if !member.isLocal() {
			continue
		}

		// Don't tell the client. We already did (above).
		if member.UID == u.User.UID {
			continue
		}

		// From the client to each member.
		u.messageUser(member, "JOIN", []string{channel.Name})
	}

	// Tell servers about this.
	// If it's a new channel, then use SJOIN. Otherwise JOIN.
	for _, server := range u.Catbox.LocalServers {
		if !exists {
			server.maybeQueueMessage(irc.Message{
				Prefix:  string(u.Catbox.Config.TS6SID),
				Command: "SJOIN",
				Params: []string{
					fmt.Sprintf("%d", channel.TS),
					channel.Name,
					"+ns",
					string(u.User.UID),
				},
			})
		} else {
			server.maybeQueueMessage(irc.Message{
				Prefix:  string(u.User.UID),
				Command: "JOIN",
				Params: []string{
					fmt.Sprintf("%d", channel.TS),
					channel.Name,
					"+",
				},
			})
		}
	}
}

// part tries to remove the client from the channel.
//
// We send a reply to the client. We also inform any other clients that need to
// know.
//
// NOTE: Only the server goroutine should call this (as we interact with its
//   member variables).
func (u *LocalUser) part(channelName, message string) {
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
	if !u.User.onChannel(channel) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		u.messageFromServer("403", []string{channelName, "You are not on that channel"})
		return
	}

	partParams := []string{channelName}
	if len(message) > 0 {
		partParams = append(partParams, message)
	}

	// Tell local clients (including the client) about the part.
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]
		if !member.isLocal() {
			continue
		}

		member.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  u.User.nickUhost(),
			Command: "PART",
			Params:  partParams,
		})
	}

	// Tell all servers. Looks like for TS6, or ratbox at least, channel
	// membership is known globally, even if no clients present in the channel.
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "PART",
			Params:  partParams,
		})
	}

	// Remove the client from the channel.
	delete(channel.Members, u.User.UID)
	delete(u.User.Channels, channel.Name)

	// If they are the last member, then drop the channel completely.
	if len(channel.Members) == 0 {
		delete(u.Catbox.Channels, channel.Name)
	}
}

// We inform servers about a QUIT if propagate is true. You may not want to
// do so if the client is getting kicked for another reason, such as KILL.
//
// In the case of KILL, you propagate a KILL message to servers rather than
// QUIT. This function does not do that for you. It propagates only a QUIT
// message if you ask it.
//
// Note: Only the server goroutine should call this (due to closing channel).
func (u *LocalUser) quit(msg string, propagate bool) {
	// May already be cleaning up.
	_, exists := u.Catbox.LocalUsers[u.ID]
	if !exists {
		return
	}

	// Tell all clients the client is in the channel with, and remove the client
	// from each channel it is in.

	// Tell each client only once.

	// Tell only local clients. We tell servers separately, who in turn tell their
	// clients.

	toldClients := map[TS6UID]struct{}{}

	for _, channel := range u.User.Channels {
		for memberUID := range channel.Members {
			member := u.Catbox.Users[memberUID]
			if !member.isLocal() {
				continue
			}

			_, exists := toldClients[member.UID]
			if exists {
				continue
			}
			toldClients[member.UID] = struct{}{}

			member.LocalUser.maybeQueueMessage(irc.Message{
				Prefix:  u.User.nickUhost(),
				Command: "QUIT",
				Params:  []string{msg},
			})

		}

		delete(channel.Members, u.User.UID)
		if len(channel.Members) == 0 {
			delete(u.Catbox.Channels, channel.Name)
		}
	}

	// Ensure we tell the client (e.g., if in no channels).
	_, exists = toldClients[u.User.UID]
	if !exists {
		u.messageUser(u.User, "QUIT", []string{msg})
	}

	// Tell all servers. They need to know about client departing.
	if propagate {
		for _, server := range u.Catbox.LocalServers {
			server.maybeQueueMessage(irc.Message{
				Prefix:  string(u.User.UID),
				Command: "QUIT",
				Params:  []string{msg},
			})
		}
	}

	u.messageFromServer("ERROR", []string{msg})

	close(u.WriteChan)

	delete(u.Catbox.Nicks, canonicalizeNick(u.User.DisplayNick))
	delete(u.Catbox.LocalUsers, u.ID)
	if u.User.isOperator() {
		delete(u.Catbox.Opers, u.User.UID)
	}
}

// The user sent us a message. Deal with it.
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

	if m.Command == "WALLOPS" {
		u.wallopsCommand(m)
		return
	}

	if m.Command == "KILL" {
		u.killCommand(m)
		return
	}

	if m.Command == "KLINE" {
		u.klineCommand(m)
		return
	}

	if m.Command == "UNKLINE" {
		u.unklineCommand(m)
		return
	}

	if m.Command == "STATS" {
		u.statsCommand(m)
		return
	}

	if m.Command == "REHASH" {
		u.rehashCommand(m)
		return
	}

	// Unknown command. We don't handle it yet anyway.
	// 421 ERR_UNKNOWNCOMMAND
	u.messageFromServer("421", []string{m.Command, "Unknown command"})
}

// The NICK command to happen both at connection registration time and
// after. There are different rules.
func (u *LocalUser) nickCommand(m irc.Message) {
	// We should have one parameter: The nick they want.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		u.messageFromServer("431", []string{"No nickname given"})
		return
	}
	nick := m.Params[0]

	if len(nick) > u.Catbox.Config.MaxNickLength {
		nick = nick[0:u.Catbox.Config.MaxNickLength]
	}

	if !isValidNick(u.Catbox.Config.MaxNickLength, nick) {
		// 432 ERR_ERRONEUSNICKNAME
		u.messageFromServer("432", []string{nick, "Erroneous nickname"})
		return
	}

	nickCanon := canonicalizeNick(nick)

	// Nick must be unique.
	_, exists := u.Catbox.Nicks[nickCanon]
	if exists {
		// 433 ERR_NICKNAMEINUSE
		u.messageFromServer("433", []string{nick, "Nickname is already in use"})
		return
	}

	// Flag the nick as taken by this client.
	u.Catbox.Nicks[nickCanon] = u.User.UID
	oldDisplayNick := u.User.DisplayNick

	// Free the old nick.
	delete(u.Catbox.Nicks, canonicalizeNick(oldDisplayNick))

	u.User.NickTS = time.Now().Unix()

	// We need to inform other clients about the nick change.
	// Any that are in the same channel as this client.
	// Tell only local clients. Tell all servers after.
	// Tell each client only once.
	// Message needs to come from the OLD nick.
	informedClients := map[TS6UID]struct{}{}
	for _, channel := range u.User.Channels {
		for memberUID := range channel.Members {
			member := u.Catbox.Users[memberUID]
			if !member.isLocal() {
				continue
			}

			_, exists := informedClients[member.UID]
			if exists {
				continue
			}
			informedClients[member.UID] = struct{}{}

			u.messageUser(member, "NICK", []string{nick})
		}
	}

	// Reply to the client. We should have above, but if they were not on any
	// channels then we did not.
	_, exists = informedClients[u.User.UID]
	if !exists {
		u.messageUser(u.User, "NICK", []string{nick})
	}

	// Finally, make the update. Do this last as we need to ensure we act
	// as the old nick when crafting messages.
	u.User.DisplayNick = nick

	// Propagate to servers.
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "NICK",
			Params:  []string{u.User.DisplayNick, fmt.Sprintf("%d", u.User.NickTS)},
		})
	}
}

// The USER command only occurs during connection registration.
func (u *LocalUser) userCommand(m irc.Message) {
	// 462 ERR_ALREADYREGISTRED
	u.messageFromServer("462", []string{"Unauthorized command (already registered)"})
}

func (u *LocalUser) joinCommand(m irc.Message) {
	// Parameters: ( <channel> *( "," <channel> ) [ <key> *( "," <key> ) ] ) / "0"

	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"JOIN", "Not enough parameters"})
		return
	}

	// JOIN 0 is a special case. Client leaves all channels.
	if m.Params[0] == "0" {
		for _, channel := range u.User.Channels {
			u.part(channel.Name, "")
		}
		return
	}

	// May have multiple channels in a single command.
	channels := commaChannelsToChannelNames(m.Params[0])

	// We could support keys.

	// Try to join the client to the channels.
	for _, channelName := range channels {
		u.join(channelName)
	}
}

func (u *LocalUser) partCommand(m irc.Message) {
	// Parameters: <channel> *( "," <channel> ) [ <Part Message> ]

	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"PART", "Not enough parameters"})
		return
	}

	partMessage := ""
	if len(m.Params) >= 2 {
		partMessage = m.Params[1]
	}

	// May have multiple channels in a single command.
	channels := commaChannelsToChannelNames(m.Params[0])

	for _, channel := range channels {
		u.part(channel, partMessage)
	}
}

// Per RFC 2812, PRIVMSG and NOTICE are essentially the same, so both PRIVMSG
// and NOTICE use this command function.
func (u *LocalUser) privmsgCommand(m irc.Message) {
	// Parameters: <msgtarget> <text to be sent>

	if len(m.Params) == 0 {
		// 411 ERR_NORECIPIENT
		u.messageFromServer("411", []string{"No recipient given (PRIVMSG)"})
		return
	}

	if len(m.Params) == 1 || len(m.Params[1]) == 0 {
		// 412 ERR_NOTEXTTOSEND
		u.messageFromServer("412", []string{"No text to send"})
		return
	}

	// I don't check if there are too many parameters. They get ignored anyway.

	target := m.Params[0]

	msg := m.Params[1]

	// The message may be too long once we add the prefix/encode the message.
	// Strip any trailing characters until it's short enough.
	// TODO: Other messages can have this problem too (PART, QUIT, etc...)

	// If sent remote, we convert target to UID (if it's a nick). So if it looks
	// like a nick, let's say it is at least UID length (9).
	targetLen := len(target)
	if target[0] != '#' && len(target) < 9 {
		targetLen = 9
	}
	msgLen := len(":") + len(u.User.nickUhost()) + len(" ") + len(m.Command) +
		len(" ") + targetLen + len(" ") + len(":") + len(msg) + len("\r\n")
	if msgLen > irc.MaxLineLength {
		trimCount := msgLen - irc.MaxLineLength
		msg = msg[:len(msg)-trimCount]
	}

	// I only support # channels right now.

	if target[0] == '#' {
		channelName := canonicalizeChannel(target)
		if !isValidChannel(channelName) {
			// 404 ERR_CANNOTSENDTOCHAN
			u.messageFromServer("404", []string{channelName, "Cannot send to channel"})
			return
		}

		channel, exists := u.Catbox.Channels[channelName]
		if !exists {
			// 403 ERR_NOSUCHCHANNEL
			u.messageFromServer("403", []string{channelName, "No such channel"})
			return
		}

		// Are they on it?
		// Technically we should allow messaging if they aren't on it
		// depending on the mode.
		if !u.User.onChannel(channel) {
			// 404 ERR_CANNOTSENDTOCHAN
			u.messageFromServer("404", []string{channelName, "Cannot send to channel"})
			return
		}

		u.LastMessageTime = time.Now()

		// Send to all members of the channel. Except the client itself it seems.
		// Tell local users directly.
		// If a user is remote, record the server we should propagate the message
		// towards. Tell each server only once.
		toServers := make(map[*LocalServer]struct{})
		for memberUID := range channel.Members {
			member := u.Catbox.Users[memberUID]
			if member.UID == u.User.UID {
				continue
			}

			if member.isLocal() {
				// From the client to each member.
				u.messageUser(member, m.Command, []string{channel.Name, msg})
				continue
			}

			toServers[member.ClosestServer] = struct{}{}
		}

		// Propagate message to any servers that need it.
		for server := range toServers {
			server.maybeQueueMessage(irc.Message{
				Prefix:  string(u.User.UID),
				Command: m.Command,
				Params:  []string{channel.Name, msg},
			})
		}

		return
	}

	// We're messaging a nick directly.

	nickName := canonicalizeNick(target)
	if !isValidNick(u.Catbox.Config.MaxNickLength, nickName) {
		// 401 ERR_NOSUCHNICK
		u.messageFromServer("401", []string{nickName, "No such nick/channel"})
		return
	}

	targetUID, exists := u.Catbox.Nicks[nickName]
	if !exists {
		// 401 ERR_NOSUCHNICK
		u.messageFromServer("401", []string{nickName, "No such nick/channel"})
		return
	}
	targetUser := u.Catbox.Users[targetUID]

	u.LastMessageTime = time.Now()

	if targetUser.isLocal() {
		u.messageUser(targetUser, m.Command, []string{nickName, msg})
	} else {
		u.messageUser(targetUser, m.Command, []string{string(targetUser.UID),
			msg})
	}
}

func (u *LocalUser) lusersCommand() {
	// We always send RPL_LUSERCLIENT and RPL_LUSERME.
	// The others only need be sent if the counts are non-zero.

	// 251 RPL_LUSERCLIENT
	u.messageFromServer("251", []string{
		fmt.Sprintf("There are %d users and %d services on %d servers.",
			len(u.Catbox.Users),
			0,
			// +1 to count ourself.
			len(u.Catbox.Users)+1),
	})

	// 252 RPL_LUSEROP
	operCount := 0
	for _, user := range u.Catbox.Users {
		if user.isOperator() {
			operCount++
		}
	}
	if operCount > 0 {
		// 252 RPL_LUSEROP
		u.messageFromServer("252", []string{
			fmt.Sprintf("%d", operCount),
			"operator(s) online",
		})
	}

	// 253 RPL_LUSERUNKNOWN
	// Unregistered connections.
	numUnknown := len(u.Catbox.LocalClients)
	if numUnknown > 0 {
		u.messageFromServer("253", []string{
			fmt.Sprintf("%d", numUnknown),
			"unknown connection(s)",
		})
	}

	// 254 RPL_LUSERCHANNELS
	// RFC 2811 says to not include +s channels in this count. But I do.
	if len(u.Catbox.Channels) > 0 {
		u.messageFromServer("254", []string{
			fmt.Sprintf("%d", len(u.Catbox.Channels)),
			"channels formed",
		})
	}

	// 255 RPL_LUSERME
	u.messageFromServer("255", []string{
		fmt.Sprintf("I have %d clients and %d servers",
			len(u.Catbox.LocalUsers), len(u.Catbox.LocalServers)),
	})
}

func (u *LocalUser) motdCommand() {
	// 375 RPL_MOTDSTART
	u.messageFromServer("375", []string{
		fmt.Sprintf("- %s Message of the day - ", u.Catbox.Config.ServerName),
	})

	// 372 RPL_MOTD
	u.messageFromServer("372", []string{
		fmt.Sprintf("- %s", u.Catbox.Config.MOTD),
	})

	// 376 RPL_ENDOFMOTD
	u.messageFromServer("376", []string{"End of MOTD command"})
}

func (u *LocalUser) quitCommand(m irc.Message) {
	msg := "Quit:"
	if len(m.Params) > 0 {
		msg += " " + m.Params[0]
	}

	u.quit(msg, true)
}

func (u *LocalUser) pingCommand(m irc.Message) {
	// Parameters: <server> (I choose to not support forwarding)
	if len(m.Params) == 0 {
		// 409 ERR_NOORIGIN
		u.messageFromServer("409", []string{"No origin specified"})
		return
	}

	// Certain clients don't send PING following any standard.
	// For example, Quassel sends a timestamp like "PING 22:46:48.650". Which is
	// even more interesting when we consider it has : in it, leading to us to
	// have issues saying "<timestamp> :is not a valid server" (as : is then in
	// the first parameter).
	//
	// Let's just reply with our server name as if they issued a correct PING to
	// us.
	//
	// If we were strict, we should reply with:
	// 402 <nick> <server> :No such server

	u.messageFromServer("PONG", []string{u.Catbox.Config.ServerName})
}

func (u *LocalUser) dieCommand(m irc.Message) {
	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	// die is not an RFC command. I use it to shut down the server.
	u.Catbox.shutdown()
}

func (u *LocalUser) whoisCommand(m irc.Message) {
	// Difference from RFC: I support only a single nickname (no mask), and no
	// server target.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		u.messageFromServer("431", []string{"No nickname given"})
		return
	}

	nick := m.Params[0]

	uid, exists := u.Catbox.Nicks[canonicalizeNick(nick)]
	if !exists {
		// 401 ERR_NOSUCHNICK
		u.maybeQueueMessage(irc.Message{
			Prefix:  u.Catbox.Config.ServerName,
			Command: "401",
			Params:  []string{u.User.DisplayNick, nick, "No such nick/channel"},
		})
		return
	}
	user := u.Catbox.Users[uid]

	// Ask the remote server for the whois if it is a remote user. This gets us
	// all the interesting details we may not have locally.
	if user.isRemote() {
		user.ClosestServer.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "WHOIS",
			Params:  []string{string(user.UID), user.DisplayNick},
		})
		return
	}

	// It's a local user. Respond ourself.

	msgs := u.Catbox.createWHOISResponse(user, u.User, false)
	for _, msg := range msgs {
		u.maybeQueueMessage(msg)
	}
}

func (u *LocalUser) operCommand(m irc.Message) {
	// Parameters: <name> <password>
	if len(m.Params) < 2 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"OPER", "Not enough parameters"})
		return
	}

	if u.User.isOperator() {
		// 381 RPL_YOUREOPER
		u.messageFromServer("381", []string{"You are already an IRC operator"})
		return
	}

	// We could require particular user/hostmask per oper.

	// Check if they gave acceptable permissions.
	pass, exists := u.Catbox.Config.Opers[m.Params[0]]
	if !exists || pass != m.Params[1] {
		// 464 ERR_PASSWDMISMATCH
		u.messageFromServer("464", []string{"Password incorrect"})
		return
	}

	// Give them oper status.
	u.User.Modes['o'] = struct{}{}

	u.Catbox.Opers[u.User.UID] = u.User

	// From themselves to themselves.
	u.messageUser(u.User, "MODE", []string{u.User.DisplayNick, "+o"})

	// 381 RPL_YOUREOPER
	u.messageFromServer("381", []string{"You are now an IRC operator"})

	// Tell all servers about this mode change.
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "MODE",
			Params:  []string{string(u.User.UID), "+o"},
		})
	}
}

// MODE command applies either to nicknames or to channels.
func (u *LocalUser) modeCommand(m irc.Message) {
	// User mode:
	// Parameters: <nickname> *( ( "+" / "-" ) *( "i" / "w" / "o" / "O" / "r" ) )

	// Channel mode:
	// Parameters: <channel> *( ( "-" / "+" ) *<modes> *<modeparams> )

	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"MODE", "Not enough parameters"})
		return
	}

	target := m.Params[0]

	// We can have blank mode. This will cause server to send current settings.
	modes := ""
	if len(m.Params) > 1 {
		modes = m.Params[1]
	}

	// Is it a nickname?
	targetUID, exists := u.Catbox.Nicks[canonicalizeNick(target)]
	if exists {
		targetUser := u.Catbox.Users[targetUID]
		u.userModeCommand(targetUser, modes)
		return
	}

	// Is it a channel?
	targetChannel, exists := u.Catbox.Channels[canonicalizeChannel(target)]
	if exists {
		u.channelModeCommand(targetChannel, modes)
		return
	}

	// Well... Not found. Send a channel not found. It seems the closest matching
	// extant error in RFC.
	// 403 ERR_NOSUCHCHANNEL
	u.messageFromServer("403", []string{target, "No such channel"})
}

// Modes we support at this time:
// +i (but not changing) (invisible)
// +o/-o (operator)
// +C/-C (must be +o to alter) (client connection notices)
func (u *LocalUser) userModeCommand(targetUser *User, modes string) {
	// They can only change their own mode.
	if targetUser.LocalUser != u {
		// 502 ERR_USERSDONTMATCH
		u.messageFromServer("502", []string{"Cannot change mode for other users"})
		return
	}

	// No modes given means we should send back their current mode.
	if len(modes) == 0 {
		// 221 RPL_UMODEIS
		u.messageFromServer("221", []string{u.User.modesString()})
		return
	}

	setModes, unsetModes, unknownModes, err := parseAndResolveUmodeChanges(modes,
		u.User.Modes)
	if err != nil {
		// 501 ERR_UMODEUNKNOWNFLAG
		u.messageFromServer("501", []string{"Unknown MODE flag"})
		return
	}

	// Apply changes and build the mode string.
	setModeStr := ""
	for mode := range setModes {
		if mode == 'o' {
			u.Catbox.Opers[u.User.UID] = u.User
		}
		u.User.Modes[mode] = struct{}{}
		setModeStr += string(mode)
	}
	unsetModeStr := ""
	for mode := range unsetModes {
		if mode == 'o' {
			delete(u.Catbox.Opers, u.User.UID)
		}
		delete(u.User.Modes, mode)
		unsetModeStr += string(mode)
	}

	// Combined string.
	modeStr := ""
	if len(setModeStr) > 0 {
		modeStr += "+" + setModeStr
	}
	if len(unsetModeStr) > 0 {
		modeStr += "-" + unsetModeStr
	}

	// We only inform the user or server if there was a change.
	if len(modeStr) > 0 {
		// Tell the user.
		u.maybeQueueMessage(irc.Message{
			Prefix:  u.User.nickUhost(),
			Command: "MODE",
			Params:  []string{u.User.DisplayNick, modeStr},
		})

		// Inform servers about the mode change.
		for _, server := range u.Catbox.LocalServers {
			server.maybeQueueMessage(irc.Message{
				Prefix:  string(u.User.UID),
				Command: "MODE",
				Params:  []string{string(u.User.UID), modeStr},
			})
		}
	}

	if len(unknownModes) > 0 {
		// 501 ERR_UMODEUNKNOWNFLAG
		u.messageFromServer("501", []string{"Unknown MODE flag"})
	}
}

func (u *LocalUser) channelModeCommand(channel *Channel, modes string) {
	if !u.User.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		u.messageFromServer("442", []string{channel.Name, "You're not on that channel"})
		return
	}

	// No modes? Send back the channel's modes.
	// Always send back +ns. That's only I support right now.
	if len(modes) == 0 {
		// 324 RPL_CHANNELMODEIS
		u.messageFromServer("324", []string{channel.Name, "+ns"})
		return
	}

	// Listing bans. I don't support bans at this time, but say that there are
	// none.
	if modes == "b" || modes == "+b" {
		// 368 RPL_ENDOFBANLIST
		u.messageFromServer("368", []string{channel.Name, "End of channel ban list"})
		return
	}

	// Since we don't have channel operators implemented, any attempt to alter
	// mode is an error.
	// 482 ERR_CHANOPRIVSNEEDED
	u.messageFromServer("482", []string{channel.Name, "You're not channel operator"})
}

func (u *LocalUser) whoCommand(m irc.Message) {
	// Contrary to RFC 2812, I support only 'WHO #channel'.
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	channel, exists := u.Catbox.Channels[canonicalizeChannel(m.Params[0])]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		u.messageFromServer("403", []string{m.Params[0], "Invalid channel name"})
		return
	}

	// Only works if they are on the channel.
	if !u.User.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		u.messageFromServer("442", []string{channel.Name, "You're not on that channel"})
		return
	}

	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]

		// 352 RPL_WHOREPLY
		// "<channel> <user> <host> <server> <nick>
		// ( "H" / "G" > ["*"] [ ( "@" / "+" ) ]
		// :<hopcount> <real name>"
		// NOTE: I'm not sure what H/G mean. I think G is away.
		// Hopcount seems unimportant also.
		mode := "H"
		if member.isOperator() {
			mode += "*"
		}
		u.messageFromServer("352", []string{
			channel.Name,
			member.Username,
			fmt.Sprintf("%s", member.Hostname),
			u.Catbox.Config.ServerName,
			member.DisplayNick,
			mode,
			"0 " + member.RealName,
		})
	}

	// 315 RPL_ENDOFWHO
	u.messageFromServer("315", []string{channel.Name, "End of WHO list"})
}

func (u *LocalUser) topicCommand(m irc.Message) {
	// Params: <channel> [ <topic> ]
	if len(m.Params) == 0 {
		u.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	channelName := canonicalizeChannel(m.Params[0])
	channel, exists := u.Catbox.Channels[channelName]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		u.messageFromServer("403", []string{m.Params[0], "Invalid channel name"})
		return
	}

	if !u.User.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		u.messageFromServer("442", []string{channel.Name, "You're not on that channel"})
		return
	}

	// If there is no new topic, then just send back the current one.
	if len(m.Params) < 2 {
		if len(channel.Topic) == 0 {
			// 331 RPL_NOTOPIC
			u.messageFromServer("331", []string{channel.Name, "No topic is set"})
			return
		}

		// 332 RPL_TOPIC
		u.messageFromServer("332", []string{channel.Name, channel.Topic})
		return
	}

	// Set new topic.

	topic := m.Params[1]
	if len(topic) > maxTopicLength {
		topic = topic[:maxTopicLength]
	}

	// If we have channel operators then we need additional logic.

	channel.Topic = topic

	// Tell all members of the channel, including the client.
	// Only local clients. We tell remote users by telling all servers.
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]
		if !member.isLocal() {
			continue
		}

		// 332 RPL_TOPIC
		u.messageUser(member, "TOPIC", []string{channel.Name, channel.Topic})
	}

	// Topic appears to propagate globally no matter what.
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "TOPIC",
			Params:  []string{channel.Name, channel.Topic},
		})
	}
}

// Initiate a connection to a server.
//
// I implement CONNECT differently than RFC 2812. Only a single parameter.
func (u *LocalUser) connectCommand(m irc.Message) {
	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	// CONNECT <server name>
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	serverName := m.Params[0]

	// Is it a server we know about?
	linkInfo, exists := u.Catbox.Config.Servers[serverName]
	if !exists {
		// 402 ERR_NOSUCHSERVER
		u.messageFromServer("402", []string{serverName, "No such server"})
		return
	}

	// Are we already linked to it?
	if u.Catbox.isLinkedToServer(serverName) {
		// No great error code.
		u.serverNotice(fmt.Sprintf("I am already linked to %s.", serverName))
		return
	}

	// We could check if we're already trying to link to it. But the result should
	// be the same.
	u.Catbox.connectToServer(linkInfo)
}

func (u *LocalUser) linksCommand(m irc.Message) {
	// Difference from RFC: No parameters respected.

	// Ourself.
	// 364 RPL_LINKS
	// <mask> <server> :<hopcount> <server info>
	u.messageFromServer("364", []string{
		u.Catbox.Config.ServerName,
		u.Catbox.Config.ServerName,
		fmt.Sprintf("%d %s", 0, u.Catbox.Config.ServerInfo),
	})

	for _, s := range u.Catbox.Servers {
		// 364 RPL_LINKS
		// <mask> <server> :<hopcount> <server info>
		u.messageFromServer("364", []string{
			s.Name,
			s.Name,
			fmt.Sprintf("%d %s", s.HopCount, s.Description),
		})
	}

	// 365 RPL_ENDOFLINKS
	// <mask> :End of LINKS list
	u.messageFromServer("365", []string{"*", "End of LINKS list"})
}

// WALLOPS command causes us to send the text to all local operators as a
// WALLOPS command. We also send it on to each remote server so it can do the
// same and show its operators.
func (u *LocalUser) wallopsCommand(m irc.Message) {
	// Params: <text>
	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"WALLOPS", "Not enough parameters"})
		return
	}

	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	text := m.Params[0]

	for _, user := range u.Catbox.Opers {
		if !user.isLocal() {
			continue
		}
		user.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  user.nickUhost(),
			Command: "WALLOPS",
			Params:  []string{text},
		})
	}

	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "WALLOPS",
			Params:  []string{text},
		})
	}
}

func (u *LocalUser) killCommand(m irc.Message) {
	// Parameters: <target username> [reason]
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"KILL", "Not enough parameters"})
		return
	}

	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	targetUID, exists := u.Catbox.Nicks[canonicalizeNick(m.Params[0])]
	if !exists {
		// 401 ERR_NOSUCHNICK
		u.messageFromServer("401", []string{m.Params[0], "No such nick/channel"})
		return
	}
	targetUser := u.Catbox.Users[targetUID]

	reason := ""
	if len(m.Params) >= 2 && len(m.Params[1]) > 0 {
		reason = m.Params[1]
	} else {
		reason = "<No reason given>"
	}

	// Tell all opers about it.
	u.Catbox.noticeOpers(fmt.Sprintf("Received KILL message for %s. From %s (%s)",
		targetUser.DisplayNick, u.User.DisplayNick, reason))

	// If it's a local user, cut them off.
	if targetUser.isLocal() {
		targetUser.LocalUser.quit(fmt.Sprintf("Killed (%s (%s))",
			u.User.DisplayNick, reason), false)
	}

	// Propagate to all servers.
	// For server message, the reason string must look like this:
	// <source> (<Reason>)
	// Where source looks like:
	// <server name>!<user host>!<user>!<nick>
	serverReason := fmt.Sprintf("%s!%s!%s!%s (%s)",
		u.Catbox.Config.ServerName,
		u.User.Hostname,
		u.User.Username,
		u.User.DisplayNick,
		reason)
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "KILL",
			Params:  []string{string(targetUser.UID), serverReason},
		})
	}
}

// Apply a KLine (user ban) locally and cut off any users matching it.
//
// Propagate it to all servers.
//
// At this time we support only permanent (locally anyway) klines.
func (u *LocalUser) klineCommand(m irc.Message) {
	// Parameters: [duration] <user@host> <reason>
	if len(m.Params) < 2 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"KLINE", "Not enough parameters"})
		return
	}

	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	duration := "0"
	uhost := ""
	reason := ""

	match, err := regexp.MatchString("^[0-9]+$", m.Params[0])
	if err != nil {
		log.Fatalf("KLine duration regex: %s", err)
	}
	if match {
		duration = m.Params[0]

		if len(m.Params) < 3 {
			// 461 ERR_NEEDMOREPARAMS
			u.messageFromServer("461", []string{"KLINE", "Not enough parameters"})
			return
		}

		uhost = m.Params[1]
		reason = m.Params[2]
	} else {
		uhost = m.Params[0]
		reason = m.Params[1]
	}

	// Hostname regex leaves something to be desired.
	re := regexp.MustCompile("^([a-zA-Z0-9*?]+)@([a-zA-Z0-9.*?]+)$")
	matches := re.FindStringSubmatch(uhost)
	if matches == nil {
		// 415 ERR_BADMASK
		u.messageFromServer("415", []string{uhost, "Bad Server/host mask"})
		return
	}
	userMask := matches[1]
	hostMask := matches[2]

	kline := KLine{
		UserMask: userMask,
		HostMask: hostMask,
		Reason:   reason,
	}

	u.Catbox.addAndApplyKLine(kline, u.User.DisplayNick, reason)

	// Propagate.
	// In TS6 this must be in ENCAP.
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "ENCAP",
			Params: []string{
				"*",
				"KLINE",
				duration,
				userMask,
				hostMask,
				reason,
			},
		})
	}
}

func (u *LocalUser) unklineCommand(m irc.Message) {
	// Parameters: <usermask@hostmask>
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"UNKLINE", "Not enough parameters"})
		return
	}

	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	pieces := strings.Split(m.Params[0], "@")
	if len(pieces) != 2 {
		// 415 ERR_BADMASK
		u.messageFromServer("415", []string{m.Params[0], "Bad Server/host mask"})
		return
	}
	userMask := pieces[0]
	hostMask := pieces[1]

	u.Catbox.removeKLine(userMask, hostMask, u.User.DisplayNick)

	// Propagate.
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "ENCAP",
			Params: []string{
				"*",
				"UNKLINE",
				userMask,
				hostMask,
			},
		})
	}
}

// I support the following queries right now:
// k/K - Show K-Lines
// I do not support remote STATS yet.
func (u *LocalUser) statsCommand(m irc.Message) {
	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"STATS", "Not enough parameters"})
		return
	}

	query := m.Params[0]
	if query != "k" && query != "K" {
		u.messageFromServer("NOTICE", []string{"Unknown stats query"})
		return
	}

	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	// We could sort the KLines.

	for _, kline := range u.Catbox.KLines {
		// 216 RPL_STATSKLINE
		// RFC 1459 says:
		// K <host> * <username> <port> <class>
		// RFC 2812 declines to say.
		// ircd-ratbox says:
		// K <host> * <username> <reason>
		// I use ratbox's.
		u.messageFromServer("216", []string{
			"K",
			kline.HostMask,
			"*",
			kline.UserMask,
			kline.Reason,
		})
	}

	// 219 RPL_ENDOFSTATS
	u.messageFromServer("219", []string{"K", "End of /STATS report"})
}

// Reload config.
// No parameters.
func (u *LocalUser) rehashCommand(m irc.Message) {
	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	cfg, err := checkAndParseConfig(u.Catbox.ConfigFile)
	if err != nil {
		u.Catbox.noticeOpers(fmt.Sprintf("Rehash: Configuration problem: %s", err))
		return
	}

	// Only certain config options can change during rehash.

	// We could close listeners and open new ones. But nah.

	u.Catbox.Config.MOTD = cfg.MOTD
	u.Catbox.Config.Opers = cfg.Opers
	u.Catbox.Config.Servers = cfg.Servers

	u.Catbox.noticeOpers(fmt.Sprintf("%s rehashed configuration.",
		u.User.DisplayNick))
}
