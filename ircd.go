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

	ip net.IP

	// Whether it completed connection registration.
	registered bool

	nick string

	user string

	realname string
}

// Server holds the state for a server.
// I put everything global to a server in an instance of struct rather than
// have global variables.
type Server struct {
	config irc.Config

	// clients maps nickname (canonicalized) to client state.
	clients map[string]*Client
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
	s.clients = map[string]*Client{}

	return &s
}

// start starts up the server.
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
			// TODO: Record unregistered

		case clientMessage := <-messageServerChan:
			log.Printf("Client %s: Message: %s", clientMessage.client,
				clientMessage.message)
			s.handleMessage(clientMessage)
		}
	}
}

// acceptConnections accepts connections and tells the main loop through a
// channel.
func acceptConnections(ln net.Listener, newClientChan chan<- *Client,
	messageServerChan chan<- ClientMessage) {
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
		}

		tcpAddr, err := net.ResolveTCPAddr("tcp", conn.RemoteAddr().String())
		client.ip = tcpAddr.IP

		newClientChan <- client

		go client.read(messageServerChan)
		go client.write()
	}
}

func (s *Server) messageClient(c *Client, command string, params []string) {
	client.writeChan <- irc.Message{
		Prefix:  s.config["server-name"],
		Command: command,
		params:  params,
	}
}

func (s *Server) handleMessage(cm ClientMessage) {
	// Clients SHOULD NOT (section 2.3) send a prefix. I'm going to disallow it
	// completely.
	if cm.message.Prefix != "" {
		s.messageClient(cm.client, "ERROR", []string{"Do not send a prefix"})
		return
	}

	if cm.message.Command == "NICK" {
		if len(cm.message.Params) == 0 {
			cm.client.writeChan <- irc.Message{
				Prefix: s.config["server-name"],
				// 431 ERR_NONICKNAMEGIVEN
				Command: "431",
				Params:  []string{"No nickname given"},
			}
			return
		}

		if len(cm.message.Params) > 1 {
			cm.client.writeChan <- irc.Message{
				Prefix: s.config["server-name"],
				// 432 ERR_ERRONEUSNICKNAME
				Command: "432",
				Params:  []string{"Erroneous nickname"},
			}
			return
		}

		nick := cm.message.Params[0]

		if !irc.IsValidNick(nick) {
			cm.client.writeChan <- irc.Message{
				Prefix: s.config["server-name"],
				// 432 ERR_ERRONEUSNICKNAME
				Command: "432",
				Params:  []string{"Erroneous nickname"},
			}
			return
		}

		nickCanon := irc.CanonicalizeNick(nick)

		_, exists := s.clients[nickCanon]
		if exists {
			cm.client.writeChan <- irc.Message{
				Prefix: s.config["server-name"],
				// 433 ERR_NICKNAMEINUSE
				Command: "432",
				Params:  []string{"Nickname is already in use"},
			}
			return
		}

		s.clients[nickCanon] = cm.client
		cm.client.nick = nick

		if cm.client.registered {
			// TODO: If this is not a connection registration, then we also need
			//   to remove the old nickname from the map.

			// TODO: If the client is registered then we need to inform other clients
			//   about the nick change.

			// TODO: Reply.
		} else {
			// We don't reply to this during registration (we don't have enough
			// info).

			// TODO: Remove from unregistered? Not fully registered, but now have in
			//   clients map.
		}

		return
	}

	if cm.message.Command == "USER" {
		if cm.client.registered {
			cm.client.writeChan <- irc.Message{
				Prefix: s.config["server-name"],
				// 462 ERR_ALREADYREGISTRED
				Command: "461",
				Params:  []string{"Unauthorized command (already registered)"},
			}
			return
		}

		// I'm going to require NICK first. RFC recommends this.
		if len(cm.client.nick) == 0 {
			cm.client.writeChan <- irc.Message{
				Prefix: s.config["server-name"],
				// No good error code that I see.
				Command: "ERROR",
				Params:  []string{"Please send NICK first"},
			}
			return
		}

		if len(cm.message.Params) != 4 {
			cm.client.writeChan <- irc.Message{
				Prefix: s.config["server-name"],
				// 461 ERR_NEEDMOREPARAMS
				Command: "461",
				Params:  []string{"Not enough parameters"},
			}
			return
		}

		user := cm.message.Params[0]
		realname := cm.message.Params[3]

		if !irc.IsValidUser(user) {
			// There isn't an appropriate response in the RFC.
			// ircd-ratbox sends an ERROR message. Do that.
			cm.client.writeChan <- irc.Message{
				Prefix:  s.config["server-name"],
				Command: "ERROR",
				Params:  []string{"Invalid username"},
			}
			return
		}
		cm.client.user = user

		// TODO: Apply user mode

		// TODO: Validate realname
		cm.client.realname = realname

		cm.client.registered = true

		cm.client.writeChan <- irc.Message{
			Prefix: s.config["server-name"],
			// 001 RPL_WELCOME
			Command: "001",
			Params: []string{
				fmt.Sprintf("Welcome to the Internet Relay Network %s",
					cm.client.nickUhost()),
			},
		}
		return
	}

	// Unknown command.
	cm.client.writeChan <- irc.Message{
		Prefix: s.config["server-name"],
		// 421 ERR_UNKNOWNCOMMAND
		Command: "421",
		Params:  []string{"Unknown command"},
	}
}

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

func (c Client) write() {
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

func (c Client) String() string {
	return fmt.Sprintf("%s", c.conn.Conn.RemoteAddr())
}

func (c Client) nickUhost() string {
	return fmt.Sprintf("%s!~%s@%s", c.nick, c.user, c.ip)
}
