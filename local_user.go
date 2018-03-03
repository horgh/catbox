package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/horgh/irc"
)

// LocalUser holds information relevant only to a regular user (non-server)
// client.
type LocalUser struct {
	// Local users are local clients and have the same attributes. Embed the type.
	*LocalClient

	// A reference to their user information.
	User *User

	// The last time we heard anything from the client.
	LastActivityTime time.Time

	// The last time we sent the client a PING.
	LastPingTime time.Time

	// The last time the client sent a PRIVMSG/NOTICE. We use this to decide
	// idle time.
	LastMessageTime time.Time

	// MessageCounter is part of flood control. It tells us how many messages we
	// have remaining before flood control kicks in. If it's 0, a message gets
	// queued.
	MessageCounter int

	// MessageQueue holds queued messages from the client.
	MessageQueue []irc.Message
}

// NewLocalUser makes a LocalUser from a LocalClient.
func NewLocalUser(c *LocalClient) *LocalUser {
	now := time.Now()

	u := &LocalUser{
		LocalClient:      c,
		LastActivityTime: now,
		LastPingTime:     now,
		LastMessageTime:  now,
		MessageCounter:   UserMessageLimit,
		MessageQueue:     []irc.Message{},
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
	channel, channelExists := u.Catbox.Channels[channelName]
	if !channelExists {
		channel = &Channel{
			Name:    channelName,
			Members: make(map[TS6UID]struct{}),
			Ops:     make(map[TS6UID]*User),
			Modes:   make(map[byte]struct{}),
			TS:      time.Now().Unix(),
		}
		u.Catbox.Channels[channelName] = channel
		channel.grantOps(u.User)
		channel.Modes['n'] = struct{}{}
		channel.Modes['s'] = struct{}{}
	}

	// Add them to the channel.
	channel.Members[u.User.UID] = struct{}{}
	u.User.Channels[channelName] = channel

	// Tell the client about the join.
	// This is what RFC says to send: JOIN, RPL_TOPIC, and RPL_NAMREPLY.

	// JOIN comes from the client, to the client.
	u.messageUser(u.User, "JOIN", []string{channel.Name})

	// If this is a new channel, send them the modes we set by default.
	if !channelExists {
		u.messageFromServer("MODE", []string{channel.Name, "+ns"})
	}

	// It appears RPL_TOPIC is optional, at least ircd-ratbox does always send it.
	// Presumably if there is no topic.
	if len(channel.Topic) > 0 {
		// 332 RPL_TOPIC
		u.messageFromServer("332", []string{channel.Name, channel.Topic})
		// 333 tells about who set the topic and topic TS (when set). This is not
		// standard.
		u.messageFromServer("333", []string{
			channel.Name,
			channel.TopicSetter,
			fmt.Sprintf("%d", channel.TopicTS),
		})
	}

	// 353 RPL_NAMREPLY: This tells the client about who is in the channel
	// (including itself).
	// Format: :<server> 353 <targetNick> <channel flag> <#channel> :<nicks>
	// <nicks> is a list of nicknames in the channel. Each is prefixed with @
	// or + to indicate opped/voiced). Apparently only one or the other.

	// Channel flag: = (public), * (private), @ (secret)
	// When we have more chan modes (-s / +p) this needs to vary
	channelFlag := "@"

	// We put as many nicks per line as possible.

	// First build the portion that is common to every NAMREPLY so we can get
	// its length.
	namMessage := irc.Message{
		Prefix:  string(u.Catbox.Config.ServerName),
		Command: "353",
		// Last parameter is where nicks go. We'll have " :" since it's blank
		// right now (when we encode to determine base size).
		Params: []string{u.User.DisplayNick, channelFlag, channel.Name, ""},
	}

	// If encoding the message truncates before we add any nicks, then there is no
	// point continuing.
	messageBuf, err := namMessage.Encode()
	if err != nil {
		log.Printf("Unable to generate RPL_NAMREPLY: %s", err)
		return
	}

	baseSize := len(messageBuf)

	nicks := ""
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]

		// We send the nick with its mode prefix.
		sendNick := member.DisplayNick
		if channel.userHasOps(member) {
			sendNick = "@" + sendNick
		}

		// Assume 1 nick will always be okay to send.
		if len(nicks) == 0 {
			nicks += sendNick
			continue
		}

		// If we add another nick, will we be above our line length? If so, fire off
		// the message and start with the nick in a new list.
		// +1 for " "
		if baseSize+len(nicks)+1+len(member.DisplayNick) > irc.MaxLineLength {
			namMessage.Params[3] = nicks
			u.maybeQueueMessage(namMessage)
			nicks = "" + sendNick
			continue
		}

		nicks += " " + sendNick
	}

	if len(nicks) > 0 {
		namMessage.Params[3] = nicks
		u.maybeQueueMessage(namMessage)
	}

	// 366 RPL_ENDOFNAMES: Ends NAMES list.
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
		if !channelExists {
			server.maybeQueueMessage(irc.Message{
				Prefix:  string(u.Catbox.Config.TS6SID),
				Command: "SJOIN",
				Params: []string{
					fmt.Sprintf("%d", channel.TS),
					channel.Name,
					"+ns",
					"@" + string(u.User.UID),
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
	channel.removeUser(u.User)

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
	log.Printf("Losing user %s", u)

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

		channel.removeUser(u.User)
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
	delete(u.Catbox.Users, u.User.UID)
}

// Set the user away. We've been given a non-blank message.
func (u *LocalUser) setAway(message string) {
	// Flag him as being away
	u.User.AwayMessage = message

	// Reply to the user.

	// 306 RPL_NOWAWAY
	u.maybeQueueMessage(irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
		Command: "306",
		Params:  []string{u.User.DisplayNick, "You have been marked as away"},
	})

	// Propagate.
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "AWAY",
			Params:  []string{message},
		})
	}
}

