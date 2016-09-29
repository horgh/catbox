package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"summercat.com/irc"
)

// Catbox holds the state for this local server.
// I put everything global to a server in an instance of struct rather than
// have global variables.
type Catbox struct {
	Config Config

	// Next client ID to issue. This turns into TS6 ID which gets concatenated
	// with our SID to make the TS6 UID.
	NextClientID     uint64
	NextClientIDLock sync.Mutex

	// LocalClients are unregistered.
	// Client id to LocalClient.
	LocalClients map[uint64]*LocalClient
	// LocalUsers are clients registered as users.
	LocalUsers map[uint64]*LocalUser
	// LocalServers are clients registered as servers.
	LocalServers map[uint64]*LocalServer

	Opers map[TS6UID]*User

	// Canonicalized nickname to TS6 UID.
	Nicks map[string]TS6UID

	// TS6 UID to User. Local or remote.
	Users map[TS6UID]*User

	// TS6 SID to Server. Local or remote.
	Servers map[TS6SID]*Server

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

// TS6ID is a client's unique identifier. Unique to this server only.
type TS6ID string

// TS6SID uniquely identifies a server. Globally.
type TS6SID string

// TS6UID is SID+UID. Uniquely identify a client. Globally.
type TS6UID string

// Event holds a message containing something to tell the server.
type Event struct {
	Type EventType

	Client *LocalClient

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

	cb, err := newCatbox(args.ConfigFile)
	if err != nil {
		log.Fatal(err)
	}

	err = cb.start()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Server shutdown cleanly.")
}

func newCatbox(configFile string) (*Catbox, error) {
	cb := Catbox{
		LocalClients: make(map[uint64]*LocalClient),
		LocalUsers:   make(map[uint64]*LocalUser),
		LocalServers: make(map[uint64]*LocalServer),
		Opers:        make(map[TS6UID]*User),
		Users:        make(map[TS6UID]*User),
		Nicks:        make(map[string]TS6UID),
		Servers:      make(map[TS6SID]*Server),
		Channels:     make(map[string]*Channel),

		// shutdown() closes this channel.
		ShutdownChan: make(chan struct{}),

		// We never manually close this channel.
		ToServerChan: make(chan Event),
	}

	err := cb.checkAndParseConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("Configuration problem: %s", err)
	}

	return &cb, nil
}

// start starts up the server.
//
// We open the TCP port, start goroutines, and then receive messages on our
// channels.
func (cb *Catbox) start() error {
	// TODO: TLS
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%s", cb.Config.ListenHost,
		cb.Config.ListenPort))
	if err != nil {
		return fmt.Errorf("Unable to listen: %s", err)
	}
	cb.Listener = ln

	// acceptConnections accepts connections on the TCP listener.
	cb.WG.Add(1)
	go cb.acceptConnections()

	// Alarm is a goroutine to wake up this one periodically so we can do things
	// like ping clients.
	cb.WG.Add(1)
	go cb.alarm()

	cb.eventLoop()

	// We don't need to drain any channels. None close that will have any
	// goroutines blocked on them.

	cb.WG.Wait()

	return nil
}

// eventLoop processes events on the server's channel.
//
// It continues until the shutdown channel closes, indicating shutdown.
func (cb *Catbox) eventLoop() {
	for {
		select {
		// Careful about using the Client we get back in events. It may have been
		// promoted to a different client type (UserClient, ServerClient).
		case evt := <-cb.ToServerChan:
			if evt.Type == NewClientEvent {
				log.Printf("New client connection: %s", evt.Client)
				cb.LocalClients[evt.Client.ID] = evt.Client
				continue
			}

			if evt.Type == DeadClientEvent {
				lc, exists := cb.LocalClients[evt.Client.ID]
				if exists {
					lc.quit("I/O error")
					continue
				}
				lu, exists := cb.LocalUsers[evt.Client.ID]
				if exists {
					lu.quit("I/O error")
					continue
				}
				ls, exists := cb.LocalServers[evt.Client.ID]
				if exists {
					ls.quit("I/O error")
					continue
				}
				continue
			}

			if evt.Type == MessageFromClientEvent {
				lc, exists := cb.LocalClients[evt.Client.ID]
				if exists {
					lc.handleMessage(evt.Message)
					continue
				}
				lu, exists := cb.LocalUsers[evt.Client.ID]
				if exists {
					lu.handleMessage(evt.Message)
					continue
				}
				ls, exists := cb.LocalServers[evt.Client.ID]
				if exists {
					ls.handleMessage(evt.Message)
					continue
				}
				continue
			}

			if evt.Type == WakeUpEvent {
				cb.checkAndPingClients()
				continue
			}

			log.Fatalf("Unexpected event: %d", evt.Type)

		case <-cb.ShutdownChan:
			return
		}
	}
}

