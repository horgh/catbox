/*
 * IRC daemon.
 */

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"summercat.com/irc"
)

// Client holds state about a single client connection.
type Client struct {
	Conn irc.Conn

	WriteChan chan irc.Message

	// A unique id.
	ID uint64

	IP net.IP

	// Whether it completed connection registration.
	Registered bool

	// Not canonicalized
	Nick string

	User string

	RealName string

	// Channel name (canonicalized) to Channel.
	Channels map[string]*Channel

	Server *Server

	LastActivityTime time.Time
}

// Channel holds everything to do with a channel.
type Channel struct {
	// Canonicalized.
	Name string

	// Client id to Client.
	Members map[uint64]*Client

	// TODO: Modes

	// TODO: Topic
}

// Server holds the state for a server.
// I put everything global to a server in an instance of struct rather than
// have global variables.
type Server struct {
	Config irc.Config

	// Client id to Client.
	Clients map[uint64]*Client

	// Canoncalized nickname to Client.
	// The reason I have this as well as clients is to track unregistered
	// clients.
	Nicks map[string]*Client

	// Channel name (canonicalized) to Channel.
	Channels map[string]*Channel
}

// ClientMessage holds a message and the client it originated from.
type ClientMessage struct {
	Client  *Client
	Message irc.Message
}

// Args are command line arguments.
type Args struct {
	ConfigFile string
}

const maxNickLength = 9

const maxChannelLength = 50

//const idleTimeBeforePing = time.Minute
const idleTimeBeforePing = 10 * time.Second

//const idleTimeBeforeDead = 3 * time.Minute
const idleTimeBeforeDead = 30 * time.Second

func main() {
	log.SetFlags(0)

	args, err := getArgs()
	if err != nil {
		log.Fatal(err)
	}

	config, err := irc.LoadConfig(args.ConfigFile)
	if err != nil {
		log.Fatal(err)
	}

	server, err := newServer(config)
	if err != nil {
		log.Fatal(err)
	}

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

	return Args{ConfigFile: *configFile}, nil
}

func newServer(config irc.Config) (*Server, error) {
	s := Server{
		Config: config,
	}

	err := s.checkConfig()
	if err != nil {
		return nil, fmt.Errorf("Configuration problem: %s", err)
	}

	s.Clients = map[uint64]*Client{}
	s.Nicks = map[string]*Client{}
	s.Channels = map[string]*Channel{}

	return &s, nil
}

// checkConfig checks configuration keys are present and in an acceptable
// format.
func (s *Server) checkConfig() error {
	requiredKeys := []string{
		"listen-port",
		"listen-host",
		"server-name",
		"version",
		"created-date",
		"motd",
	}

	// TODO: Check format of each

	for _, key := range requiredKeys {
		v, exists := s.Config[key]
		if !exists {
			return fmt.Errorf("Missing required key: %s", key)
		}

		if len(v) == 0 {
			return fmt.Errorf("Configuration value is blank: %s", key)
		}
	}

	return nil
}

// start starts up the server.
//
// We open the TCP port, open our channels, and then act based on messages on
// the channels.
func (s *Server) start() error {
	// TODO: TLS
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%s", s.Config["listen-host"],
		s.Config["listen-port"]))
	if err != nil {
		return fmt.Errorf("Unable to listen: %s", err)
	}

	// We hear about new client connections on this channel.
	newClientChan := make(chan *Client, 100)

	// We hear messages from clients on this channel.
	messageServerChan := make(chan ClientMessage, 100)

	// We hear messages when client read/write fails so we can clean up the
	// client.
	deadClientChan := make(chan *Client, 100)

	go s.acceptConnections(ln, newClientChan, messageServerChan, deadClientChan)

	// Alarm is a goroutine to wake up this one periodically so we can do things
	// like ping clients.
	// One channel is for the alarm to send to the server.
	// The other the server responds to the alarm. The server will close it to
	// tell alarm to end.
	fromAlarmChan := make(chan struct{})
	toAlarmChan := make(chan struct{})
	go s.alarm(fromAlarmChan, toAlarmChan)

	for {
		select {
		case client := <-newClientChan:
			log.Printf("New client connection: %s", client)
			s.Clients[client.ID] = client
			client.LastActivityTime = time.Now()

		case client := <-deadClientChan:
			log.Printf("Client %s died.", client)
			// It's possible we already know about it.
			_, exists := s.Clients[client.ID]
			if exists {
				client.quit("I/O error")
			}

		case clientMessage := <-messageServerChan:
			log.Printf("Client %s: Message: %s", clientMessage.Client,
				clientMessage.Message)

			// Possibly from a client that disconnected.
			_, exists := s.Clients[clientMessage.Client.ID]
			if !exists {
				log.Printf("Ignoring message from disconnected client.")
			} else {
				s.handleMessage(clientMessage.Client, clientMessage.Message)
			}
		case <-fromAlarmChan:
			toAlarmChan <- struct{}{}
			// TODO: For clean shutdown we need to close the toAlarmChan.
			s.checkAndPingClients()
		}
	}
}

