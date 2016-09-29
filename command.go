package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"summercat.com/irc"
)

// The NICK command to happen both at connection registration time and
// after. There are different rules.
func (c *LocalClient) nickCommand(m irc.Message) {
	// We should have one parameter: The nick they want.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		c.messageFromServer("431", []string{"No nickname given"})
		return
	}
	nick := m.Params[0]

	if len(nick) > c.Catbox.Config.MaxNickLength {
		nick = nick[0:c.Catbox.Config.MaxNickLength]
	}

	if !isValidNick(c.Catbox.Config.MaxNickLength, nick) {
		// 432 ERR_ERRONEUSNICKNAME
		c.messageFromServer("432", []string{nick, "Erroneous nickname"})
		return
	}

	nickCanon := canonicalizeNick(nick)

	// Nick must be unique.
	_, exists := c.Catbox.Nicks[nickCanon]
	if exists {
		// 433 ERR_NICKNAMEINUSE
		c.messageFromServer("433", []string{nick, "Nickname is already in use"})
		return
	}

	// NOTE: I no longer flag the nick as taken until registration completes.
	//   Simpler.

	c.PreRegDisplayNick = nick

	// We don't reply during registration (we don't have enough info, no uhost
	// anyway).

	// If we have USER done already, then we're done registration.
	if len(c.PreRegUser) > 0 {
		c.completeRegistration()
	}
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
	u.Catbox.Nicks[nickCanon] = u.UID
	oldDisplayNick := u.DisplayNick

	// Free the old nick.
	delete(u.Catbox.Nicks, canonicalizeNick(oldDisplayNick))

	// We need to inform other clients about the nick change.
	// Any that are in the same channel as this client.
	informedClients := map[TS6UID]struct{}{}
	for _, channel := range u.Channels {
		for memberUID := range channel.Members {
			// Tell each client only once.
			_, exists := informedClients[memberUID]
			if exists {
				continue
			}

			member := u.Catbox.Users[memberUID]

			// Message needs to come from the OLD nick.
			u.messageUser(member, "NICK", []string{nick})
			informedClients[member.UID] = struct{}{}
		}
	}

	// Reply to the client. We should have above, but if they were not on any
	// channels then we did not.
	_, exists = informedClients[u.UID]
	if !exists {
		u.messageClient(u, "NICK", []string{nick})
	}

	// Finally, make the update. Do this last as we need to ensure we act
	// as the old nick when crafting messages.
	u.DisplayNick = nick
}

func (c *LocalClient) userCommand(m irc.Message) {
	// RFC RECOMMENDs NICK before USER. But I'm going to allow either way now.
	// One reason to do so is how to react if NICK was taken and client
	// proceeded to USER.

	// 4 parameters: <user> <mode> <unused> <realname>
	if len(m.Params) != 4 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	user := m.Params[0]

	if len(user) > c.Catbox.Config.MaxNickLength {
		user = user[0:c.Catbox.Config.MaxNickLength]
	}

	if !isValidUser(c.Catbox.Config.MaxNickLength, user) {
		// There isn't an appropriate response in the RFC. ircd-ratbox sends an
		// ERROR message. Do that.
		c.messageFromServer("ERROR", []string{"Invalid username"})
		return
	}
	c.PreRegUser = user

	// We could do something with user mode here.

	// Validate realname.
	// Arbitrary. Length only.
	if len(m.Params[3]) > 64 {
		c.messageFromServer("ERROR", []string{"Invalid realname"})
		return
	}
	c.PreRegRealName = m.Params[3]

	// If we have a nick, then we're done registration.
	if len(c.PreRegDisplayNick) > 0 {
		c.completeRegistration()
	}
}

// The USER command only occurs during connection registration.
func (u *LocalUser) userCommand(m irc.Message) {
	// 462 ERR_ALREADYREGISTRED
	u.messageFromServer("462", []string{"Unauthorized command (already registered)"})
}