// Set the user back from away.
func (u *LocalUser) setUnaway() {
	// If they're not away, don't do anything.
	if u.User.AwayMessage == "" {
		return
	}

	// Flag him as back.
	u.User.AwayMessage = ""

	// 305 RPL_UNAWAY
	u.maybeQueueMessage(irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
		Command: "305",
		Params: []string{
			u.User.DisplayNick,
			"You are no longer been marked as being away",
		},
	})

	// Propagate.
	for _, server := range u.Catbox.LocalServers {
		server.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "AWAY",
			Params:  []string{},
		})
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

	// Flood protection. If we've used all our available message space for now,
	// queue it.
	if !u.User.isFloodExempt() {
		if u.MessageCounter == 0 {
			log.Printf("%s is flooding. Queueing their message.", u.User.DisplayNick)
			u.MessageQueue = append(u.MessageQueue, m)

			// Check for overwhelming their queue and disconnect them if so.
			if len(u.MessageQueue) >= ExcessFloodThreshold {
				u.quit("Excess flood", true)
				return
			}

			return
		}
		u.MessageCounter--
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

	if m.Command == "RESTART" {
		u.restartCommand(m)
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

	if m.Command == "MAP" {
		u.mapCommand(m)
		return
	}

	if m.Command == "VERSION" {
		u.versionCommand(m)
		return
	}

	if m.Command == "TIME" {
		u.timeCommand(m)
		return
	}

	if m.Command == "WHOWAS" {
		u.whowasCommand(m)
		return
	}

	if m.Command == "AWAY" {
		u.awayCommand(m)
		return
	}

	if m.Command == "INVITE" {
		u.inviteCommand(m)
		return
	}

	if m.Command == "OPME" {
		u.opmeCommand(m)
		return
	}

	if m.Command == "SQUIT" {
		u.squitCommand(m)
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

	// Truncate and validate the nick.

	if len(nick) > u.Catbox.Config.MaxNickLength {
		nick = nick[0:u.Catbox.Config.MaxNickLength]
	}

	if !isValidNick(u.Catbox.Config.MaxNickLength, nick) {
		// 432 ERR_ERRONEUSNICKNAME
		u.messageFromServer("432", []string{nick, "Erroneous nickname"})
		return
	}

	// Ignore the command if it's the exact same as the current nick.
	// This is a case sensitive comparison.
	if nick == u.User.DisplayNick {
		return
	}

	newNickCanon := canonicalizeNick(nick)
	oldNickCanon := canonicalizeNick(u.User.DisplayNick)

	// Nick must be unique. However, allow them to change their nick to a
	// different case. e.g. "user" may change to "User", but no one else may.
	if newNickCanon != oldNickCanon {
		_, exists := u.Catbox.Nicks[newNickCanon]
		if exists {
			// 433 ERR_NICKNAMEINUSE
			u.messageFromServer("433", []string{nick, "Nickname is already in use"})
			return
		}
	}

	// Free the old nick.
	delete(u.Catbox.Nicks, oldNickCanon)

	// Flag the nick as taken by this client.
	u.Catbox.Nicks[newNickCanon] = u.User.UID

	// Nick TS changes when nick is set.
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
	_, exists := informedClients[u.User.UID]
	if !exists {
		u.messageUser(u.User, "NICK", []string{nick})
	}

	// Finally, make the update. Do this last as we need to ensure we act as the
	// old nick when crafting messages.
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

	// Are we messaging a channel? Note I only support # channels right now.
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

	// Reply with 301 RPL_AWAY if they're away.
	if len(targetUser.AwayMessage) > 0 {
		u.maybeQueueMessage(irc.Message{
			Prefix:  u.Catbox.Config.ServerName,
			Command: "301",
			Params: []string{
				u.User.DisplayNick,
				targetUser.DisplayNick,
				targetUser.AwayMessage,
			},
		})
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
			len(u.Catbox.Servers)+1),
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

	// 265 tells current local user count and max. Not standard.
	u.messageFromServer("265", []string{
		fmt.Sprintf("%d", len(u.Catbox.LocalUsers)),
		fmt.Sprintf("%d", u.Catbox.HighestLocalUserCount),
		fmt.Sprintf("Current local users %d, max %d",
			len(u.Catbox.LocalUsers), u.Catbox.HighestLocalUserCount),
	})

	// 266 tells global user count and max. Not standard.
	u.messageFromServer("266", []string{
		fmt.Sprintf("%d", len(u.Catbox.Users)),
		fmt.Sprintf("%d", u.Catbox.HighestGlobalUserCount),
		fmt.Sprintf("Current global users %d, max %d",
			len(u.Catbox.Users), u.Catbox.HighestGlobalUserCount),
	})

	// 250 tells highest total connections, highest total local users (again, it
	// does seem like ratbox does this), and the total number of connections
	// received. Again this is not standard, but interesting.
	u.messageFromServer("250", []string{
		fmt.Sprintf("Highest connection count: %d (%d clients) (%d connections received)",
			u.Catbox.HighestConnectionCount, u.Catbox.HighestLocalUserCount,
			u.Catbox.ConnectionCount),
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

	// Clients may send PING where the origin server (param 0) is nonsense.
	//
	// For example, Quassel sends a timestamp like "PING 22:46:48.650". Which is
	// even more interesting when we consider it has : in it, leading to us to
	// have issues saying "<timestamp> :is not a valid server" (as : is then in
	// the first parameter) if we claim it to be invalid.
	//
	// Let's act as if the parameter makes sense. Reply to it as if it is a
	// server name.
	//
	// Also, not sending back param 0 will confuse mIRC. It will show the PONG
	// coming from the server in its status.
	//
	// :<us> PONG <source, us> <server we are replying to, argument 0>

	u.messageFromServer("PONG", []string{u.Catbox.Config.ServerName, m.Params[0]})
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

func (u *LocalUser) restartCommand(m irc.Message) {
	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	u.Catbox.restart(u.User)
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

	u.Catbox.noticeLocalOpers(fmt.Sprintf("%s@%s became an operator.",
		u.User.DisplayNick, u.Catbox.Config.ServerName))
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
		params := []string{}
		if len(m.Params) > 2 {
			params = append(params, m.Params[2:]...)
		}
		u.channelModeCommand(targetChannel, modes, params)
		return
	}

	// Well... Not found. Send a channel not found. It seems the closest matching
	// extant error in RFC.
	// 403 ERR_NOSUCHCHANNEL
	u.messageFromServer("403", []string{target, "No such channel"})
}

// Modes we support at this time:
// +i/-i (invisible, actually doesn't change anything for this server, but)
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

// We've found a MODE message is about a channel.
func (u *LocalUser) channelModeCommand(channel *Channel, modes string,
	params []string) {
	if !u.User.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		u.messageFromServer("442", []string{channel.Name,
			"You're not on that channel"})
		return
	}

	// No modes? Send back the channel's modes.
	// Always send back +ns. That's only I support right now.
	if len(modes) == 0 {
		// 324 RPL_CHANNELMODEIS
		u.messageFromServer("324", []string{channel.Name, "+ns"})
		// 329 RPL_CREATIONTIME. Not standard but oft used.
		u.messageFromServer("329", []string{channel.Name,
			fmt.Sprintf("%d", channel.TS)})
		return
	}

	// Listing bans. I don't support bans at this time. Say that there are none.
	if modes == "b" || modes == "+b" {
		// 368 RPL_ENDOFBANLIST
		u.messageFromServer("368", []string{channel.Name,
			"End of channel ban list"})
		return
	}

	// This is a channel mode change.
	// They must be channel operator.
	if !channel.userHasOps(u.User) {
		// 482 ERR_CHANOPRIVSNEEDED
		u.messageFromServer("482", []string{channel.Name,
			"You're not channel operator"})
		return
	}

	// Apply mode changes we support.
	// Currently I support:
	// - +o/-o
	// Also generate the information we need to send to our local users and to
	// servers.

	// +/-
	action := '+'

	// Count how many modes we apply.
	// We support only a limited number per command.
	modesApplied := 0

	// Track the mode string of the modes we actually apply.
	appliedModes := ""

	// Track the current action in our applied modes string.
	appliedModesAction := ' '

	// Track the parameters of the modes we actually apply.
	appliedParamsUser := []string{}
	appliedParamsServer := []string{}

	// Track what parameter we're on (of those presented).
	// i.e., if we had "+oo u1 u2", then we start out at index 0 pointing
	// to u1, then index 1 indicating u2.
	paramIndex := 0

	for _, char := range modes {
		if modesApplied >= ChanModesPerCommand {
			break
		}

		if char == '+' || char == '-' {
			action = char
			continue
		}

		if char != 'o' {
			continue
		}

		// +o/-o

		// Must have a parameter. A nick.
		if paramIndex >= len(params) {
			break
		}

		// Consume the parameter.
		targetNick := params[paramIndex]
		paramIndex++

		// Resolve the nick to a user.
		targetUID, exists := u.Catbox.Nicks[canonicalizeNick(targetNick)]
		if !exists {
			break
		}
		targetUser := u.Catbox.Users[targetUID]

		if !targetUser.onChannel(channel) {
			break
		}

		// Looks okay to do this.

		if action == '+' {
			if channel.userHasOps(targetUser) {
				break
			}
			channel.grantOps(targetUser)
		} else {
			if !channel.userHasOps(targetUser) {
				break
			}
			channel.removeOps(targetUser)
		}

		if appliedModesAction != action {
			appliedModesAction = action
			appliedModes += string(appliedModesAction)
		}

		appliedModes += string(char)
		appliedParamsUser = append(appliedParamsUser, targetUser.DisplayNick)
		appliedParamsServer = append(appliedParamsServer, string(targetUser.UID))

		modesApplied++
	}

	// If we didn't apply any changes, then we're done.
	if modesApplied == 0 {
		return
	}

	// Tell all local users in the channel about the mode changes.

	userModeParams := []string{channel.Name, appliedModes}
	userModeParams = append(userModeParams, appliedParamsUser...)

	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]

		if !member.isLocal() {
			continue
		}

		member.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  u.User.nickUhost(),
			Command: "MODE",
			Params:  userModeParams,
		})
	}

	// Propagate mode changes everywhere.

	serverModeParams := []string{
		fmt.Sprintf("%d", channel.TS),
		channel.Name,
		appliedModes,
	}
	serverModeParams = append(serverModeParams, appliedParamsServer...)

	for _, ls := range u.Catbox.LocalServers {
		ls.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "TMODE",
			Params:  serverModeParams,
		})
	}
}