// acceptConnections accepts TCP connections and tells the main server loop
// through a channel. It sets up separate goroutines for reading/writing to
// and from the client.
func (s *Server) acceptConnections(ln net.Listener,
	newClientChan chan<- *Client, messageServerChan chan<- ClientMessage,
	deadClientChan chan<- *Client) {
	id := uint64(0)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %s", err)
			continue
		}

		clientWriteChan := make(chan irc.Message, 100)

		client := &Client{
			Conn:      irc.NewConn(conn),
			WriteChan: clientWriteChan,
			ID:        id,
			Channels:  make(map[string]*Channel),
			Server:    s,
		}

		// Handle rollover of uint64. Unlikely to happen (outside abuse) but.
		if id+1 == 0 {
			log.Fatalf("Unique ids rolled over!")
		}
		id++

		tcpAddr, err := net.ResolveTCPAddr("tcp", conn.RemoteAddr().String())
		// This shouldn't happen.
		if err != nil {
			log.Fatalf("Unable to resolve TCP address: %s", err)
		}

		client.IP = tcpAddr.IP

		go client.readLoop(messageServerChan, deadClientChan)
		go client.writeLoop(deadClientChan)

		newClientChan <- client
	}
}

// Alarm sends a message to the server goroutine to wake it up.
// It then receives a message from the server. It does this so it can know if
// the server wants it to shut down.
// After both of these actions it will sleep before repeating.
func (s *Server) alarm(toServer chan<- struct{}, fromServer <-chan struct{}) {
	for {
		//time.Sleep(time.Minute)
		time.Sleep(time.Second)

		toServer <- struct{}{}

		_, ok := <-fromServer
		if !ok {
			log.Printf("Alarm shutting down.")
			return
		}
	}
}

// checkAndPingClients looks at each connected client.
//
// If they've been idle a short time, we send them a PING (if they're
// registered).
//
// If they've been idle a long time, we kill their connection.
func (s *Server) checkAndPingClients() {
	now := time.Now()

	for _, client := range s.Clients {
		timeIdle := now.Sub(client.LastActivityTime)

		if client.Registered {
			if timeIdle < idleTimeBeforePing {
				continue
			}

			if timeIdle > idleTimeBeforeDead {
				client.quit(fmt.Sprintf("Ping timeout: %d seconds",
					int(timeIdle.Seconds())))
				continue
			}

			s.messageClient(client, "PING", []string{s.Config["server-name"]})
			continue
		}

		if timeIdle > idleTimeBeforeDead {
			client.quit("Idle too long.")
		}
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
		if len(c.Nick) > 0 {
			nick = c.Nick
		}
		newParams := []string{nick}

		newParams = append(newParams, params...)
		params = newParams
	}

	c.WriteChan <- irc.Message{
		Prefix:  s.Config["server-name"],
		Command: command,
		Params:  params,
	}
}

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

	if m.Command == "PRIVMSG" {
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
		s.messageClient(c, "432", []string{nick, "Nickname is already in use"})
		return
	}

	// Flag the nick as taken by this client.
	s.Nicks[nickCanon] = c
	oldNick := c.Nick

	// The NICK command to happen both at connection registration time and
	// after. There are different rules.

	// Free the old nick (if there is one).
	// I do this in both registered and not states in case there are clients
	// misbehaving. I suppose we could not let them issue any more NICKs
	// beyond the first too if they are not registered.
	if len(oldNick) > 0 {
		delete(s.Nicks, canonicalizeNick(oldNick))
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
	c.Nick = nick
}