func (c *LocalClient) passCommand(m irc.Message) {
	// For server registration:
	// PASS <password>, TS, <ts version>, <SID>
	if len(m.Params) < 4 {
		// For now I only recognise this form of PASS.
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"PASS", "Not enough parameters"})
		return
	}

	if c.GotPASS {
		c.quit("Double PASS")
		return
	}

	// We can't validate password yet.

	if m.Params[1] != "TS" {
		c.quit("Unexpected PASS format: TS")
		return
	}

	tsVersion, err := strconv.ParseInt(m.Params[2], 10, 64)
	if err != nil {
		c.quit("Unexpected PASS format: Version: " + err.Error())
		return
	}

	// Support only TS 6.
	if tsVersion != 6 {
		c.quit("Unsupported TS version")
		return
	}

	// Beyond format, we can't validate SID yet.
	if !isValidSID(m.Params[3]) {
		c.quit("Malformed SID")
		return
	}

	// Everything looks OK. Store them.

	c.PreRegPass = m.Params[0]
	c.PreRegTS6SID = m.Params[3]

	c.GotPASS = true

	// Don't reply yet.
}

func (c *LocalClient) capabCommand(m irc.Message) {
	// CAPAB <space separated list>
	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"CAPAB", "Not enough parameters"})
		return
	}

	if !c.GotPASS {
		c.quit("PASS first")
		return
	}

	if c.GotCAPAB {
		c.quit("Double CAPAB")
		return
	}

	capabs := strings.Split(m.Params[0], " ")

	// No real validation to do on these right now. Just record them.

	for _, cap := range capabs {
		cap = strings.TrimSpace(cap)
		if len(cap) == 0 {
			continue
		}

		cap = strings.ToUpper(cap)

		c.PreRegCapabs[cap] = struct{}{}
	}

	// For TS6 we must have QS and ENCAP.

	_, exists := c.PreRegCapabs["QS"]
	if !exists {
		c.quit("Missing QS")
		return
	}

	_, exists = c.PreRegCapabs["ENCAP"]
	if !exists {
		c.quit("Missing ENCAP")
		return
	}

	c.GotCAPAB = true
}

func (c *LocalClient) serverCommand(m irc.Message) {
	// SERVER <name> <hopcount> <description>
	if len(m.Params) != 3 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"SERVER", "Not enough parameters"})
		return
	}

	if !c.GotCAPAB {
		c.quit("CAPAB first.")
		return
	}

	if c.GotSERVER {
		c.quit("Double SERVER.")
		return
	}

	// We could validate the hostname format. But we have a list of hosts we will
	// link to, so check against that directly.
	linkInfo, exists := c.Catbox.Config.Servers[m.Params[0]]
	if !exists {
		c.quit("I don't know you")
		return
	}

	// At this point we should have a password from the PASS command. Check it.
	if linkInfo.Pass != c.PreRegPass {
		c.quit("Bad password")
		return
	}

	// Hopcount should be 1.
	if m.Params[1] != "1" {
		c.quit("Bad hopcount")
		return
	}

	// Is this server already linked?
	_, exists = c.Catbox.Servers[TS6SID(m.Params[0])]
	if exists {
		c.quit("Already linked")
		return
	}

	c.PreRegServerName = m.Params[0]
	c.PreRegServerDesc = m.Params[2]

	c.GotSERVER = true

	// Reply. Our reply differs depending on whether we initiated the link.

	// If they initiated the link, then we reply with PASS/CAPAB/SERVER.
	// If we did, then we already sent PASS/CAPAB/SERVER. Reply with SVINFO
	// instead.

	if !c.SentSERVER {
		c.sendPASS(linkInfo.Pass)
		c.sendCAPAB()
		c.sendSERVER()
		return
	}

	c.sendSVINFO()
}