func (u *LocalUser) whoCommand(m irc.Message) {
	// Contrary to RFC 2812, I support only 'WHO #channel'.
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	// Special case: OPERSPY of a kind. This will let the oper see all users.
	if m.Params[0] == "!*" {
		u.operspyWhoCommand(m)
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
		// Maybe "H" means here, "G" means gone.

		mode := "H"

		// If away, mode is G.
		if len(member.AwayMessage) > 0 {
			mode = "G"
		}

		if member.isOperator() {
			mode += "*"
		}

		if channel.userHasOps(member) {
			mode += "@"
		}

		serverName := u.Catbox.Config.ServerName
		if member.isRemote() {
			serverName = member.Server.Name
		}

		u.messageFromServer("352", []string{
			channel.Name,
			member.Username,
			member.Hostname,
			serverName,
			member.DisplayNick,
			mode,
			fmt.Sprintf("%d %s", member.HopCount, member.RealName),
		})
	}

	// 315 RPL_ENDOFWHO
	u.messageFromServer("315", []string{channel.Name, "End of WHO list"})
}

// This is only available to opers.
// It is to partially support something like ratbox's WHO !<param> command
// that lets opers see things regular users cannot.
// In this case, I want to send the WHO result of all users to the oper.
func (u *LocalUser) operspyWhoCommand(m irc.Message) {
	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	// Tell them every user.
	for _, user := range u.Catbox.Users {
		// 352 RPL_WHOREPLY
		// "<channel> <user> <host> <server> <nick>
		// ( "H" / "G" > ["*"] [ ( "@" / "+" ) ]
		// :<hopcount> <real name>"

		mode := "H"
		// If away, mode is G.
		if len(user.AwayMessage) > 0 {
			mode = "G"
		}

		if user.isOperator() {
			mode += "*"
		}

		serverName := u.Catbox.Config.ServerName
		if user.isRemote() {
			serverName = user.Server.Name
		}

		u.messageFromServer("352", []string{
			// * for name.
			"*",
			user.Username,
			user.Hostname,
			serverName,
			user.DisplayNick,
			mode,
			fmt.Sprintf("%d %s", user.HopCount, user.RealName),
		})
	}

	// 315 RPL_ENDOFWHO
	u.messageFromServer("315", []string{"*", "End of WHO list"})

	u.Catbox.noticeOpers(fmt.Sprintf("%s used OPERSPY WHO !*",
		u.User.DisplayNick))
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

		// 333 tells about who set the topic and topic TS (when set). This is not
		// standard.
		u.messageFromServer("333", []string{
			channel.Name,
			channel.TopicSetter,
			fmt.Sprintf("%d", channel.TopicTS),
		})
		return
	}

	topic := m.Params[1]
	if len(topic) > maxTopicLength {
		topic = topic[:maxTopicLength]
	}

	// TODO: When we support channel mode +t we will need additional logic.

	// Set new topic.

	channel.Topic = topic
	channel.TopicTS = time.Now().Unix()
	channel.TopicSetter = u.User.nickUhost()

	// Tell all members of the channel, including the client.
	// Only local clients. We tell remote users by telling all servers.
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]
		if !member.isLocal() {
			continue
		}

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
			"*",
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

	u.Catbox.issueKill(u.User, targetUser, reason)
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

	pieces := strings.Split(uhost, "@")
	if len(pieces) != 2 {
		// 415 ERR_BADMASK
		u.messageFromServer("415", []string{uhost, "Bad Server/host mask"})
		return
	}

	if !isValidUserMask(pieces[0]) ||
		!isValidHostMask(pieces[1]) {
		// 415 ERR_BADMASK
		u.messageFromServer("415", []string{uhost, "Bad Server/host mask"})
		return
	}

	userMask := pieces[0]
	hostMask := pieces[1]

	kline := KLine{
		UserMask: userMask,
		HostMask: hostMask,
		Reason:   reason,
	}

	// Propagate.
	// In TS6 this must be in ENCAP.
	// Do this before applying K-Line locally for the hopefully rare scenario
	// that the user K-Lines himself.
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

	u.Catbox.addAndApplyKLine(kline, u.User.DisplayNick, reason)
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

	u.Catbox.rehash(u.User)
}

