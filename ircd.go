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

	// Client id to UserClient.
	Members map[uint64]*UserClient

	Topic string
}

// Server holds the state for a server.
// I put everything global to a server in an instance of struct rather than
// have global variables.
type Server struct {
	Config Config

	// Next client id to issue.
	NextClientID     uint64
	NextClientIDLock sync.Mutex

	// Client id to Client.
	UnregisteredClients map[uint64]*Client

	// Client id to UserClient.
	UserClients map[uint64]*UserClient

	// Client id to ServerClient.
	ServerClients map[uint64]*ServerClient

	// Canonicalized nickname to client id.
	// Client may be registered or not.
	Nicks map[string]uint64

	// Client id to user client. Opers.
	Opers map[uint64]*UserClient

	// Server names to client id.
	Servers map[string]uint64

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
	Type EventType

	Client *Client

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
		UnregisteredClients: make(map[uint64]*Client),
		UserClients:         make(map[uint64]*UserClient),
		ServerClients:       make(map[uint64]*ServerClient),
		Nicks:               make(map[string]uint64),
		Opers:               make(map[uint64]*UserClient),
		Servers:             make(map[string]uint64),
		Channels:            make(map[string]*Channel),

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

	s.eventLoop()

	// We don't need to drain any channels. None close that will have any
	// goroutines blocked on them.

	s.WG.Wait()

	return nil
}

// eventLoop processes events on the server's channel.
//
// It continues until the shutdown channel closes, indicating shutdown.
func (s *Server) eventLoop() {
	for {
		select {
		// Careful about using the Client we get back in events. It may have been
		// promoted to a different client type (UserClient, ServerClient).
		case evt := <-s.ToServerChan:
			if evt.Type == NewClientEvent {
				log.Printf("New client connection: %s", evt.Client)
				s.UnregisteredClients[evt.Client.ID] = evt.Client
				continue
			}

			if evt.Type == DeadClientEvent {
				client, exists := s.UnregisteredClients[evt.Client.ID]
				if exists {
					client.quit("I/O error")
					continue
				}

				userClient, exists := s.UserClients[evt.Client.ID]
				if exists {
					userClient.quit("I/O error")
					continue
				}

				serverClient, exists := s.ServerClients[evt.Client.ID]
				if exists {
					serverClient.quit("I/O error")
					continue
				}
				continue
			}

			if evt.Type == MessageFromClientEvent {
				client, exists := s.UnregisteredClients[evt.Client.ID]
				if exists {
					client.handleMessage(evt.Message)
					continue
				}

				userClient, exists := s.UserClients[evt.Client.ID]
				if exists {
					userClient.handleMessage(evt.Message)
					continue
				}

				serverClient, exists := s.ServerClients[evt.Client.ID]
				if exists {
					serverClient.handleMessage(evt.Message)
					continue
				}
				continue
			}

			if evt.Type == WakeUpEvent {
				s.checkAndPingClients()
				continue
			}

			log.Fatalf("Unexpected event: %d", evt.Type)

		case <-s.ShutdownChan:
			return
		}
	}
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
	for _, client := range s.UnregisteredClients {
		client.quit("Server shutting down")
	}
	for _, client := range s.UserClients {
		client.quit("Server shutting down")
	}
	for _, client := range s.ServerClients {
		client.quit("Server shutting down")
	}
}

func (s *Server) getClientID() uint64 {
	s.NextClientIDLock.Lock()

	id := s.NextClientID

	if s.NextClientID+1 == 0 {
		log.Fatalf("Client id overflow")
	}
	s.NextClientID++

	s.NextClientIDLock.Unlock()
	return id
}

// acceptConnections accepts TCP connections and tells the main server loop
// through a channel. It sets up separate goroutines for reading/writing to
// and from the client.
func (s *Server) acceptConnections() {
	defer s.WG.Done()

	for {
		if s.isShuttingDown() {
			break
		}

		conn, err := s.Listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %s", err)
			continue
		}

		id := s.getClientID()
		client := NewClient(s, id, conn)

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
//
// We also kill any whose send queue maxed out.
func (s *Server) checkAndPingClients() {
	now := time.Now()

	// Unregistered clients do not receive PINGs, nor do we care about their
	// idle time. Kill them if they are connected too long and still unregistered.
	for _, client := range s.UnregisteredClients {
		if client.SendQueueExceeded {
			client.quit("SendQ exceeded")
			continue
		}

		timeConnected := now.Sub(client.ConnectionStartTime)

		// If it's been connected long enough to need to ping it, cut it off.
		if timeConnected > s.Config.PingTime {
			client.quit("Idle too long.")
		}
	}

	// I want to iterate and use same logic on both user and server clients. Do it
	// by creating an interface that they both satisfy.
	type UserServerClient interface {
		isSendQueueExceeded() bool
		getLastActivityTime() time.Time
		getLastPingTime() time.Time
		quit(s string)
		messageFromServer(s string, p []string)
		setLastPingTime(t time.Time)
	}

	clients := []UserServerClient{}
	for _, v := range s.UserClients {
		clients = append(clients, v)
	}
	for _, v := range s.ServerClients {
		clients = append(clients, v)
	}

	// User and server clients we are more lenient with. Ping them if they are
	// idle for a while.
	for _, client := range clients {
		if client.isSendQueueExceeded() {
			client.quit("SendQ exceeded")
			continue
		}

		timeIdle := now.Sub(client.getLastActivityTime())
		timeSincePing := now.Sub(client.getLastPingTime())

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

		client.messageFromServer("PING", []string{s.Config.ServerName})
		client.setLastPingTime(now)
		continue
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
//
// We only need to use this function in goroutines other the main server
// goroutine.
func (s *Server) newEvent(evt Event) {
	select {
	case s.ToServerChan <- evt:
	case <-s.ShutdownChan:
	}
}

// Send a message to all local operator users.
func (s *Server) noticeOpers(msg string) {
	log.Print(msg)

	for _, c := range s.Opers {
		c.notice(msg)
	}
}