func (c *LocalClient) svinfoCommand(m irc.Message) {
	// SVINFO <TS version> <min TS version> 0 <current time>
	if len(m.Params) < 4 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"SVINFO", "Not enough parameters"})
		return
	}

	if !c.GotSERVER || !c.SentSERVER {
		c.quit("SERVER first")
		return
	}

	// Once we have SVINFO, we'll upgrade to ServerClient, so we will never see
	// double SVINFO.

	if m.Params[0] != "6" || m.Params[1] != "6" {
		c.quit("Unsupported TS version")
		return
	}

	if m.Params[2] != "0" {
		c.quit("Malformed third parameter")
		return
	}

	theirEpoch, err := strconv.ParseInt(m.Params[3], 10, 64)
	if err != nil {
		c.quit("Malformed time")
		return
	}

	epoch := time.Now().Unix()

	delta := epoch - theirEpoch
	if delta < 0 {
		delta *= -1
	}

	if delta > 60 {
		c.quit("Time insanity")
		return
	}

	// If we initiated the connection, then we already sent SVINFO (in reply
	// to them sending SERVER). This is their reply to our SVINFO.
	if !c.SentSVINFO {
		c.sendSVINFO()
	}

	// Let's choose here to decide we're linked. The burst is still to come.
	c.registerServer()
}

func (c *LocalClient) errorCommand(m irc.Message) {
	c.quit("Bye")
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
		for _, channel := range u.Channels {
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
	if u.onChannel(&Channel{Name: channelName}) {
		// 443 ERR_USERONCHANNEL
		// This error code is supposed to be for inviting a user on a channel
		// already, but it works.
		u.messageFromServer("443", []string{u.DisplayNick, channelName,
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
	channel.Members[u.UID] = struct{}{}
	u.Channels[channelName] = channel

	// Tell the client about the join. This is what RFC says to send:
	// Send JOIN, RPL_TOPIC, and RPL_NAMREPLY.

	// JOIN comes from the client, to the client.
	u.messageClient(u, "JOIN", []string{channel.Name})

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
		if memberUID == u.UID {
			continue
		}

		member := u.Catbox.Users[memberUID]

		// From the client to each member.
		u.messageUser(member, "JOIN", []string{channel.Name})
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
	msgLen := len(":") + len(u.nickUhost()) + len(" ") + len(m.Command) +
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
		if !u.onChannel(channel) {
			// 404 ERR_CANNOTSENDTOCHAN
			u.messageFromServer("404", []string{channelName, "Cannot send to channel"})
			return
		}

		u.LastMessageTime = time.Now()

		// Send to all members of the channel. Except the client itself it seems.
		for memberUID := range channel.Members {
			if memberUID == u.UID {
				continue
			}

			member := u.Catbox.Users[memberUID]

			// From the client to each member.
			u.messageUser(member, m.Command, []string{channel.Name, msg})
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

	u.messageUser(targetUser, m.Command, []string{nickName, msg})
}

func (u *LocalUser) lusersCommand() {
	// We always send RPL_LUSERCLIENT and RPL_LUSERME.
	// The others only need be sent if the counts are non-zero.

	// 251 RPL_LUSERCLIENT
	u.messageFromServer("251", []string{
		fmt.Sprintf("There are %d users and %d services on %d servers.",
			len(u.Catbox.LocalUsers),
			0,
			// +1 to count ourself.
			len(u.Catbox.LocalServers)+1),
	})

	// 252 RPL_LUSEROP
	operCount := 0
	for _, client := range u.Catbox.LocalUsers {
		if client.isOperator() {
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
	if !u.isOperator() {
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

	if u.isOperator() {
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
	u.Modes['o'] = struct{}{}

	u.Catbox.Opers[u.UID] = u

	u.messageClient(u, "MODE", []string{u.DisplayNick, "+o"})

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
		for k := range u.Modes {
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
		if !u.isOperator() {
			continue
		}

		delete(u.Modes, 'o')
		delete(u.Catbox.Opers, u.UID)
		u.messageClient(u, "MODE", []string{"-o", u.DisplayNick})
	}
}

func (u *LocalUser) channelModeCommand(channel *Channel, modes string) {
	if !u.onChannel(channel) {
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
	if !u.onChannel(channel) {
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

	if !u.onChannel(channel) {
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
		u.messageUser(member, "TOPIC", []string{channel.Name, channel.Topic})
	}
}

// Initiate a connection to a server.
//
// I implement CONNECT differently than RFC 2812. Only a single parameter.
func (u *LocalUser) connectCommand(m irc.Message) {
	if !u.isOperator() {
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
