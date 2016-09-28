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
func (c *Client) nickCommand(m irc.Message) {
	// We should have one parameter: The nick they want.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		c.messageFromServer("431", []string{"No nickname given"})
		return
	}
	nick := m.Params[0]

	if len(nick) > c.Server.Config.MaxNickLength {
		nick = nick[0:c.Server.Config.MaxNickLength]
	}

	if !isValidNick(c.Server.Config.MaxNickLength, nick) {
		// 432 ERR_ERRONEUSNICKNAME
		c.messageFromServer("432", []string{nick, "Erroneous nickname"})
		return
	}

	nickCanon := canonicalizeNick(nick)

	// Nick must be unique.
	_, exists := c.Server.Nicks[nickCanon]
	if exists {
		// 433 ERR_NICKNAMEINUSE
		c.messageFromServer("433", []string{nick, "Nickname is already in use"})
		return
	}

	// Flag the nick as taken by this client.
	c.Server.Nicks[nickCanon] = c.ID
	oldDisplayNick := c.PreRegDisplayNick

	// Free the old nick (if there is one).
	if len(oldDisplayNick) > 0 {
		delete(c.Server.Nicks, canonicalizeNick(oldDisplayNick))
	}

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
func (c *UserClient) nickCommand(m irc.Message) {
	// We should have one parameter: The nick they want.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		c.messageFromServer("431", []string{"No nickname given"})
		return
	}
	nick := m.Params[0]

	if len(nick) > c.Server.Config.MaxNickLength {
		nick = nick[0:c.Server.Config.MaxNickLength]
	}

	if !isValidNick(c.Server.Config.MaxNickLength, nick) {
		// 432 ERR_ERRONEUSNICKNAME
		c.messageFromServer("432", []string{nick, "Erroneous nickname"})
		return
	}

	nickCanon := canonicalizeNick(nick)

	// Nick must be unique.
	_, exists := c.Server.Nicks[nickCanon]
	if exists {
		// 433 ERR_NICKNAMEINUSE
		c.messageFromServer("433", []string{nick, "Nickname is already in use"})
		return
	}

	// Flag the nick as taken by this client.
	c.Server.Nicks[nickCanon] = c.ID
	oldDisplayNick := c.DisplayNick

	// Free the old nick.
	delete(c.Server.Nicks, canonicalizeNick(oldDisplayNick))

	// We need to inform other clients about the nick change.
	// Any that are in the same channel as this client.
	informedClients := map[uint64]struct{}{}
	for _, channel := range c.Channels {
		for _, member := range channel.Members {
			// Tell each client only once.
			_, exists := informedClients[member.ID]
			if exists {
				continue
			}

			// Message needs to come from the OLD nick.
			c.messageClient(member, "NICK", []string{nick})
			informedClients[member.ID] = struct{}{}
		}
	}

	// Reply to the client. We should have above, but if they were not on any
	// channels then we did not.
	_, exists = informedClients[c.ID]
	if !exists {
		c.messageClient(c, "NICK", []string{nick})
	}

	// Finally, make the update. Do this last as we need to ensure we act
	// as the old nick when crafting messages.
	c.DisplayNick = nick
}