// Map is a non standard command. It shows linked servers, and in an ASCII way,
// which is linked to which. Like a server map. We also show the server SIDs
// and how many users (and what % of the global count) each has.
//
// If we are in a network linked this way:
// me -> server A
// me -> server B
// server A -> server C
// server B -> server D
//
// Then output looks like this
//
// me[SID] ----------------- | Users: n (n.n%)
//   server A[SID] --------- | Users: n (n.n%)
//     server C[SID] ------- | Users: n (n.n%)
//   server B[SID] --------- | Users: n (n.n%)
//     server D[SID] ------- | Users: n (n.n%)
func (u *LocalUser) mapCommand(m irc.Message) {
	lines := []string{}

	globalUserCount := len(u.Catbox.Users)

	// Ourself.
	lines = append(lines, serverToMapLine(u.Catbox.Config.ServerName,
		u.Catbox.Config.TS6SID, len(u.Catbox.LocalUsers), globalUserCount, 0))

	for _, ls := range u.Catbox.LocalServers {
		// The local server.
		lines = append(lines, serverToMapLine(ls.Server.Name, ls.Server.SID,
			ls.Server.getLocalUserCount(u.Catbox.Users), globalUserCount,
			ls.Server.HopCount))

		// And all servers it is linked to.
		linkedServers := ls.Server.getLinkedServers(u.Catbox.Servers)
		for _, s := range linkedServers {
			lines = append(lines, serverToMapLine(s.Name, s.SID,
				s.getLocalUserCount(u.Catbox.Users), globalUserCount, s.HopCount))
		}
	}

	msgs := []irc.Message{}
	for _, line := range lines {
		msgs = append(msgs, irc.Message{
			Prefix:  u.Catbox.Config.ServerName,
			Command: "015",
			Params:  []string{u.User.DisplayNick, line},
		})
	}

	msgs = append(msgs, irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
		Command: "017",
		Params:  []string{u.User.DisplayNick, "End of /MAP"},
	})

	for _, msg := range msgs {
		u.maybeQueueMessage(msg)
	}
}

