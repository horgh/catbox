/*
 * IRC daemon.
 */

package main

import (
	"flag"
	"fmt"
	"log"
	"net"

	"summercat.com/irc"
)

// Client holds state about a single client connection.
type Client struct {
	conn irc.Conn

	writeChan chan irc.Message

	// A unique id.
	id uint64

	ip net.IP

	// Whether it completed connection registration.
	registered bool

	nick string

	user string

	realname string

	Channels map[string]*Channel
}

// Server holds the state for a server.
// I put everything global to a server in an instance of struct rather than
// have global variables.
type Server struct {
	config irc.Config

	// clients maps unique client id to client state.
	Clients map[uint64]*Client

	// clients maps nickname (canonicalized) to client state.
	// The nick must be canonicalized.
	// The reason I have this as well as clients is to track unregistered
	// clients.
	Nicks map[string]*Client

	// The channel must be canonicalized.
	Channels map[string]*Channel
}

// Channel holds everything to do with a channel.
type Channel struct {
	Name string

	Members map[uint64]*Client

	// TODO: Modes
	// TODO: Topic
}

// ClientMessage holds a message and the client it originated from.
type ClientMessage struct {
	client  *Client
	message irc.Message
}

// Args are command line arguments.
type Args struct {
	configFile string
}

func main() {
	log.SetFlags(0)

	args, err := getArgs()
	if err != nil {
		log.Fatal(err)
	}

	config, err := irc.LoadConfig(args.configFile)
	if err != nil {
		log.Fatal(err)
	}

	server := newServer(config)

	err = server.start()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Server shutdown cleanly.")
}

func getArgs() (Args, error) {
	configFile := flag.String("config", "", "Configuration file.")

	flag.Parse()

	if len(*configFile) == 0 {
		flag.PrintDefaults()
		return Args{}, fmt.Errorf("You must provie a configuration file.")
	}

	return Args{configFile: *configFile}, nil
}

func newServer(config irc.Config) *Server {
	s := Server{config: config}
	s.Clients = map[uint64]*Client{}
	s.Nicks = map[string]*Client{}
	s.Channels = map[string]*Channel{}

	return &s
}

// start starts up the server.
//
// We open the TCP port, open our channels, and then act based on messages on
// the channels.
func (s *Server) start() error {
	// TODO: TLS
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%s", s.config["listen-host"],
		s.config["listen-port"]))
	if err != nil {
		return fmt.Errorf("Unable to listen: %s", err)
	}

	// We hear about new client connections on this channel.
	newClientChan := make(chan *Client, 100)

	// We hear messages from clients on this channel.
	messageServerChan := make(chan ClientMessage, 100)

	go acceptConnections(ln, newClientChan, messageServerChan)

	for {
		select {
		case client := <-newClientChan:
			log.Printf("New client connection: %s", client)
			s.Clients[client.id] = client

		case clientMessage := <-messageServerChan:
			log.Printf("Client %s: Message: %s", clientMessage.client,
				clientMessage.message)
			s.handleMessage(clientMessage)
		}
	}
}

// acceptConnections accepts TCP connections and tells the main server loop
// through a channel. It sets up separate goroutines for reading/writing to
// and from the client.
func acceptConnections(ln net.Listener, newClientChan chan<- *Client,
	messageServerChan chan<- ClientMessage) {

	id := uint64(0)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %s", err)
			continue
		}

		clientWriteChan := make(chan irc.Message, 100)

		client := &Client{
			conn:      irc.NewConn(conn),
			writeChan: clientWriteChan,
			id:        id,
			Channels:  make(map[string]*Channel),
		}

		// TODO: Handle rollover
		id++

		tcpAddr, err := net.ResolveTCPAddr("tcp", conn.RemoteAddr().String())
		// This shouldn't happen.
		if err != nil {
			log.Printf("Unable to resolve TCP address: %s", err)
		}

		client.ip = tcpAddr.IP

		newClientChan <- client

		go client.read(messageServerChan)
		go client.write()
	}
}

// Send an IRC message to a client. Appears to be from the server.
// This works by writing to a client's channel.
func (s *Server) messageClient(c *Client, command string, params []string) {
	// For numeric messages, we need to prepend the nick.
	// Use * for the nick in cases where the client doesn't have one yet.
	// This is what ircd-ratbox does. Maybe not RFC...
	isNumeric := true
	for _, c := range command {
		if c < 48 || c > 57 {
			isNumeric = false
		}
	}

	if isNumeric {
		nick := "*"
		if len(c.nick) > 0 {
			nick = c.nick
		}
		newParams := []string{nick}

		newParams = append(newParams, params...)
		params = newParams
	}

	c.writeChan <- irc.Message{
		Prefix:  s.config["server-name"],
		Command: command,
		Params:  params,
	}
}

// Send an IRC message to a client. From the server, but appears to be from
// the client (by prefix).
// This works by writing to a client's channel.
func (c *Client) messageClient(from *Client, command string, params []string) {
	c.writeChan <- irc.Message{
		Prefix:  from.nickUhost(),
		Command: command,
		Params:  params,
	}
}