func (s *Server) userCommand(c *Client, m irc.Message) {
	// The USER command only occurs during connection registration.
	if c.Registered {
		// 462 ERR_ALREADYREGISTRED
		s.messageClient(c, "462",
			[]string{"Unauthorized command (already registered)"})
		return
	}

	// I'm going to require NICK before user. RFC RECOMMENDs this.
	if len(c.Nick) == 0 {
		// No good error code that I see.
		s.messageClient(c, "ERROR", []string{"Please send NICK first"})
		return
	}

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

	// TODO: Validate realname

	c.RealName = m.Params[3]

	// This completes connection registration.

	c.Registered = true

	// RFC 2813 specifies messages to send upon registration.

	// 001 RPL_WELCOME
	s.messageClient(c, "001", []string{
		fmt.Sprintf("Welcome to the Internet Relay Network %s", c.nickUhost()),
	})

	// 002 RPL_YOURHOST
	s.messageClient(c, "002", []string{
		fmt.Sprintf("Your host is %s, running version %s", s.Config["server-name"],
			s.Config["version"]),
	})

	// 003 RPL_CREATED
	s.messageClient(c, "003", []string{
		fmt.Sprintf("This server was created %s", s.Config["created-date"]),
	})

	// 004 RPL_MYINFO
	// <servername> <version> <available user modes> <available channel modes>
	s.messageClient(c, "004", []string{
		// It seems ambiguous if these are to be separate parameters.
		s.Config["server-name"],
		s.Config["version"],
		"io",
		"ntsi",
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
			"=", channel.Name, fmt.Sprintf(":%s", member.Nick),
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
	msgLen := len(":") + len(c.nickUhost()) + len(" PRIVMSG ") + len(target) +
		len(" ") + len(":") + len(msg) + len("\r\n")
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
			c.messageClient(member, "PRIVMSG", []string{channel.Name, msg})
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

	c.messageClient(targetClient, "PRIVMSG", []string{nickName, msg})
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
	// TODO: When we have operators.

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
		fmt.Sprintf("- %s Message of the day - ", s.Config["server-name"]),
	})

	// 372 RPL_MOTD
	s.messageClient(c, "372", []string{
		fmt.Sprintf("- %s", s.Config["motd"]),
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

	if server != s.Config["server-name"] {
		// 402 ERR_NOSUCHSERVER
		s.messageClient(c, "402", []string{server, "No such server"})
		return
	}

	s.messageClient(c, "PONG", []string{server})
}

// Send an IRC message to a client from another client.
// The server is the one sending it, but it appears from the client through use
// of the prefix.
//
// This works by writing to a client's channel.
func (c *Client) messageClient(to *Client, command string, params []string) {
	to.WriteChan <- irc.Message{
		Prefix:  c.nickUhost(),
		Command: command,
		Params:  params,
	}
}

func (c *Client) onChannel(channel *Channel) bool {
	_, exists := c.Channels[channel.Name]
	return exists
}

// readLoop endlessly reads from the client's TCP connection. It parses each
// IRC protocol message and passes it to the server through the server's
// channel.
func (c *Client) readLoop(messageServerChan chan<- ClientMessage,
	deadClientChan chan<- *Client) {
	for {
		message, err := c.Conn.ReadMessage()
		if err != nil {
			log.Printf("Client %s: %s", c, err)
			deadClientChan <- c
			return
		}

		messageServerChan <- ClientMessage{
			Client:  c,
			Message: message,
		}
	}
}

// writeLoop endlessly reads from the client's channel, encodes each message,
// and writes it to the client's TCP connection.
func (c *Client) writeLoop(deadClientChan chan<- *Client) {
	for message := range c.WriteChan {
		err := c.Conn.WriteMessage(message)
		if err != nil {
			log.Printf("Client %s: %s", c, err)
			deadClientChan <- c
			break
		}
	}

	// Close the TCP connection. We do this here because we need to be sure we've
	// processed all messages to the client before closing the socket.
	err := c.Conn.Close()
	if err != nil {
		log.Printf("Client %s: Problem closing connection: %s", c, err)
	}

	log.Printf("Client %s write goroutine terminating.", c)
}

func (c *Client) String() string {
	return fmt.Sprintf("%d %s", c.ID, c.Conn.RemoteAddr())
}

func (c *Client) nickUhost() string {
	return fmt.Sprintf("%s!~%s@%s", c.Nick, c.User, c.IP)
}

// part tries to remove the client from the channel.
//
// We send a reply to the client. We also inform any other clients that need to
// know.
func (c *Client) part(channelName, message string) {
	// NOTE: Difference from RFC 2812: I only accept one channel at a time.
	channelName = canonicalizeChannel(channelName)

	if !isValidChannel(channelName) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.Server.messageClient(c, "403", []string{channelName, "Invalid channel name"})
		return
	}

	// Find the channel.
	channel, exists := c.Server.Channels[channelName]
	if !exists {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.Server.messageClient(c, "403", []string{channelName, "No such channel"})
		return
	}

	// Are they on the channel?
	if !c.onChannel(channel) {
		// 403 ERR_NOSUCHCHANNEL. Used to indicate channel name is invalid.
		c.Server.messageClient(c, "403", []string{channelName, "You are not on that channel"})
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

func (c *Client) quit(msg string) {
	if c.Registered {
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

		delete(c.Server.Nicks, canonicalizeNick(c.Nick))
	} else {
		// May have set a nick.
		if len(c.Nick) > 0 {
			delete(c.Server.Nicks, canonicalizeNick(c.Nick))
		}
	}

	c.Server.messageClient(c, "ERROR", []string{msg})

	// Close their connection and channels.
	// Closing the channel leads to closing the TCP connection.
	close(c.WriteChan)

	delete(c.Server.Clients, c.ID)
}

// canonicalizeNick converts the given nick to its canonical representation
// (which must be unique).
//
// Note: We don't check validity or strip whitespace.
func canonicalizeNick(n string) string {
	return strings.ToLower(n)
}

// canonicalizeChannel converts the given channel to its canonical
// representation (which must be unique).
//
// Note: We don't check validity or strip whitespace.
func canonicalizeChannel(c string) string {
	return strings.ToLower(c)
}

// isValidNick checks if a nickname is valid.
func isValidNick(n string) bool {
	if len(n) == 0 || len(n) > maxNickLength {
		return false
	}

	// TODO: For now I accept only a-z, 0-9, or _. RFC is more lenient.
	for i, char := range n {
		if char >= 'a' && char <= 'z' {
			continue
		}

		if char >= '0' && char <= '9' {
			// No digits in first position.
			if i == 0 {
				return false
			}
			continue
		}

		if char == '_' {
			continue
		}

		return false
	}

	return true
}

// isValidUser checks if a user (USER command) is valid
func isValidUser(u string) bool {
	if len(u) == 0 || len(u) > maxNickLength {
		return false
	}

	// TODO: For now I accept only a-z or 0-9. RFC is more lenient.
	for _, char := range u {
		if char >= 'a' && char <= 'z' {
			continue
		}

		if char >= '0' && char <= '9' {
			continue
		}

		return false
	}

	return true
}

// isValidChannel checks a channel name for validity.
//
// You should canonicalize it before using this function.
func isValidChannel(c string) bool {
	if len(c) == 0 || len(c) > maxChannelLength {
		return false
	}

	// TODO: I accept only a-z or 0-9 as valid characters right now. RFC
	//   accepts more.
	for i, char := range c {
		if i == 0 {
			// TODO: I only allow # channels right now.
			if char == '#' {
				continue
			}
			return false
		}

		if char >= 'a' && char <= 'z' {
			continue
		}

		if char >= '0' && char <= '9' {
			continue
		}

		return false
	}

	return true
}
