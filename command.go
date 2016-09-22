package main

import (
	"fmt"
	"time"

	"summercat.com/irc"
)

// handleMessage takes action based on a client's IRC message.
func (s *Server) handleMessage(c *Client, m irc.Message) {
	// Record that client said something to us just now.
	c.LastActivityTime = time.Now()

	// Clients SHOULD NOT (section 2.3) send a prefix. I'm going to disallow it
	// completely for all commands.
	if m.Prefix != "" {
		s.messageClient(c, "ERROR", []string{"Do not send a prefix"})
		return
	}

	// Non-RFC command that appears to be widely supported. Just ignore it for
	// now.
	if m.Command == "CAP" {
		return
	}

	if m.Command == "NICK" {
		s.nickCommand(c, m)
		return
	}

	if m.Command == "USER" {
		s.userCommand(c, m)
		return
	}

	// Let's say *all* other commands require you to be registered.
	// This is likely stricter than RFC.
	if !c.Registered {
		// 451 ERR_NOTREGISTERED
		s.messageClient(c, "451", []string{fmt.Sprintf("You have not registered.")})
		return
	}

	if m.Command == "JOIN" {
		s.joinCommand(c, m)
		return
	}

	if m.Command == "PART" {
		s.partCommand(c, m)
		return
	}

	// Per RFC these commands are near identical.
	if m.Command == "PRIVMSG" || m.Command == "NOTICE" {
		s.privmsgCommand(c, m)
		return
	}

	if m.Command == "LUSERS" {
		s.lusersCommand(c)
		return
	}

	if m.Command == "MOTD" {
		s.motdCommand(c)
		return
	}

	if m.Command == "QUIT" {
		s.quitCommand(c, m)
		return
	}

	if m.Command == "PONG" {
		// Not doing anything with this. Just accept it.
		return
	}

	if m.Command == "PING" {
		s.pingCommand(c, m)
		return
	}

	if m.Command == "DIE" {
		s.dieCommand(c, m)
		return
	}

	if m.Command == "WHOIS" {
		s.whoisCommand(c, m)
		return
	}

	if m.Command == "OPER" {
		s.operCommand(c, m)
		return
	}

	if m.Command == "MODE" {
		s.modeCommand(c, m)
		return
	}

	if m.Command == "WHO" {
		s.whoCommand(c, m)
		return
	}

	if m.Command == "TOPIC" {
		s.topicCommand(c, m)
		return
	}

	// Unknown command. We don't handle it yet anyway.

	// 421 ERR_UNKNOWNCOMMAND
	s.messageClient(c, "421", []string{m.Command, "Unknown command"})
}

func (s *Server) nickCommand(c *Client, m irc.Message) {
	// We should have one parameter: The nick they want.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		s.messageClient(c, "431", []string{"No nickname given"})
		return
	}

	// We could check if there is more than 1 parameter. But it doesn't seem
	// particularly problematic if there are. We ignore them. There's not a good
	// error to raise in RFC even if we did check.

	nick := m.Params[0]

	if !isValidNick(nick) {
		// 432 ERR_ERRONEUSNICKNAME
		s.messageClient(c, "432", []string{nick, "Erroneous nickname"})
		return
	}

	// Nick must be caselessly unique.
	nickCanon := canonicalizeNick(nick)

	_, exists := s.Nicks[nickCanon]
	if exists {
		// 433 ERR_NICKNAMEINUSE
		s.messageClient(c, "433", []string{nick, "Nickname is already in use"})
		return
	}

	// Flag the nick as taken by this client.
	s.Nicks[nickCanon] = c
	oldDisplayNick := c.DisplayNick

	// The NICK command to happen both at connection registration time and
	// after. There are different rules.

	// Free the old nick (if there is one).
	// I do this in both registered and not states in case there are clients
	// misbehaving. I suppose we could not let them issue any more NICKs
	// beyond the first too if they are not registered.
	if len(oldDisplayNick) > 0 {
		delete(s.Nicks, canonicalizeNick(oldDisplayNick))
	}

	if c.Registered {
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
		_, exists := informedClients[c.ID]
		if !exists {
			c.messageClient(c, "NICK", []string{nick})
		}
	}

	// We don't reply during registration (we don't have enough info, no uhost
	// anyway).

	// Finally, make the update. Do this last as we need to ensure we act
	// as the old nick when crafting messages.
	c.DisplayNick = nick

	// If we have USER done already, then we're done registration.
	if len(c.User) > 0 {
		s.completeRegistration(c)
	}
}