// handleMessage takes action based on a client's IRC message.
func (s *Server) handleMessage(cm ClientMessage) {
	// Clients SHOULD NOT (section 2.3) send a prefix. I'm going to disallow it
	// completely.
	if cm.message.Prefix != "" {
		// TODO: For ERROR cases, does RFC mean we should close the connection?
		//   It seems ambiguous.
		s.messageClient(cm.client, "ERROR", []string{"Do not send a prefix"})
		return
	}

	if cm.message.Command == "NICK" {
		// We should have one parameter: The nick they want.
		if len(cm.message.Params) == 0 {
			// 431 ERR_NONICKNAMEGIVEN
			s.messageClient(cm.client, "431", []string{"No nickname given"})
			return
		}

		// We could check if there are more than 1 parameters. But it doesn't
		// seem particularly problematic if there are. We ignore them.
		// There's not a good error to raise in RFC even if we did check.

		nick := cm.message.Params[0]

		if !irc.IsValidNick(nick) {
			// 432 ERR_ERRONEUSNICKNAME
			s.messageClient(cm.client, "432", []string{nick, "Erroneous nickname"})
			return
		}

		// Nick must be caselessly unique.
		nickCanon := irc.CanonicalizeNick(nick)

		// Is the nick taken already?
		_, exists := s.Nicks[nickCanon]
		if exists {
			// 433 ERR_NICKNAMEINUSE
			s.messageClient(cm.client, "432",
				[]string{nick, "Nickname is already in use"})
			return
		}

		// Flag the nick as taken by this client.
		s.Nicks[nickCanon] = cm.client
		oldNick := cm.client.nick
		cm.client.nick = nick

		// The NICK command to happen both at connection registration time and
		// after. There are different rules.

		// Free the old nick (if there is one).
		// I do this in both registered and not states in case there are clients
		// misbehaving. I suppose we could not let them issue any more NICKs
		// beyond the first too if they are not registered.
		if len(oldNick) > 0 {
			delete(s.Nicks, irc.CanonicalizeNick(oldNick))
		}

		if cm.client.registered {
			// TODO: We need to inform other clients about the nick change.

			// TODO: Reply to the client.
		}

		// We don't reply during registration (we don't have enough info).

		return
	}

	if cm.message.Command == "USER" {
		// The USER command only occurs during connection registration.
		if cm.client.registered {
			// 462 ERR_ALREADYREGISTRED
			s.messageClient(cm.client, "462",
				[]string{"Unauthorized command (already registered)"})
			return
		}

		// I'm going to require NICK first. RFC recommends this.
		if len(cm.client.nick) == 0 {
			// No good error code that I see.
			s.messageClient(cm.client, "ERROR", []string{"Please send NICK first"})
			return
		}

		// 4 parameters: <user> <mode> <unused> <realname>
		if len(cm.message.Params) != 4 {
			// 461 ERR_NEEDMOREPARAMS
			s.messageClient(cm.client, "461",
				[]string{cm.message.Command, "Not enough parameters"})
			return
		}

		user := cm.message.Params[0]

		if !irc.IsValidUser(user) {
			// There isn't an appropriate response in the RFC. ircd-ratbox sends an
			// ERROR message. Do that.
			s.messageClient(cm.client, "ERROR", []string{"Invalid username"})
			return
		}
		cm.client.user = user

		// TODO: Apply user mode

		// TODO: Validate realname

		cm.client.realname = cm.message.Params[3]

		// This completes connection registration.

		cm.client.registered = true

		// 001 RPL_WELCOME
		s.messageClient(cm.client, "001",
			[]string{
				fmt.Sprintf("Welcome to the Internet Relay Network %s",
					cm.client.nickUhost())})
		return
	}

	// Let's say *all* other commands require you to be registered.
	// This is likely stricter than RFC.
	if !cm.client.registered {
		// 451 ERR_NOTREGISTERED
		s.messageClient(cm.client, "451",
			[]string{fmt.Sprintf("You have not registered.")})
		return
	}

	if cm.message.Command == "JOIN" {
		s.joinCommand(cm.client, cm.message)
		return
	}

	if cm.message.Command == "PART" {
		s.partCommand(cm.client, cm.message)
		return
	}

	if cm.message.Command == "PRIVMSG" {
		s.privmsgCommand(cm.client, cm.message)
		return
	}

	// Unknown command. We don't handle it yet anyway.

	// 421 ERR_UNKNOWNCOMMAND
	s.messageClient(cm.client, "421",
		[]string{cm.message.Command, "Unknown command"})
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
		// TODO: Part all. Inform other clients. Inform the client.
		return
	}

	// Again, we could check if there are too many parameters, but we just
	// ignore them.

	channels, err := irc.ParseChannels(m.Params[0])
	if err != nil {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		s.messageClient(c, "403", []string{channels[len(channels)-1], err.Error()})
		return
	}

	// TODO: Support keys.

	// Try to join the client to the channel(s)

	for _, channelName := range channels {
		// Is the client in the channel already?
		for _, channel := range c.Channels {
			if channel.Name == channelName {
				// I don't see a good error code for this.
				s.messageClient(c, "ERROR", []string{"You are on that channel"})
				// We could try to process any remaining channels. But let's not.
				// We could just ignore this too...
				return
			}
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
		channel.Members[c.id] = c
		c.Channels[channelName] = channel

		// Tell the client about the join. This is what RFC says to send:
		// Send JOIN, RPL_TOPIC, and RPL_NAMREPLY.

		// JOIN comes from the client.
		c.messageClient(c, "JOIN", []string{channel.Name})

		// It appears RPL_TOPIC is optional, at least ircd-ratbox does not send it.
		// Presumably if there is no topic.
		// TODO: Send topic when we have one.

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
				"=", channel.Name, fmt.Sprintf(":%s", member.nick),
			})
		}

		// 366 RPL_ENDOFNAMES
		s.messageClient(c, "366", []string{channel.Name, "End of NAMES list"})

		// Tell each member in the channel about the client.
		for _, member := range channel.Members {
			// Don't tell the client. We already did.
			if member.id == c.id {
				continue
			}
			member.messageClient(c, "JOIN", []string{channel.Name})
		}
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

	channels, err := irc.ParseChannels(m.Params[0])
	if err != nil {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		s.messageClient(c, "403",
			[]string{channels[len(channels)-1], "No such channel"})
		return
	}

	for _, channelName := range channels {
		// Find the channel.
		channel, exists := s.Channels[channelName]
		if !exists {
			// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
			s.messageClient(c, "403", []string{channelName, "No such channel"})
			// We could have additional channels to work on. But don't.
			return
		}

		// Are they on the channel?
		if !isOnChannel(channel, c) {
			// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
			s.messageClient(c, "403", []string{channelName, "No such channel"})
			// We could have additional channels to work on. But don't.
			return
		}

		// Tell everyone (including the client) about the part.
		for _, member := range channel.Members {
			params := []string{channelName}
			if len(params) == 2 {
				params = append(params, params[1])
			}
			member.messageClient(c, "PART", params)
		}

		// Remove the client from the channel.
		delete(channel.Members, c.id)
		delete(c.Channels, channel.Name)

		// If they are the last member, then drop the channel completely.
		if len(channel.Members) == 0 {
			delete(s.Channels, channel.Name)
		}
	}
}

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

	// I only support # channels right now.

	if target[0] == '#' {
		channelName := irc.CanonicalizeChannel(target)
		if !irc.IsValidChannel(channelName) {
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
		_, exists = channel.Members[c.id]
		if !exists {
			// 404 ERR_CANNOTSENDTOCHAN
			s.messageClient(c, "404", []string{channelName, "Cannot send to channel"})
			return
		}

		// Send to all members of the channel. Except the client itself it seems.
		for _, member := range channel.Members {
			if member.id == c.id {
				continue
			}
			member.messageClient(c, "PRIVMSG", []string{channel.Name, m.Params[1]})
		}

		return
	}

	// It's a nick.
	nickName := irc.CanonicalizeNick(target)
	if !irc.IsValidNick(nickName) {
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

	targetClient.messageClient(c, "PRIVMSG", []string{nickName, m.Params[1]})
}

func isOnChannel(channel *Channel, client *Client) bool {
	for _, member := range channel.Members {
		if member.id == client.id {
			return true
		}
	}

	return false
}

// read endlessly reads from the client's TCP connection. It parses each IRC
// protocol message and passes it to the server through the server's channel.
func (c *Client) read(messageServerChan chan<- ClientMessage) {
	for {
		buf, err := c.conn.Read()
		if err != nil {
			log.Printf("Client %s: Read error: %s", c, err)
			// TODO: We need to inform the server if we're giving up on this client.
			return
		}

		message, err := irc.ParseMessage(buf)
		if err != nil {
			// TODO: We need to inform the server if we're giving up on this client.
			log.Printf("Client %s: Invalid message: %s", c, err)
			return
		}

		messageServerChan <- ClientMessage{
			client:  c,
			message: message,
		}
	}
}

// write endlessly reads from the client's channel, encodes each message, and
// writes it to the client's TCP connection.
func (c *Client) write() {
	for message := range c.writeChan {
		buf, err := message.Encode()
		if err != nil {
			// TODO: We need to inform the server if we're giving up on this client.
			log.Printf("Client %s: Unable to encode message: %s", c, err)
			return
		}

		err = c.conn.Write(buf)
		if err != nil {
			// TODO: We need to inform the server if we're giving up on this client.
			log.Printf("Client %s: Unable to write message: %s", c, err)
			return
		}
	}
}

func (c *Client) String() string {
	return fmt.Sprintf("%s", c.conn.Conn.RemoteAddr())
}

func (c *Client) nickUhost() string {
	return fmt.Sprintf("%s!~%s@%s", c.nick, c.user, c.ip)
}