func (c *Client) userCommand(m irc.Message) {
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

	if len(user) > c.Server.Config.MaxNickLength {
		user = user[0:c.Server.Config.MaxNickLength]
	}

	if !isValidUser(c.Server.Config.MaxNickLength, user) {
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
func (c *UserClient) userCommand(m irc.Message) {
	// 462 ERR_ALREADYREGISTRED
	c.messageFromServer("462", []string{"Unauthorized command (already registered)"})
}

func (c *Client) passCommand(m irc.Message) {
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

func (c *Client) capabCommand(m irc.Message) {
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

func (c *Client) serverCommand(m irc.Message) {
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
	linkInfo, exists := c.Server.Config.Servers[m.Params[0]]
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
	_, exists = c.Server.Servers[m.Params[0]]
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

func (c *Client) svinfoCommand(m irc.Message) {
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

	if c.GotSVINFO {
		c.quit("Double SVINFO")
		return
	}

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

	// We reply with our SVINFO, burst, and PING indicating end of burst.

	c.GotSVINFO = true

	// If we initiated the connection, then we already sent SVINFO (in reply
	// to them sending SERVER). This is their reply to our SVINFO.
	if !c.SentSVINFO {
		c.sendSVINFO()
	}

	// TODO: Burst

	c.sendPING()
}

func (c *Client) pingCommand(m irc.Message) {
	// We expect a PING from server as part of burst end.
	// PING <Remote SID>
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"SVINFO", "Not enough parameters"})
		return
	}

	if !c.GotSVINFO {
		c.quit("Unexpected PING")
		return
	}

	// Allow multiple pings.

	if m.Params[0] != c.PreRegTS6SID {
		c.quit("Unexpected SID")
		return
	}

	c.GotPING = true

	// Reply.

	c.maybeQueueMessage(irc.Message{
		Prefix:  c.Server.Config.TS6SID,
		Command: "PONG",
		Params: []string{
			c.Server.Config.ServerName,
			c.PreRegTS6SID,
		},
	})

	c.SentPONG = true

	if c.GotPONG {
		c.registerServer()
	}
}

func (c *Client) pongCommand(m irc.Message) {
	// We expect this at end of server link burst.
	// :<Remote SID> PONG <Remote server name> <My SID>
	if len(m.Params) < 2 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"SVINFO", "Not enough parameters"})
		return
	}

	if !c.GotSVINFO || !c.SentPING {
		c.quit("Unexpected PING")
		return
	}

	if c.GotPONG {
		c.quit("Double PONG")
		return
	}

	if m.Prefix != c.PreRegTS6SID {
		c.quit("Unknown prefix")
		return
	}

	if m.Params[0] != c.PreRegServerName {
		c.quit("Unknown server name")
		return
	}

	if m.Params[1] != c.Server.Config.TS6SID {
		c.quit("Unknown SID")
		return
	}

	// No reply.

	c.GotPONG = true

	if c.SentPONG {
		c.registerServer()
	}
}

func (c *Client) errorCommand(m irc.Message) {
	c.quit("Bye")
}

func (c *UserClient) joinCommand(m irc.Message) {
	// Parameters: ( <channel> *( "," <channel> ) [ <key> *( "," <key> ) ] ) / "0"

	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"JOIN", "Not enough parameters"})
		return
	}

	// JOIN 0 is a special case. Client leaves all channels.
	if len(m.Params) == 1 && m.Params[0] == "0" {
		for _, channel := range c.Channels {
			c.part(channel.Name, "")
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
		c.messageFromServer("403", []string{channelName, "Invalid channel name"})
		return
	}

	// TODO: Support keys.

	// Try to join the client to the channel.

	// Is the client in the channel already?
	if c.onChannel(&Channel{Name: channelName}) {
		// 443 ERR_USERONCHANNEL
		// This error code is supposed to be for inviting a user on a channel
		// already, but it works.
		c.messageFromServer("443", []string{c.DisplayNick, channelName,
			"is already on channel"})
		return
	}

	// Look up / create the channel
	channel, exists := c.Server.Channels[channelName]
	if !exists {
		channel = &Channel{
			Name:    channelName,
			Members: make(map[uint64]*UserClient),
		}
		c.Server.Channels[channelName] = channel
	}

	// Add the client to the channel.
	channel.Members[c.ID] = c
	c.Channels[channelName] = channel

	// Tell the client about the join. This is what RFC says to send:
	// Send JOIN, RPL_TOPIC, and RPL_NAMREPLY.

	// JOIN comes from the client, to the client.
	c.messageClient(c, "JOIN", []string{channel.Name})

	// It appears RPL_TOPIC is optional, at least ircd-ratbox does not send it.
	// Presumably if there is no topic.
	if len(channel.Topic) > 0 {
		// 332 RPL_TOPIC
		c.messageFromServer("332", []string{channel.Name, channel.Topic})
	}

	// RPL_NAMREPLY: This tells the client about who is in the channel
	// (including itself).
	// It ends with RPL_ENDOFNAMES.
	for _, member := range channel.Members {
		// 353 RPL_NAMREPLY
		c.messageFromServer("353", []string{
			// = means public channel. TODO: When we have chan modes +s / +p this
			// needs to vary
			// TODO: We need to include @ / + for each nick opped/voiced.
			// Note we can have multiple nicks per RPL_NAMREPLY. TODO: Do that.
			"=", channel.Name, fmt.Sprintf(":%s", member.DisplayNick),
		})
	}

	// 366 RPL_ENDOFNAMES
	c.messageFromServer("366", []string{channel.Name, "End of NAMES list"})

	// Tell each member in the channel about the client.
	for _, member := range channel.Members {
		// Don't tell the client. We already did (above).
		if member.ID == c.ID {
			continue
		}

		// From the client to each member.
		c.messageClient(member, "JOIN", []string{channel.Name})
	}
}

