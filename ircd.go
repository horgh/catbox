package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"summercat.com/irc"
)

// Channel holds everything to do with a channel.
type Channel struct {
	// Canonicalized.
	Name string

	// Client id to Client.
	Members map[uint64]*Client

	Topic string
}

// Server holds the state for a server.
// I put everything global to a server in an instance of struct rather than
// have global variables.
type Server struct {
	Config Config

	// Client id to Client.
	Clients map[uint64]*Client

	// Canonicalized nickname to Client.
	// The reason I have this as well as clients is to track unregistered
	// clients.
	Nicks map[string]*Client

	// Channel name (canonicalized) to Channel.
	Channels map[string]*Channel

	// When we close this channel, this indicates that we're shutting down.
	// Other goroutines can check if this channel is closed.
	ShutdownChan chan struct{}

	// We hear about new client connections on this channel.
	NewClientChan chan *Client

	// When clients die (such as I/O error), the server hears about it on this
	// channel. This is so we can clean up the client.
	// It's useful to be able to know immediately and inform the client if we're
	// going to decide they are getting cut off (e.g., malformed message).
	DeadClientChan chan *Client

	// We hear messages from clients on this channel.
	MessageServerChan chan ClientMessage

	// The Alarm goroutine sends messages on this channel to wake the server up.
	FromAlarmChan chan struct{}

	// TCP listener.
	Listener net.Listener

	// WaitGroup to ensure all goroutines clean up before we end.
	WG sync.WaitGroup
}

// ClientMessage holds a message and the client it originated from.
type ClientMessage struct {
	Client  *Client
	Message irc.Message
}

func main() {
	log.SetFlags(0)

	args, err := getArgs()
	if err != nil {
		log.Fatal(err)
	}

	server, err := newServer(args.ConfigFile)
	if err != nil {
		log.Fatal(err)
	}

	err = server.start()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Server shutdown cleanly.")
}

func newServer(configFile string) (*Server, error) {
	s := Server{
		Clients:  make(map[uint64]*Client),
		Nicks:    make(map[string]*Client),
		Channels: make(map[string]*Channel),

		// shutdown() closes this channel.
		ShutdownChan: make(chan struct{}),

		// acceptConnections() closes this channel.
		NewClientChan: make(chan *Client),

		// We don't ever manually close this channel.
		// This channel can be buffered or not. Order is not important.
		DeadClientChan: make(chan *Client, 100),

		// We don't ever manually close this channel.
		// This channel can be buffered or not. Order is not important.
		MessageServerChan: make(chan ClientMessage, 100),

		// alarm() closes this channel.
		FromAlarmChan: make(chan struct{}),
	}

	err := s.checkAndParseConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("Configuration problem: %s", err)
	}

	return &s, nil
}

// start starts up the server.
//
// We open the TCP port, start goroutines, and then receive messages on our
// channels.
func (s *Server) start() error {
	// TODO: TLS
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%s", s.Config.ListenHost,
		s.Config.ListenPort))
	if err != nil {
		return fmt.Errorf("Unable to listen: %s", err)
	}
	s.Listener = ln

	// acceptConnections accepts connections on the TCP listener.
	s.WG.Add(1)
	go s.acceptConnections()

	// Alarm is a goroutine to wake up this one periodically so we can do things
	// like ping clients.
	s.WG.Add(1)
	go s.alarm()

MessageLoop:
	for {
		select {
		case client := <-s.NewClientChan:
			// The channel will close when we shut down, so make sure we actually
			// received a non-default message.
			if client != nil {
				log.Printf("New client connection: %s", client)
				s.Clients[client.ID] = client
				client.LastActivityTime = time.Now()
			}

		case clientMessage := <-s.MessageServerChan:
			log.Printf("Client %s: Message: %s", clientMessage.Client,
				clientMessage.Message)

			// This could be from a client that disconnected. Ignore it if so.
			_, exists := s.Clients[clientMessage.Client.ID]
			if exists {
				s.handleMessage(clientMessage.Client, clientMessage.Message)
			}

		case client := <-s.DeadClientChan:
			_, exists := s.Clients[client.ID]
			if exists {
				log.Printf("Client %s died.", client)
				client.quit("I/O error")
			}

		case <-s.FromAlarmChan:
			s.checkAndPingClients()

		case <-s.ShutdownChan:
			break MessageLoop
		}
	}

	// We're shutting down. Drain all channels. We want goroutines that send on
	// them to not be blocked.
	for range s.NewClientChan {
	}
	for range s.FromAlarmChan {
	}

	// We don't need to drain messageServerChan or deadClientChan.
	// We can't in fact, since if we close them then client goroutines may panic.
	// The client goroutines won't block sending to them as they will check
	// ShutdownChan.

	s.WG.Wait()

	return nil
}

