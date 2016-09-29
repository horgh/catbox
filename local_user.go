package main

import (
	"fmt"
	"log"
	"net"
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

func (u *LocalUser) notice(s string) {
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

	return TS6UID(u.Catbox.Config.TS6SID + string(ts6id)), nil
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
	if !u.User.onChannel(channel) {
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
		u.User.messageUser(member, "PART", params)
	}

	// Remove the client from the channel.
	delete(channel.Members, u.User.UID)
	delete(u.User.Channels, channel.Name)

	// If they are the last member, then drop the channel completely.
	if len(channel.Members) == 0 {
		delete(u.Catbox.Channels, channel.Name)
	}
}

// Note: Only the server goroutine should call this (due to closing channel).
func (u *LocalUser) quit(msg string) {
	// May already be cleaning up.
	_, exists := u.Catbox.LocalUsers[u.ID]
	if !exists {
		return
	}

	// Tell all clients the client is in the channel with, and remove the client
	// from each channel it is in.

	// Tell each client only once.

	toldClients := map[TS6UID]struct{}{}

	for _, channel := range u.User.Channels {
		for memberUID := range channel.Members {
			_, exists := toldClients[memberUID]
			if exists {
				continue
			}

			member := u.Catbox.Users[memberUID]

			u.User.messageUser(member, "QUIT", []string{msg})

			toldClients[memberUID] = struct{}{}
		}

		delete(channel.Members, u.User.UID)
		if len(channel.Members) == 0 {
			delete(u.Catbox.Channels, channel.Name)
		}
	}

	// Ensure we tell the client (e.g., if in no channels).
	_, exists = toldClients[u.User.UID]
	if !exists {
		u.User.messageUser(u.User, "QUIT", []string{msg})
	}

	u.messageFromServer("ERROR", []string{msg})

	close(u.WriteChan)

	delete(u.Catbox.Nicks, canonicalizeNick(u.User.DisplayNick))

	delete(u.Catbox.LocalUsers, u.ID)

	if u.User.isOperator() {
		delete(u.Catbox.Opers, u.User.UID)
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

	// We need to inform other clients about the nick change.
	// Any that are in the same channel as this client.
	informedClients := map[TS6UID]struct{}{}
	for _, channel := range u.User.Channels {
		for memberUID := range channel.Members {
			// Tell each client only once.
			_, exists := informedClients[memberUID]
			if exists {
				continue
			}

			member := u.Catbox.Users[memberUID]

			// Message needs to come from the OLD nick.
			u.User.messageUser(member, "NICK", []string{nick})
			informedClients[member.UID] = struct{}{}
		}
	}

	// Reply to the client. We should have above, but if they were not on any
	// channels then we did not.
	_, exists = informedClients[u.User.UID]
	if !exists {
		u.User.messageUser(u.User, "NICK", []string{nick})
	}

	// Finally, make the update. Do this last as we need to ensure we act
	// as the old nick when crafting messages.
	u.User.DisplayNick = nick
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
	if len(m.Params) == 1 && m.Params[0] == "0" {
		for _, channel := range u.User.Channels {
			u.part(channel.Name, "")
		}
		return
	}

	// Again, we could check if there are too many parameters, but we just
	// ignore them.

	// NOTE: I choose to not support comma separated channels. RFC 2812
	//   allows multiple channels in a single command.

	channelName := canonicalizeChannel(m.Params[0])
	if !isValidChannel(channelName) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		u.messageFromServer("403", []string{channelName, "Invalid channel name"})
		return
	}

	// TODO: Support keys.

	// Try to join the client to the channel.

	// Is the client in the channel already?
	if u.User.onChannel(&Channel{Name: channelName}) {
		// 443 ERR_USERONCHANNEL
		// This error code is supposed to be for inviting a user on a channel
		// already, but it works.
		u.messageFromServer("443", []string{u.User.DisplayNick, channelName,
			"is already on channel"})
		return
	}

	// Look up / create the channel
	channel, exists := u.Catbox.Channels[channelName]
	if !exists {
		channel = &Channel{
			Name:    channelName,
			Members: make(map[TS6UID]struct{}),
		}
		u.Catbox.Channels[channelName] = channel
	}

	// Add the client to the channel.
	channel.Members[u.User.UID] = struct{}{}
	u.User.Channels[channelName] = channel

	// Tell the client about the join. This is what RFC says to send:
	// Send JOIN, RPL_TOPIC, and RPL_NAMREPLY.

	// JOIN comes from the client, to the client.
	u.User.messageUser(u.User, "JOIN", []string{channel.Name})

	// If this is a new channel, send them the modes we set by default.
	if !exists {
		u.messageFromServer("MODE", []string{channel.Name, "+ns"})
	}

	// It appears RPL_TOPIC is optional, at least ircd-ratbox does not send it.
	// Presumably if there is no topic.
	if len(channel.Topic) > 0 {
		// 332 RPL_TOPIC
		u.messageFromServer("332", []string{channel.Name, channel.Topic})
	}

	// Channel flag: = (public), * (private), @ (secret)
	// TODO: When we have more chan modes (-s / +p) this needs to vary
	channelFlag := "@"

	// RPL_NAMREPLY: This tells the client about who is in the channel
	// (including itself).
	// It ends with RPL_ENDOFNAMES.
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]
		// 353 RPL_NAMREPLY
		u.messageFromServer("353", []string{
			// TODO: We need to include @ / + for each nick opped/voiced.
			// TODO: Multiple nicks per RPL_NAMREPLY.
			channelFlag, channel.Name, fmt.Sprintf(":%s", member.DisplayNick),
		})
	}

	// 366 RPL_ENDOFNAMES
	u.messageFromServer("366", []string{channel.Name, "End of NAMES list"})

	// Tell each member in the channel about the client.
	for memberUID := range channel.Members {
		// Don't tell the client. We already did (above).
		if memberUID == u.User.UID {
			continue
		}

		member := u.Catbox.Users[memberUID]

		// From the client to each member.
		u.User.messageUser(member, "JOIN", []string{channel.Name})
	}
}