// Reply with version information.
// Parameters: None (that I accept, RFC specifies you can query remote server).
func (u *LocalUser) versionCommand(m irc.Message) {
	// 351 RPL_VERSION
	// <version>.<debuglevel> <server name> :<comments>
	// Apparently <debuglevel> to be blank if not debug.
	// Comments are free form. But I use similar to what ratbox does. See its doc
	// server-version-info.

	version := fmt.Sprintf("%s.", Version)

	// H HUB, M IDLE_FROM_MSG, TS supports TS, 6 TS6, o TS only
	comments := fmt.Sprintf("HM TS6o %s", string(u.Catbox.Config.TS6SID))

	u.maybeQueueMessage(irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
		Command: "351",
		Params: []string{
			u.User.DisplayNick,
			version,
			u.Catbox.Config.ServerName,
			comments,
		},
	})
}

// Send back current time.
// No parameter supported.
func (u *LocalUser) timeCommand(m irc.Message) {
	// 391 RPL_TIME
	u.maybeQueueMessage(irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
		Command: "391",
		Params: []string{
			u.User.DisplayNick,
			u.Catbox.Config.ServerName,
			time.Now().Format(time.RFC1123),
		},
	})
}

// WHOWAS is to look up previously used nick information.
// I choose to not really implement it. Instead we always reply in the negative.
func (u *LocalUser) whowasCommand(m irc.Message) {
	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"WHOWAS", "Not enough parameters"})
		return
	}

	nick := m.Params[0]

	// 406 ERR_WASNOSUCHNICK
	u.maybeQueueMessage(irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
		Command: "406",
		Params:  []string{u.User.DisplayNick, nick, "There was no such nickname"},
	})

	// 369 RPL_ENDOFWHOWAS
	u.maybeQueueMessage(irc.Message{
		Prefix:  u.Catbox.Config.ServerName,
		Command: "369",
		Params:  []string{u.User.DisplayNick, nick, "End of WHOWAS"},
	})
}