// shutdown starts server shutdown.
func (s *Server) shutdown() {
	log.Printf("Server shutdown initiated.")

	// Closing ShutdownChan indicates to other goroutines that we're shutting
	// down.
	close(s.ShutdownChan)

	err := s.Listener.Close()
	if err != nil {
		log.Printf("Problem closing TCP listener: %s", err)
	}

	// All clients need to be told. This also closes their write channels.
	for _, client := range s.Clients {
		client.quit("Server shutting down")
	}
}

// acceptConnections accepts TCP connections and tells the main server loop
// through a channel. It sets up separate goroutines for reading/writing to
// and from the client.
func (s *Server) acceptConnections() {
	defer s.WG.Done()

	id := uint64(0)

	for {
		if s.shuttingDown() {
			log.Printf("Connection accepter shutting down.")
			close(s.NewClientChan)
			return
		}

		conn, err := s.Listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %s", err)
			continue
		}

		client := NewClient(s, id, conn)

		// Handle rollover of uint64. Unlikely to happen (outside abuse) but.
		if id+1 == 0 {
			log.Fatalf("Unique ids rolled over!")
		}
		id++

		// NewClientChan is synchronous. We want to make sure server knows about
		// the client before it starts hearing anything from its other channels
		// about the client.
		s.NewClientChan <- client

		s.WG.Add(1)
		go client.readLoop()
		s.WG.Add(1)
		go client.writeLoop()
	}
}

// Return true if the server is shutting down.
func (s *Server) shuttingDown() bool {
	// No messages get sent to this channel, so if we receive a message on it,
	// then we know the channel was closed.
	select {
	case <-s.ShutdownChan:
		return true
	default:
		return false
	}
}

// Alarm sends a message to the server goroutine to wake it up.
// It sleeps and then repeats.
func (s *Server) alarm() {
	defer s.WG.Done()

	for {
		time.Sleep(s.Config.WakeupTime)

		select {
		case s.FromAlarmChan <- struct{}{}:
		case <-s.ShutdownChan:
			log.Printf("Alarm shutting down.")
			close(s.FromAlarmChan)
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
		timeSincePing := now.Sub(client.LastPingTime)

		if client.Registered {
			// Was it active recently enough that we don't need to do anything?
			if timeIdle < s.Config.PingTime {
				continue
			}

			// It's been idle a while.

			// Has it been idle long enough that we consider it dead?
			if timeIdle > s.Config.DeadTime {
				client.quit(fmt.Sprintf("Ping timeout: %d seconds",
					int(timeIdle.Seconds())))
				continue
			}

			// Should we ping it? We might have pinged it recently.
			if timeSincePing < s.Config.PingTime {
				continue
			}

			s.messageClient(client, "PING", []string{s.Config.ServerName})
			client.LastPingTime = now
			continue
		}

		if timeIdle > s.Config.DeadTime {
			client.quit("Idle too long.")
		}
	}
}

// Send an IRC message to a client. Appears to be from the server.
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
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
		if len(c.DisplayNick) > 0 {
			nick = c.DisplayNick
		}
		newParams := []string{nick}

		newParams = append(newParams, params...)
		params = newParams
	}

	c.WriteChan <- irc.Message{
		Prefix:  s.Config.ServerName,
		Command: command,
		Params:  params,
	}
}

// This function informs the server there is a new dead client.
//
// It can be called from any goroutine.
//
// It sends a message on a channel to the server.
//
// We take steps to not be blocked forever if the server's dead client channel
// are closed (such as during shutdown) by select'ing on the shutdown channel.
func (s *Server) newDeadClient(c *Client) {
	select {
	case s.DeadClientChan <- c:
	case <-s.ShutdownChan:
	}
}

// Tell the server about a new message from a client.
//
// You can call this function from any goroutine.
//
// We try to not block send to the channel on shutdown by select'ing on the
// shutdown channel (which never receives messages, and will be closed when we
// shutdown, so will receive the default at that point).
func (s *Server) newClientMessage(c *Client, m irc.Message) {
	select {
	case s.MessageServerChan <- ClientMessage{Client: c, Message: m}:
	case <-s.ShutdownChan:
	}
}