func (s *Server) userCommand(c *Client, m irc.Message) {
	// The USER command only occurs during connection registration.
	if c.Registered {
		// 462 ERR_ALREADYREGISTRED
		s.messageClient(c, "462",
			[]string{"Unauthorized command (already registered)"})
		return
	}

	// RFC RECOMMENDs NICK before USER. But I'm going to allow either way now.
	// One reason to do so is how to react if NICK was taken and client
	// proceeded to USER.

	// 4 parameters: <user> <mode> <unused> <realname>
	if len(m.Params) != 4 {
		// 461 ERR_NEEDMOREPARAMS
		s.messageClient(c, "461", []string{m.Command, "Not enough parameters"})
		return
	}

	user := m.Params[0]

	if !isValidUser(user) {
		// There isn't an appropriate response in the RFC. ircd-ratbox sends an
		// ERROR message. Do that.
		s.messageClient(c, "ERROR", []string{"Invalid username"})
		return
	}
	c.User = user

	// We could do something with user mode here.

	// Validate realname.
	// Arbitrary. Length only.
	if len(m.Params[3]) > 64 {
		s.messageClient(c, "ERROR", []string{"Invalid realname"})
		return
	}
	c.RealName = m.Params[3]

	// If we have a nick, then we're done registration.
	if len(c.DisplayNick) > 0 {
		s.completeRegistration(c)
	}
}

func (s *Server) completeRegistration(c *Client) {
	c.Registered = true

	// RFC 2813 specifies messages to send upon registration.

	// 001 RPL_WELCOME
	s.messageClient(c, "001", []string{
		fmt.Sprintf("Welcome to the Internet Relay Network %s", c.nickUhost()),
	})

	// 002 RPL_YOURHOST
	s.messageClient(c, "002", []string{
		fmt.Sprintf("Your host is %s, running version %s", s.Config.ServerName,
			s.Config.Version),
	})

	// 003 RPL_CREATED
	s.messageClient(c, "003", []string{
		fmt.Sprintf("This server was created %s", s.Config.CreatedDate),
	})

	// 004 RPL_MYINFO
	// <servername> <version> <available user modes> <available channel modes>
	s.messageClient(c, "004", []string{
		// It seems ambiguous if these are to be separate parameters.
		s.Config.ServerName,
		s.Config.Version,
		"o",
		"n",
	})

	s.lusersCommand(c)

	s.motdCommand(c)
}

func (s *Server) joinCommand(c *Client, m irc.Message) {
	// Parameters: ( <channel> *( "," <channel> ) [ <key> *( "," <key> ) ] ) / "0"

	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		s.messageClient(c, "461", []string{"JOIN", "Not enough parameters"})
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
		s.messageClient(c, "403", []string{channelName, "Invalid channel name"})
		return
	}

	// TODO: Support keys.

	// Try to join the client to the channel.

	// Is the client in the channel already?
	if c.onChannel(&Channel{Name: channelName}) {
		// We could just ignore it too.
		s.messageClient(c, "ERROR", []string{"You are on that channel"})
		return
	}

	// Look up / create the channel
	channel, exists := s.Channels[channelName]
	if !exists {
		channel = &Channel{
			Name:    channelName,
			Members: make(map[uint64]*Client),
		}
		s.Channels[channelName] = channel
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
		s.messageClient(c, "332", []string{channel.Name, channel.Topic})
	}

	// RPL_NAMREPLY: This tells the client about who is in the channel
	// (including itself).
	// It ends with RPL_ENDOFNAMES.
	for _, member := range channel.Members {
		// 353 RPL_NAMREPLY
		s.messageClient(c, "353", []string{
			// = means public channel. TODO: When we have chan modes +s / +p this
			// needs to vary
			// TODO: We need to include @ / + for each nick opped/voiced.
			// Note we can have multiple nicks per RPL_NAMREPLY. TODO: Do that.
			"=", channel.Name, fmt.Sprintf(":%s", member.DisplayNick),
		})
	}

	// 366 RPL_ENDOFNAMES
	s.messageClient(c, "366", []string{channel.Name, "End of NAMES list"})

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