// Set yourself away by including a message.
// Set yourself not away by not including a message, or having a blank message.
// Parameters: [message]
func (u *LocalUser) awayCommand(m irc.Message) {
	if len(m.Params) == 0 || len(m.Params[0]) == 0 {
		u.setUnaway()
		return
	}

	u.setAway(m.Params[0])
}

// Invite a user to a channel.
// Parameters: <nick> <channel>
// You must be on the channel.
// If the channel is +i, you must have ops. Actually when we have ops, it is
// probably better to always require ops to invite.
// If the nick is on the channel, error.
func (u *LocalUser) inviteCommand(m irc.Message) {
	if len(m.Params) < 2 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"INVITE", "Not enough parameters"})
		return
	}

	nick := m.Params[0]
	channelName := m.Params[1]

	// Find the target user.
	targetUID, exists := u.Catbox.Nicks[canonicalizeNick(nick)]
	if !exists {
		// 401 ERR_NOSUCHNICK
		u.messageFromServer("401", []string{nick, "No such nick/channel"})
		return
	}

	targetUser := u.Catbox.Users[targetUID]

	// Find the channel.
	channel, exists := u.Catbox.Channels[canonicalizeChannel(channelName)]
	if !exists {
		// Just say they're not on channel. There's a no such channel reply but
		// RFC 1459 doesn't include that as valid reply. Maybe for privacy.
		// 442 ERR_NOTONCHANNEL
		u.messageFromServer("442", []string{channelName,
			"You're not on that channel"})
		return
	}

	// Channel exists. Check the person doing the inviting is in it.
	_, onChannel := channel.Members[u.User.UID]
	if !onChannel {
		// 442 ERR_NOTONCHANNEL
		u.messageFromServer("442", []string{channelName,
			"You're not on that channel"})
	}

	// Is the user we wish to invite already in the channel?
	_, onChannel = channel.Members[targetUser.UID]
	if onChannel {
		// 443 ERR_USERONCHANNEL
		u.messageFromServer("443", []string{targetUser.DisplayNick, channel.Name,
			"is already on channel"})
		return
	}

	// We may try to invite.

	// They must have ops to do this.
	if !channel.userHasOps(u.User) {
		// 482 ERR_CHANOPRIVSNEEDED
		u.messageFromServer("482", []string{channel.Name,
			"You're not channel operator"})
		return
	}

	// Send an invite message.
	if targetUser.isLocal() {
		targetUser.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  u.User.nickUhost(),
			Command: "INVITE",
			Params:  []string{targetUser.DisplayNick, channel.Name},
		})
	} else {
		targetUser.ClosestServer.maybeQueueMessage(irc.Message{
			Prefix:  string(u.User.UID),
			Command: "INVITE",
			Params: []string{
				string(targetUser.UID),
				channel.Name,
				fmt.Sprintf("%d", channel.TS),
			},
		})
	}

	// Reply to the user.

	// First tell them we're inviting the user.
	// 341 RPL_INVITING
	u.messageFromServer("341", []string{channel.Name, targetUser.DisplayNick})

	// Second tell them if the user is away.
	if len(targetUser.AwayMessage) > 0 {
		u.maybeQueueMessage(irc.Message{
			Prefix:  u.Catbox.Config.ServerName,
			Command: "301",
			Params: []string{
				u.User.DisplayNick,
				targetUser.DisplayNick,
				targetUser.AwayMessage,
			},
		})
	}
}