func (c *UserClient) partCommand(m irc.Message) {
	// Parameters: <channel> *( "," <channel> ) [ <Part Message> ]

	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"PART", "Not enough parameters"})
		return
	}

	// Again, we don't raise error if there are too many parameters.

	partMessage := ""
	if len(m.Params) >= 2 {
		partMessage = m.Params[1]
	}

	c.part(m.Params[0], partMessage)
}

// Per RFC 2812, PRIVMSG and NOTICE are essentially the same, so both PRIVMSG
// and NOTICE use this command function.
func (c *UserClient) privmsgCommand(m irc.Message) {
	// Parameters: <msgtarget> <text to be sent>

	if len(m.Params) == 0 {
		// 411 ERR_NORECIPIENT
		c.messageFromServer("411", []string{"No recipient given (PRIVMSG)"})
		return
	}

	if len(m.Params) == 1 {
		// 412 ERR_NOTEXTTOSEND
		c.messageFromServer("412", []string{"No text to send"})
		return
	}

	// I don't check if there are too many parameters. They get ignored anyway.

	target := m.Params[0]

	msg := m.Params[1]

	// The message may be too long once we add the prefix/encode the message.
	// Strip any trailing characters until it's short enough.
	// TODO: Other messages can have this problem too (PART, QUIT, etc...)
	msgLen := len(":") + len(c.nickUhost()) + len(" ") + len(m.Command) +
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
			c.messageFromServer("404", []string{channelName, "Cannot send to channel"})
			return
		}

		channel, exists := c.Server.Channels[channelName]
		if !exists {
			// 403 ERR_NOSUCHCHANNEL
			c.messageFromServer("403", []string{channelName, "No such channel"})
			return
		}

		// Are they on it?
		// TODO: Technically we should allow messaging if they aren't on it
		//   depending on the mode.
		if !c.onChannel(channel) {
			// 404 ERR_CANNOTSENDTOCHAN
			c.messageFromServer("404", []string{channelName, "Cannot send to channel"})
			return
		}

		c.LastMessageTime = time.Now()

		// Send to all members of the channel. Except the client itself it seems.
		for _, member := range channel.Members {
			if member.ID == c.ID {
				continue
			}

			// From the client to each member.
			c.messageClient(member, m.Command, []string{channel.Name, msg})
		}

		return
	}

	// We're messaging a nick directly.

	nickName := canonicalizeNick(target)
	if !isValidNick(c.Server.Config.MaxNickLength, nickName) {
		// 401 ERR_NOSUCHNICK
		c.messageFromServer("401", []string{nickName, "No such nick/channel"})
		return
	}

	targetClientID, exists := c.Server.Nicks[nickName]
	if !exists {
		// 401 ERR_NOSUCHNICK
		c.messageFromServer("401", []string{nickName, "No such nick/channel"})
		return
	}
	targetClient := c.Server.UserClients[targetClientID]

	c.LastMessageTime = time.Now()

	c.messageClient(targetClient, m.Command, []string{nickName, msg})
}