func (u *LocalUser) partCommand(m irc.Message) {
	// Parameters: <channel> *( "," <channel> ) [ <Part Message> ]

	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		u.messageFromServer("461", []string{"PART", "Not enough parameters"})
		return
	}

	// Again, we don't raise error if there are too many parameters.

	partMessage := ""
	if len(m.Params) >= 2 {
		partMessage = m.Params[1]
	}

	u.part(m.Params[0], partMessage)
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

	if len(m.Params) == 1 {
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
	msgLen := len(":") + len(u.User.nickUhost()) + len(" ") + len(m.Command) +
		len(" ") + len(target) + len(" ") + len(":") + len(msg) + len("\r\n")
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
		// TODO: Technically we should allow messaging if they aren't on it
		//   depending on the mode.
		if !u.User.onChannel(channel) {
			// 404 ERR_CANNOTSENDTOCHAN
			u.messageFromServer("404", []string{channelName, "Cannot send to channel"})
			return
		}

		u.LastMessageTime = time.Now()

		// Send to all members of the channel. Except the client itself it seems.
		for memberUID := range channel.Members {
			if memberUID == u.User.UID {
				continue
			}

			member := u.Catbox.Users[memberUID]

			// From the client to each member.
			u.User.messageUser(member, m.Command, []string{channel.Name, msg})
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

	u.User.messageUser(targetUser, m.Command, []string{nickName, msg})
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

	u.quit(msg)
}

func (u *LocalUser) pingCommand(m irc.Message) {
	// Parameters: <server> (I choose to not support forwarding)
	if len(m.Params) == 0 {
		// 409 ERR_NOORIGIN
		u.messageFromServer("409", []string{"No origin specified"})
		return
	}

	server := m.Params[0]

	if server != u.Catbox.Config.ServerName {
		// 402 ERR_NOSUCHSERVER
		u.messageFromServer("402", []string{server, "No such server"})
		return
	}

	u.messageFromServer("PONG", []string{server})
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
	nickCanonical := canonicalizeNick(nick)

	targetUID, exists := u.Catbox.Nicks[nickCanonical]
	if !exists {
		// 401 ERR_NOSUCHNICK
		u.messageFromServer("401", []string{nick, "No such nick/channel"})
		return
	}
	targetUser := u.Catbox.Users[targetUID]

	// 311 RPL_WHOISUSER
	u.messageFromServer("311", []string{
		targetUser.DisplayNick,
		targetUser.Username,
		fmt.Sprintf("%s", targetUser.Hostname),
		"*",
		targetUser.RealName,
	})

	// 319 RPL_WHOISCHANNELS
	// I choose to not show any.

	// 312 RPL_WHOISSERVER
	u.messageFromServer("312", []string{
		targetUser.DisplayNick,
		u.Catbox.Config.ServerName,
		u.Catbox.Config.ServerInfo,
	})

	// 301 RPL_AWAY
	// TODO: AWAY not implemented yet.

	// 313 RPL_WHOISOPERATOR
	if targetUser.isOperator() {
		u.messageFromServer("313", []string{
			targetUser.DisplayNick,
			"is an IRC operator",
		})
	}

	// TODO: TLS information

	// 317 RPL_WHOISIDLE
	// Only if local.
	if targetUser.LocalUser != nil {
		idleDuration := time.Now().Sub(targetUser.LocalUser.LastMessageTime)
		idleSeconds := int(idleDuration.Seconds())
		u.messageFromServer("317", []string{
			targetUser.DisplayNick,
			fmt.Sprintf("%d", idleSeconds),
			"seconds idle",
		})
	}

	// 318 RPL_ENDOFWHOIS
	u.messageFromServer("318", []string{
		targetUser.DisplayNick,
		"End of WHOIS list",
	})
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

	// TODO: Host matching

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
	u.User.messageUser(u.User, "MODE", []string{u.User.DisplayNick, "+o"})

	// 381 RPL_YOUREOPER
	u.messageFromServer("381", []string{"You are now an IRC operator"})
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

func (u *LocalUser) userModeCommand(targetUser *User, modes string) {
	// They can only change their own mode.
	if targetUser.LocalUser != u {
		// 502 ERR_USERSDONTMATCH
		u.messageFromServer("502", []string{"Cannot change mode for other users"})
		return
	}

	// No modes given means we should send back their current mode.
	if len(modes) == 0 {
		modeReturn := "+"
		for k := range u.User.Modes {
			modeReturn += string(k)
		}

		// 221 RPL_UMODEIS
		u.messageFromServer("221", []string{modeReturn})
		return
	}

	action := ' '
	for _, char := range modes {
		if char == '+' || char == '-' {
			action = char
			continue
		}

		if action == ' ' {
			// Malformed. No +/-.
			// 472 ERR_UNKNOWNMODE
			u.messageFromServer("472", []string{modes, "is unknown mode to me"})
			continue
		}

		// Only mode I support right now is 'o' (operator).
		// But some others I will ignore silently to avoid clients getting unknown
		// mode messages.
		if char == 'i' || char == 'w' || char == 's' {
			continue
		}

		if char != 'o' {
			// 501 ERR_UMODEUNKNOWNFLAG
			u.messageFromServer("501", []string{"Unknown MODE flag"})
			continue
		}

		// Ignore it if they try to +o (operator) themselves. RFC says to do so.
		if action == '+' {
			continue
		}

		// This is -o. They have to be operator for there to be any effect.
		if !u.User.isOperator() {
			continue
		}

		delete(u.User.Modes, 'o')
		delete(u.Catbox.Opers, u.User.UID)
		u.User.messageUser(u.User, "MODE", []string{"-o", u.User.DisplayNick})
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
	if len(topic) == 0 {
		topic = ":"
	}

	// TODO: If/when we have channel operators then we need additional logic

	channel.Topic = topic

	// Tell all members of the channel, including the client.
	for memberUID := range channel.Members {
		member := u.Catbox.Users[memberUID]
		// 332 RPL_TOPIC
		u.User.messageUser(member, "TOPIC", []string{channel.Name, channel.Topic})
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
	linkedAlready := false
	for _, server := range u.Catbox.Servers {
		if server.Name == serverName {
			linkedAlready = true
			break
		}
	}
	if linkedAlready {
		// No great error code.
		u.notice(fmt.Sprintf("I am already linked to %s.", serverName))
		return
	}

	// We could check if we're trying to link to it. But the result should be the
	// same.

	// Initiate a connection.
	// Put it in a goroutine to avoid blocking server goroutine.
	u.Catbox.WG.Add(1)
	go func() {
		defer u.Catbox.WG.Done()

		u.notice(fmt.Sprintf("Connecting to %s...", linkInfo.Name))

		conn, err := net.DialTimeout("tcp",
			fmt.Sprintf("%s:%d", linkInfo.Hostname, linkInfo.Port),
			u.Catbox.Config.DeadTime)
		if err != nil {
			log.Printf("Unable to connect to server [%s]: %s", linkInfo.Name, err)
			return
		}

		id := u.Catbox.getClientID()

		client := NewLocalClient(u.Catbox, id, conn)

		// Make sure we send to the client's write channel before telling the server
		// about the client. It is possible otherwise that the server (if shutting
		// down) could have closed the write channel on us.
		client.sendPASS(linkInfo.Pass)
		client.sendCAPAB()
		client.sendSERVER()

		client.Catbox.newEvent(Event{Type: NewClientEvent, Client: client})

		client.Catbox.WG.Add(1)
		go client.readLoop()
		client.Catbox.WG.Add(1)
		go client.writeLoop()
	}()
}

func (u *LocalUser) linksCommand(m irc.Message) {
	// Difference from RFC: No parameters respected.

	for _, s := range u.Catbox.Servers {
		// 364 RPL_LINKS
		// <mask> <server> :<hopcount> <server info>
		u.messageFromServer("364", []string{
			s.Name,
			s.Name,
			fmt.Sprintf("%d %s", s.Hopcount, s.Description),
		})
	}

	// 365 RPL_ENDOFLINKS
	// <mask> :End of LINKS list
	u.messageFromServer("365", []string{"*", "End of LINKS list"})
}