// OPME is an operator command to grant them ops in a channel.
// Params: <channel>
func (u *LocalUser) opmeCommand(m irc.Message) {
	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"OPME", "Not enough parameters"})
		return
	}

	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	channel, exists := u.Catbox.Channels[canonicalizeChannel(m.Params[0])]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL.
		u.messageFromServer("403", []string{m.Params[0], "Invalid channel name"})
		return
	}

	if channel.userHasOps(u.User) {
		return
	}

	channel.grantOps(u.User)

	// Tell local users in the channel.
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]

		if !member.isLocal() {
			continue
		}

		member.LocalUser.maybeQueueMessage(irc.Message{
			Prefix:  u.Catbox.Config.ServerName,
			Command: "MODE",
			Params:  []string{channel.Name, "+o", u.User.DisplayNick},
		})
	}

	// Propagate to servers.
	for _, ls := range u.Catbox.LocalServers {
		ls.maybeQueueMessage(irc.Message{
			Prefix:  string(u.Catbox.Config.TS6SID),
			Command: "TMODE",
			Params: []string{
				fmt.Sprintf("%d", channel.TS),
				channel.Name,
				"+o",
				string(u.User.UID),
			},
		})
	}

	// Tell operators.
	u.Catbox.noticeOpers(fmt.Sprintf("%s used OPME in %s", u.User.DisplayNick,
		channel.Name))
}

func (u *LocalUser) squitCommand(m irc.Message) {
	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"SQUIT", "Not enough parameters"})
		return
	}
	serverName := m.Params[0]
	reason := "No reason given"
	if len(m.Params) > 1 {
		reason = m.Params[1]
	}

	if !u.User.isOperator() {
		// 481 ERR_NOPRIVILEGES
		u.messageFromServer("481", []string{
			"Permission Denied- You're not an IRC operator"})
		return
	}

	var server *Server
	for _, s := range u.Catbox.Servers {
		if s.Name == serverName {
			server = s
			break
		}
	}

	if server == nil {
		// 402 ERR_NOSUCHSERVER
		u.messageFromServer("402", []string{serverName, "No such server"})
		return
	}

	if server.isLocal() {
		server.LocalServer.quit(fmt.Sprintf("%s issued SQUIT: %s",
			u.User.DisplayNick, reason))
		return
	}

	server.ClosestServer.maybeQueueMessage(irc.Message{
		Prefix:  string(u.User.UID),
		Command: "SQUIT",
		Params:  []string{string(server.SID), reason},
	})
}