func (c *UserClient) lusersCommand() {
	// We always send RPL_LUSERCLIENT and RPL_LUSERME.
	// The others only need be sent if the counts are non-zero.

	// 251 RPL_LUSERCLIENT
	c.messageFromServer("251", []string{
		fmt.Sprintf("There are %d users and %d services on %d servers.",
			len(c.Server.UserClients),
			0,
			// +1 to count ourself.
			len(c.Server.ServerClients)+1),
	})

	// 252 RPL_LUSEROP
	operCount := 0
	for _, client := range c.Server.UserClients {
		if client.isOperator() {
			operCount++
		}
	}
	if operCount > 0 {
		// 252 RPL_LUSEROP
		c.messageFromServer("252", []string{
			fmt.Sprintf("%d", operCount),
			"operator(s) online",
		})
	}

	// 253 RPL_LUSERUNKNOWN
	// Unregistered connections.
	numUnknown := len(c.Server.UnregisteredClients)
	if numUnknown > 0 {
		c.messageFromServer("253", []string{
			fmt.Sprintf("%d", numUnknown),
			"unknown connection(s)",
		})
	}

	// 254 RPL_LUSERCHANNELS
	if len(c.Server.Channels) > 0 {
		c.messageFromServer("254", []string{
			fmt.Sprintf("%d", len(c.Server.Channels)),
			"channels formed",
		})
	}

	// 255 RPL_LUSERME
	c.messageFromServer("255", []string{
		fmt.Sprintf("I have %d clients and %d servers",
			len(c.Server.UserClients), len(c.Server.ServerClients)),
	})
}

func (c *UserClient) motdCommand() {
	// 375 RPL_MOTDSTART
	c.messageFromServer("375", []string{
		fmt.Sprintf("- %s Message of the day - ", c.Server.Config.ServerName),
	})

	// 372 RPL_MOTD
	c.messageFromServer("372", []string{
		fmt.Sprintf("- %s", c.Server.Config.MOTD),
	})

	// 376 RPL_ENDOFMOTD
	c.messageFromServer("376", []string{"End of MOTD command"})
}

func (c *UserClient) quitCommand(m irc.Message) {
	msg := "Quit:"
	if len(m.Params) > 0 {
		msg += " " + m.Params[0]
	}

	c.quit(msg)
}

func (c *UserClient) pingCommand(m irc.Message) {
	// Parameters: <server> (I choose to not support forwarding)
	if len(m.Params) == 0 {
		// 409 ERR_NOORIGIN
		c.messageFromServer("409", []string{"No origin specified"})
		return
	}

	server := m.Params[0]

	if server != c.Server.Config.ServerName {
		// 402 ERR_NOSUCHSERVER
		c.messageFromServer("402", []string{server, "No such server"})
		return
	}

	c.messageFromServer("PONG", []string{server})
}

func (c *UserClient) dieCommand(m irc.Message) {
	if !c.isOperator() {
		// 481 ERR_NOPRIVILEGES
		c.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	// die is not an RFC command. I use it to shut down the server.
	c.Server.shutdown()
}

func (c *UserClient) whoisCommand(m irc.Message) {
	// Difference from RFC: I support only a single nickname (no mask), and no
	// server target.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		c.messageFromServer("431", []string{"No nickname given"})
		return
	}

	nick := m.Params[0]
	nickCanonical := canonicalizeNick(nick)

	targetClientID, exists := c.Server.Nicks[nickCanonical]
	if !exists {
		// 401 ERR_NOSUCHNICK
		c.messageFromServer("401", []string{nick, "No such nick/channel"})
		return
	}
	targetClient := c.Server.UserClients[targetClientID]

	// 311 RPL_WHOISUSER
	c.messageFromServer("311", []string{
		targetClient.DisplayNick,
		targetClient.User,
		fmt.Sprintf("%s", targetClient.Conn.IP),
		"*",
		targetClient.RealName,
	})

	// 319 RPL_WHOISCHANNELS
	// I choose to not show any.

	// 312 RPL_WHOISSERVER
	c.messageFromServer("312", []string{
		targetClient.DisplayNick,
		c.Server.Config.ServerName,
		c.Server.Config.ServerInfo,
	})

	// 301 RPL_AWAY
	// TODO: AWAY not implemented yet.

	// 313 RPL_WHOISOPERATOR
	if targetClient.isOperator() {
		c.messageFromServer("313", []string{
			targetClient.DisplayNick,
			"is an IRC operator",
		})
	}

	// TODO: TLS information

	// 317 RPL_WHOISIDLE
	idleDuration := time.Now().Sub(targetClient.LastMessageTime)
	idleSeconds := int(idleDuration.Seconds())
	c.messageFromServer("317", []string{
		targetClient.DisplayNick,
		fmt.Sprintf("%d", idleSeconds),
		"seconds idle",
	})

	// 318 RPL_ENDOFWHOIS
	c.messageFromServer("318", []string{
		targetClient.DisplayNick,
		"End of WHOIS list",
	})
}