// shutdown starts server shutdown.
func (cb *Catbox) shutdown() {
	log.Printf("Server shutdown initiated.")

	// Closing ShutdownChan indicates to other goroutines that we're shutting
	// down.
	close(cb.ShutdownChan)

	err := cb.Listener.Close()
	if err != nil {
		log.Printf("Problem closing TCP listener: %s", err)
	}

	// All clients need to be told. This also closes their write channels.
	for _, client := range cb.LocalClients {
		client.quit("Server shutting down")
	}
	for _, client := range cb.LocalUsers {
		client.quit("Server shutting down")
	}
	for _, client := range cb.LocalServers {
		client.quit("Server shutting down")
	}
}

// getClientID generates a new client ID. Each client that connects to us (or
// we connect to in the case of initiating a connection to a server) we assign
// a unique id using this function.
//
// We take a lock to allow it to be called safely from any goroutine.
func (cb *Catbox) getClientID() uint64 {
	cb.NextClientIDLock.Lock()

	id := cb.NextClientID

	if cb.NextClientID+1 == 0 {
		log.Fatalf("Client id overflow")
	}
	cb.NextClientID++

	cb.NextClientIDLock.Unlock()

	return id
}

// acceptConnections accepts TCP connections and tells the main server loop
// through a channel. It sets up separate goroutines for reading/writing to
// and from the client.
func (cb *Catbox) acceptConnections() {
	defer cb.WG.Done()

	for {
		if cb.isShuttingDown() {
			break
		}

		conn, err := cb.Listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %s", err)
			continue
		}

		id := cb.getClientID()

		client := NewLocalClient(cb, id, conn)

		cb.newEvent(Event{Type: NewClientEvent, Client: client})

		cb.WG.Add(1)
		go client.readLoop()
		cb.WG.Add(1)
		go client.writeLoop()
	}

	log.Printf("Connection accepter shutting down.")
}

// Return true if the server is shutting down.
func (cb *Catbox) isShuttingDown() bool {
	// No messages get sent to this channel, so if we receive a message on it,
	// then we know the channel was closed.
	select {
	case <-cb.ShutdownChan:
		return true
	default:
		return false
	}
}

// Alarm sends a message to the server goroutine to wake it up.
// It sleeps and then repeats.
func (cb *Catbox) alarm() {
	defer cb.WG.Done()

	for {
		if cb.isShuttingDown() {
			break
		}

		time.Sleep(cb.Config.WakeupTime)

		cb.newEvent(Event{Type: WakeUpEvent})
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
func (cb *Catbox) checkAndPingClients() {
	now := time.Now()

	// Unregistered clients do not receive PINGs, nor do we care about their
	// idle time. Kill them if they are connected too long and still unregistered.
	for _, client := range cb.LocalClients {
		if client.SendQueueExceeded {
			client.quit("SendQ exceeded")
			continue
		}

		timeConnected := now.Sub(client.ConnectionStartTime)

		// If it's been connected long enough to need to ping it, cut it off.
		if timeConnected > cb.Config.PingTime {
			client.quit("Idle too long.")
		}
	}

	// I want to iterate and use same logic on both user and server clients. Do it
	// by creating an interface that they both satisfy.
	type UserServer interface {
		isSendQueueExceeded() bool
		getLastActivityTime() time.Time
		getLastPingTime() time.Time
		quit(s string)
		messageFromServer(s string, p []string)
		setLastPingTime(t time.Time)
	}

	clients := []UserServer{}
	for _, v := range cb.LocalUsers {
		clients = append(clients, v)
	}
	for _, v := range cb.LocalServers {
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
		if timeIdle < cb.Config.PingTime {
			continue
		}

		// It's been idle a while.

		// Has it been idle long enough that we consider it dead?
		if timeIdle > cb.Config.DeadTime {
			client.quit(fmt.Sprintf("Ping timeout: %d seconds",
				int(timeIdle.Seconds())))
			continue
		}

		// Should we ping it? We might have pinged it recently.
		if timeSincePing < cb.Config.PingTime {
			continue
		}

		client.messageFromServer("PING", []string{cb.Config.ServerName})
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
func (cb *Catbox) newEvent(evt Event) {
	select {
	case cb.ToServerChan <- evt:
	case <-cb.ShutdownChan:
	}
}

// Send a message to all local operator users.
func (cb *Catbox) noticeOpers(msg string) {
	log.Print(msg)

	for _, c := range cb.Opers {
		if c.LocalUser != nil {
			c.LocalUser.notice(msg)
		}

		// TODO: Remote opers?
	}
}