func (s *Server) partCommand(c *Client, m irc.Message) {
	// Parameters: <channel> *( "," <channel> ) [ <Part Message> ]

	if len(m.Params) == 0 {
		// 461 ERR_NEEDMOREPARAMS
		s.messageClient(c, "461", []string{"PART", "Not enough parameters"})
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
func (s *Server) privmsgCommand(c *Client, m irc.Message) {
	// Parameters: <msgtarget> <text to be sent>

	if len(m.Params) == 0 {
		// 411 ERR_NORECIPIENT
		s.messageClient(c, "411", []string{"No recipient given (PRIVMSG)"})
		return
	}

	if len(m.Params) == 1 {
		// 412 ERR_NOTEXTTOSEND
		s.messageClient(c, "412", []string{"No text to send"})
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
			s.messageClient(c, "404", []string{channelName, "Cannot send to channel"})
			return
		}

		channel, exists := s.Channels[channelName]
		if !exists {
			// 403 ERR_NOSUCHCHANNEL
			s.messageClient(c, "403", []string{channelName, "No such channel"})
			return
		}

		// Are they on it?
		// TODO: Technically we should allow messaging if they aren't on it
		//   depending on the mode.
		if !c.onChannel(channel) {
			// 404 ERR_CANNOTSENDTOCHAN
			s.messageClient(c, "404", []string{channelName, "Cannot send to channel"})
			return
		}

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
	if !isValidNick(nickName) {
		// 401 ERR_NOSUCHNICK
		s.messageClient(c, "401", []string{nickName, "No such nick/channel"})
		return
	}

	targetClient, exists := s.Nicks[nickName]
	if !exists {
		// 401 ERR_NOSUCHNICK
		s.messageClient(c, "401", []string{nickName, "No such nick/channel"})
		return
	}

	c.messageClient(targetClient, m.Command, []string{nickName, msg})
}

func (s *Server) lusersCommand(c *Client) {
	// We always send RPL_LUSERCLIENT and RPL_LUSERME.
	// The others only need be sent if the counts are non-zero.

	// 251 RPL_LUSERCLIENT
	s.messageClient(c, "251", []string{
		fmt.Sprintf("There are %d users and %d services on %d servers.",
			len(s.Nicks), 0, 0),
	})

	// 252 RPL_LUSEROP
	operCount := 0
	for _, client := range s.Nicks {
		if client.isOperator() {
			operCount++
		}
	}
	if operCount > 0 {
		// 252 RPL_LUSEROP
		s.messageClient(c, "252", []string{
			fmt.Sprintf("%d", operCount),
			"operator(s) online",
		})
	}

	// 253 RPL_LUSERUNKNOWN
	// Unregistered connections.
	numUnknown := len(s.Clients) - len(s.Nicks)
	if numUnknown > 0 {
		s.messageClient(c, "253", []string{
			fmt.Sprintf("%d", numUnknown),
			"unknown connection(s)",
		})
	}

	// 254 RPL_LUSERCHANNELS
	if len(s.Channels) > 0 {
		s.messageClient(c, "254", []string{
			fmt.Sprintf("%d", len(s.Channels)),
			"channels formed",
		})
	}

	// 255 RPL_LUSERME
	s.messageClient(c, "255", []string{
		fmt.Sprintf("I have %d clients and %d servers",
			len(s.Nicks), 0),
	})
}

func (s *Server) motdCommand(c *Client) {
	// 375 RPL_MOTDSTART
	s.messageClient(c, "375", []string{
		fmt.Sprintf("- %s Message of the day - ", s.Config.ServerName),
	})

	// 372 RPL_MOTD
	s.messageClient(c, "372", []string{
		fmt.Sprintf("- %s", s.Config.MOTD),
	})

	// 376 RPL_ENDOFMOTD
	s.messageClient(c, "376", []string{"End of MOTD command"})
}

func (s *Server) quitCommand(c *Client, m irc.Message) {
	msg := "Quit:"
	if len(m.Params) > 0 {
		msg += " " + m.Params[0]
	}

	c.quit(msg)
}

func (s *Server) pingCommand(c *Client, m irc.Message) {
	// Parameters: <server> (I choose to not support forwarding)
	if len(m.Params) == 0 {
		// 409 ERR_NOORIGIN
		s.messageClient(c, "409", []string{"No origin specified"})
		return
	}

	server := m.Params[0]

	if server != s.Config.ServerName {
		// 402 ERR_NOSUCHSERVER
		s.messageClient(c, "402", []string{server, "No such server"})
		return
	}

	s.messageClient(c, "PONG", []string{server})
}

func (s *Server) dieCommand(c *Client, m irc.Message) {
	if !c.isOperator() {
		// 481 ERR_NOPRIVILEGES
		s.messageClient(c, "481", []string{"Permission Denied- You're not an IRC operator"})
		return
	}

	// die is not an RFC command. I use it to shut down the server.
	s.shutdown()
}

func (s *Server) whoisCommand(c *Client, m irc.Message) {
	// Difference from RFC: I support only a single nickname (no mask), and no
	// server target.
	if len(m.Params) == 0 {
		// 431 ERR_NONICKNAMEGIVEN
		s.messageClient(c, "431", []string{"No nickname given"})
		return
	}

	nick := m.Params[0]
	nickCanonical := canonicalizeNick(nick)

	targetClient, exists := s.Nicks[nickCanonical]
	if !exists {
		// 401 ERR_NOSUCHNICK
		s.messageClient(c, "401", []string{nick, "No such nick/channel"})
		return
	}

	// 311 RPL_WHOISUSER
	s.messageClient(c, "311", []string{
		targetClient.DisplayNick,
		targetClient.User,
		fmt.Sprintf("%s", targetClient.IP),
		"*",
		targetClient.RealName,
	})

	// 319 RPL_WHOISCHANNELS
	// I choose to not show any.

	// 312 RPL_WHOISSERVER
	s.messageClient(c, "312", []string{
		targetClient.DisplayNick,
		s.Config.ServerName,
		s.Config.ServerInfo,
	})

	// 301 RPL_AWAY
	// TODO: AWAY not implemented yet.

	// 313 RPL_WHOISOPERATOR
	if targetClient.isOperator() {
		s.messageClient(c, "313", []string{
			targetClient.DisplayNick,
			"is an IRC operator",
		})
	}

	// TODO: TLS information

	// 317 RPL_WHOISIDLE
	idleDuration := time.Now().Sub(targetClient.LastActivityTime)
	idleSeconds := int(idleDuration.Seconds())
	s.messageClient(c, "317", []string{
		targetClient.DisplayNick,
		fmt.Sprintf("%d", idleSeconds),
		"seconds idle",
	})

	// 318 RPL_ENDOFWHOIS
	s.messageClient(c, "318", []string{
		targetClient.DisplayNick,
		"End of WHOIS list",
	})
}

func (s *Server) operCommand(c *Client, m irc.Message) {
	// Parameters: <name> <password>
	if len(m.Params) < 2 {
		// 461 ERR_NEEDMOREPARAMS
		s.messageClient(c, "461", []string{"OPER", "Not enough parameters"})
		return
	}

	if c.isOperator() {
		s.messageClient(c, "ERROR", []string{"You are already an operator."})
		return
	}

	// TODO: Host matching

	// Check if they gave acceptable permissions.
	pass, exists := s.Config.Opers[m.Params[0]]
	if !exists || pass != m.Params[1] {
		// 464 ERR_PASSWDMISMATCH
		s.messageClient(c, "464", []string{"Password incorrect"})
		return
	}

	// Give them oper status.
	c.Modes['o'] = struct{}{}

	c.messageClient(c, "MODE", []string{c.DisplayNick, "+o"})

	// 381 RPL_YOUREOPER
	s.messageClient(c, "381", []string{"You are now an IRC operator"})
}

// MODE command applies either to nicknames or to channels.
func (s *Server) modeCommand(c *Client, m irc.Message) {
	// User mode:
	// Parameters: <nickname> *( ( "+" / "-" ) *( "i" / "w" / "o" / "O" / "r" ) )

	// Channel mode:
	// Parameters: <channel> *( ( "-" / "+" ) *<modes> *<modeparams> )

	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		s.messageClient(c, "461", []string{"MODE", "Not enough parameters"})
		return
	}

	target := m.Params[0]

	// We can have blank mode. This will cause server to send current settings.
	modes := ""
	if len(m.Params) > 1 {
		modes = m.Params[1]
	}

	// Is it a nickname?
	targetClient, exists := s.Nicks[canonicalizeNick(target)]
	if exists {
		s.userModeCommand(c, targetClient, modes)
		return
	}

	// Is it a channel?
	targetChannel, exists := s.Channels[canonicalizeChannel(target)]
	if exists {
		s.channelModeCommand(c, targetChannel, modes)
		return
	}

	// Well... Not found. Send a channel not found. It seems the closest matching
	// extant error in RFC.
	// 403 ERR_NOSUCHCHANNEL
	s.messageClient(c, "403", []string{target, "No such channel"})
}

func (s *Server) userModeCommand(c, targetClient *Client, modes string) {
	// They can only change their own mode.
	if targetClient != c {
		// 502 ERR_USERSDONTMATCH
		s.messageClient(c, "502", []string{"Cannot change mode for other users"})
		return
	}

	// No modes given means we should send back their current mode.
	if len(modes) == 0 {
		modeReturn := "+"
		for k := range c.Modes {
			modeReturn += string(k)
		}

		// 221 RPL_UMODEIS
		s.messageClient(c, "221", []string{modeReturn})
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
			s.messageClient(c, "ERROR", []string{"Malformed MODE"})
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
			s.messageClient(c, "501", []string{"Unknown MODE flag"})
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

func (s *Server) channelModeCommand(c *Client, channel *Channel,
	modes string) {
	if !c.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		s.messageClient(c, "442", []string{channel.Name, "You're not on that channel"})
		return
	}

	// No modes? Send back the channel's modes.
	// Always send back +n. That's only I support right now.
	if len(modes) == 0 {
		// 324 RPL_CHANNELMODEIS
		s.messageClient(c, "324", []string{channel.Name, "+n"})
		return
	}

	// Listing bans. I don't support bans at this time, but say that there are
	// none.
	if modes == "b" || modes == "+b" {
		// 368 RPL_ENDOFBANLIST
		s.messageClient(c, "368", []string{channel.Name, "End of channel ban list"})
		return
	}

	// Since we don't have channel operators implemented, any attempt to alter
	// mode is an error.
	// 482 ERR_CHANOPRIVSNEEDED
	s.messageClient(c, "482", []string{channel.Name, "You're not channel operator"})
}

func (s *Server) whoCommand(c *Client, m irc.Message) {
	// Contrary to RFC 2812, I support only 'WHO #channel'.
	if len(m.Params) < 1 {
		// 461 ERR_NEEDMOREPARAMS
		s.messageClient(c, "461", []string{m.Command, "Not enough parameters"})
		return
	}

	channel, exists := s.Channels[canonicalizeChannel(m.Params[0])]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.Server.messageClient(c, "403", []string{m.Params[0], "Invalid channel name"})
		return
	}

	// Only works if they are on the channel.
	if !c.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		s.messageClient(c, "442", []string{channel.Name, "You're not on that channel"})
		return
	}

	for _, member := range channel.Members {
		// 352 RPL_WHOREPLY
		// "<channel> <user> <host> <server> <nick>
		// ( "H" / "G" > ["*"] [ ( "@" / "+" ) ]
		// :<hopcount> <real name>"
		// NOTE: I'm not sure what H/G mean.
		// Hopcount seems unimportant also.
		mode := "H"
		if member.isOperator() {
			mode += "*"
		}
		s.messageClient(c, "352", []string{
			channel.Name, member.User, fmt.Sprintf("%s", member.IP),
			s.Config.ServerName, member.DisplayNick,
			mode, "0 " + member.RealName,
		})
	}

	// 315 RPL_ENDOFWHO
	s.messageClient(c, "315", []string{channel.Name, "End of WHO list"})
}

func (s *Server) topicCommand(c *Client, m irc.Message) {
	// Params: <channel> [ <topic> ]
	if len(m.Params) == 0 {
		s.messageClient(c, "461", []string{m.Command, "Not enough parameters"})
		return
	}

	channelName := canonicalizeChannel(m.Params[0])
	channel, exists := s.Channels[channelName]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.Server.messageClient(c, "403", []string{m.Params[0], "Invalid channel name"})
		return
	}

	if !c.onChannel(channel) {
		// 442 ERR_NOTONCHANNEL
		s.messageClient(c, "442", []string{channel.Name, "You're not on that channel"})
		return
	}

	// If there is no new topic, then just send back the current one.
	if len(m.Params) < 2 {
		if len(channel.Topic) == 0 {
			// 331 RPL_NOTOPIC
			s.messageClient(c, "331", []string{channel.Name, "No topic is set"})
			return
		}

		// 332 RPL_TOPIC
		s.messageClient(c, "332", []string{channel.Name, channel.Topic})
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