func (c *UserClient) operCommand(m irc.Message) {
	// Parameters: <name> <password>
	if len(m.Params) < 2 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"OPER", "Not enough parameters"})
		return
	}

	if c.isOperator() {
		// 381 RPL_YOUREOPER
		c.messageFromServer("381", []string{"You are already an IRC operator"})
		return
	}

	// TODO: Host matching

	// Check if they gave acceptable permissions.
	pass, exists := c.Server.Config.Opers[m.Params[0]]
	if !exists || pass != m.Params[1] {
		// 464 ERR_PASSWDMISMATCH
		c.messageFromServer("464", []string{"Password incorrect"})
		return
	}

	// Give them oper status.
	c.Modes['o'] = struct{}{}

	c.Server.Opers[c.ID] = c

	c.messageClient(c, "MODE", []string{c.DisplayNick, "+o"})

	// 381 RPL_YOUREOPER
	c.messageFromServer("381", []string{"You are now an IRC operator"})
}

// MODE command applies either to nicknames or to channels.
func (c *UserClient) modeCommand(m irc.Message) {
	// User mode:
	// Parameters: <nickname> *( ( "+" / "-" ) *( "i" / "w" / "o" / "O" / "r" ) )

	// Channel mode:
	// Parameters: <channel> *( ( "-" / "+" ) *<modes> *<modeparams> )

	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{"MODE", "Not enough parameters"})
		return
	}

	target := m.Params[0]

	// We can have blank mode. This will cause server to send current settings.
	modes := ""
	if len(m.Params) > 1 {
		modes = m.Params[1]
	}

	// Is it a nickname?
	targetClientID, exists := c.Server.Nicks[canonicalizeNick(target)]
	if exists {
		targetClient := c.Server.UserClients[targetClientID]
		c.userModeCommand(targetClient, modes)
		return
	}

	// Is it a channel?
	targetChannel, exists := c.Server.Channels[canonicalizeChannel(target)]
	if exists {
		c.channelModeCommand(targetChannel, modes)
		return
	}

	// Well... Not found. Send a channel not found. It seems the closest matching
	// extant error in RFC.
	// 403 ERR_NOSUCHCHANNEL
	c.messageFromServer("403", []string{target, "No such channel"})
}

func (c *UserClient) userModeCommand(targetClient *UserClient,
	modes string) {
	// They can only change their own mode.
	if targetClient != c {
		// 502 ERR_USERSDONTMATCH
		c.messageFromServer("502", []string{"Cannot change mode for other users"})
		return
	}

	// No modes given means we should send back their current mode.
	if len(modes) == 0 {
		modeReturn := "+"
		for k := range c.Modes {
			modeReturn += string(k)
		}

		// 221 RPL_UMODEIS
		c.messageFromServer("221", []string{modeReturn})
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
			c.messageFromServer("472", []string{modes, "is unknown mode to me"})
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
			c.messageFromServer("501", []string{"Unknown MODE flag"})
			continue
		}

		// Ignore it if they try to +o (operator) themselves. RFC says to do so.
		if action == '+' {
			continue
		}

		// This is -o. They have to be operator for there to be any effect.
		if !c.isOperator() {
			continue
		}

		delete(c.Modes, 'o')
		c.messageClient(c, "MODE", []string{"-o", c.DisplayNick})
	}
}

func (c *UserClient) channelModeCommand(channel *Channel, modes string) {
	if !c.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		c.messageFromServer("442", []string{channel.Name, "You're not on that channel"})
		return
	}

	// No modes? Send back the channel's modes.
	// Always send back +n. That's only I support right now.
	if len(modes) == 0 {
		// 324 RPL_CHANNELMODEIS
		c.messageFromServer("324", []string{channel.Name, "+n"})
		return
	}

	// Listing bans. I don't support bans at this time, but say that there are
	// none.
	if modes == "b" || modes == "+b" {
		// 368 RPL_ENDOFBANLIST
		c.messageFromServer("368", []string{channel.Name, "End of channel ban list"})
		return
	}

	// Since we don't have channel operators implemented, any attempt to alter
	// mode is an error.
	// 482 ERR_CHANOPRIVSNEEDED
	c.messageFromServer("482", []string{channel.Name, "You're not channel operator"})
}

