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

	// Tell the server something on this channel.
	ToServerChan chan Event

	// TCP listener.
	Listener net.Listener

	// WaitGroup to ensure all goroutines clean up before we end.
	WG sync.WaitGroup
}

// Event holds a message containing something to tell the server.
type Event struct {
	Type    EventType
	Client  *Client
	Message irc.Message
}

// EventType is a type of event we can tell the server about.
type EventType int

const (
	// NullEvent is a default event. This means the event was not populated.
	NullEvent EventType = iota

	// NewClientEvent means a new client connected.
	NewClientEvent

	// DeadClientEvent means client died for some reason. Clean it up.
	// It's useful to be able to know immediately and inform the client if we're
	// going to decide they are getting cut off (e.g., malformed message).
	DeadClientEvent

	// MessageFromClientEvent means a client sent a message.
	MessageFromClientEvent

	// WakeUpEvent means the server should wake up and do bookkeeping.
	WakeUpEvent
)

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

		// We never manually close this channel.
		ToServerChan: make(chan Event),
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
		case evt := <-s.ToServerChan:
			if evt.Type == NewClientEvent {
				log.Printf("New client connection: %s", evt.Client)
				s.Clients[evt.Client.ID] = evt.Client
				continue
			}

			if evt.Type == DeadClientEvent {
				_, exists := s.Clients[evt.Client.ID]
				if exists {
					log.Printf("Client %s died.", evt.Client)
					evt.Client.quit("I/O error")
				}
				continue
			}

			if evt.Type == MessageFromClientEvent {
				_, exists := s.Clients[evt.Client.ID]
				if exists {
					log.Printf("Client %s: Message: %s", evt.Client, evt.Message)
					s.handleMessage(evt.Client, evt.Message)
				}
				continue
			}

			if evt.Type == WakeUpEvent {
				s.checkAndPingClients()
				continue
			}

			log.Fatalf("Unexpected event: %d", evt.Type)

		case <-s.ShutdownChan:
			break MessageLoop
		}
	}

	// We don't need to drain any channels. None close that will have any
	// goroutines blocked on them.

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
		if s.isShuttingDown() {
			break
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

		// ToServerChan is synchronous. We want to make sure server knows about the
		// client before it starts hearing anything from its other channels about
		// the client.
		s.newEvent(Event{Type: NewClientEvent, Client: client})

		s.WG.Add(1)
		go client.readLoop()
		s.WG.Add(1)
		go client.writeLoop()
	}

	log.Printf("Connection accepter shutting down.")
}

// Return true if the server is shutting down.
func (s *Server) isShuttingDown() bool {
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
		if s.isShuttingDown() {
			break
		}

		time.Sleep(s.Config.WakeupTime)

		s.newEvent(Event{Type: WakeUpEvent})
	}

	log.Printf("Alarm shutting down.")
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

// newEvent tells the server something happens.
//
// Any goroutine can call this function.
//
// It sends the server a message on its ToServerChan.
//
// It will not block on shutdown as we select on the shutdown channel which we
// close when shutting down the server. This means receive on the shutdown
// channel will proceed at that point.
func (s *Server) newEvent(evt Event) {
	select {
	case s.ToServerChan <- evt:
	case <-s.ShutdownChan:
	}
}