func (c *UserClient) whoCommand(m irc.Message) {
	// Contrary to RFC 2812, I support only 'WHO #channel'.
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	channel, exists := c.Server.Channels[canonicalizeChannel(m.Params[0])]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.messageFromServer("403", []string{m.Params[0], "Invalid channel name"})
		return
	}

	// Only works if they are on the channel.
	if !c.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		c.messageFromServer("442", []string{channel.Name, "You're not on that channel"})
		return
	}

	for _, member := range channel.Members {
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
		c.messageFromServer("352", []string{
			channel.Name,
			member.User,
			fmt.Sprintf("%s", member.Conn.IP),
			c.Server.Config.ServerName,
			member.DisplayNick,
			mode,
			"0 " + member.RealName,
		})
	}

	// 315 RPL_ENDOFWHO
	c.messageFromServer("315", []string{channel.Name, "End of WHO list"})
}

func (c *UserClient) topicCommand(m irc.Message) {
	// Params: <channel> [ <topic> ]
	if len(m.Params) == 0 {
		c.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	channelName := canonicalizeChannel(m.Params[0])
	channel, exists := c.Server.Channels[channelName]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.messageFromServer("403", []string{m.Params[0], "Invalid channel name"})
		return
	}

	if !c.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		c.messageFromServer("442", []string{channel.Name, "You're not on that channel"})
		return
	}

	// If there is no new topic, then just send back the current one.
	if len(m.Params) < 2 {
		if len(channel.Topic) == 0 {
			// 331 RPL_NOTOPIC
			c.messageFromServer("331", []string{channel.Name, "No topic is set"})
			return
		}

		// 332 RPL_TOPIC
		c.messageFromServer("332", []string{channel.Name, channel.Topic})
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
	for _, member := range channel.Members {
		// 332 RPL_TOPIC
		c.messageClient(member, "TOPIC", []string{channel.Name, channel.Topic})
	}
}

// Initiate a connection to a server.
//
// I implement CONNECT differently than RFC 2812. Only a single parameter.
func (c *UserClient) connectCommand(m irc.Message) {
	if !c.isOperator() {
		// 481 ERR_NOPRIVILEGES
		c.messageFromServer("481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	// CONNECT <server name>
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		c.messageFromServer("461", []string{m.Command, "Not enough parameters"})
		return
	}

	serverName := m.Params[0]

	// Is it a server we know about?
	linkInfo, exists := c.Server.Config.Servers[serverName]
	if !exists {
		// 402 ERR_NOSUCHSERVER
		c.messageFromServer("402", []string{serverName, "No such server"})
		return
	}

	// Are we already linked to it?
	_, exists = c.Server.Servers[serverName]
	if exists {
		// No great error code.
		c.notice(fmt.Sprintf("I am already linked to %s.", serverName))
		return
	}

	// We could check if we're trying to link to it. But the result should be the
	// same.

	// Initiate a connection.
	// Put it in a goroutine to avoid blocking server goroutine.
	c.Server.WG.Add(1)
	go func() {
		defer c.Server.WG.Done()

		c.notice(fmt.Sprintf("Connecting to %s...", linkInfo.Name))

		conn, err := net.DialTimeout("tcp",
			fmt.Sprintf("%s:%d", linkInfo.Hostname, linkInfo.Port),
			c.Server.Config.DeadTime)
		if err != nil {
			log.Printf("Unable to connect to server [%s]: %s", linkInfo.Name, err)
			return
		}

		id := c.Server.getClientID()

		client := NewClient(c.Server, id, conn)
		client.Server.newEvent(Event{Type: NewClientEvent, Client: client})

		client.sendPASS(linkInfo.Pass)
		client.sendCAPAB()
		client.sendSERVER()

		client.Server.WG.Add(1)
		go client.readLoop()
		c.Server.WG.Add(1)
		go client.writeLoop()
	}()
}
