package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"summercat.com/irc"
)

// Client holds state about a single client connection.
// All clients are in this state until they register as either a user client
// or as a server.
type Client struct {
	// Conn holds the TCP connection to the client.
	Conn Conn

	// WriteChan is the channel to send to to write to the client.
	WriteChan chan irc.Message

	// A unique id. Internal to this server only.
	ID uint64

	ConnectionStartTime time.Time

	// Server references the main server the client is connected to (local
	// client).
	// It's helpful to have to avoid passing server all over the place.
	Server *Server

	// Track if we overflow our send queue. If we do, we'll kill the client.
	SendQueueExceeded bool

	// Info client may send us before we complete its registration and promote it
	// to UserClient or ServerClient.

	// User info

	// NICK
	PreRegDisplayNick string

	// USER
	PreRegUser     string
	PreRegRealName string

	// Server info

	// PASS
	PreRegPass   string
	PreRegTS6SID string

	// CAPAB
	PreRegCapabs map[string]struct{}

	// SERVER
	PreRegServerName string
	PreRegServerDesc string

	// Boolean flags involved in the several step server link process.
	// Use them to keep track of where we are in the process.

	GotPASS   bool
	GotCAPAB  bool
	GotSERVER bool

	SentPASS   bool
	SentCAPAB  bool
	SentSERVER bool
	SentSVINFO bool
}

// NewClient creates a Client
func NewClient(s *Server, id uint64, conn net.Conn) *Client {
	return &Client{
		Conn: NewConn(conn, s.Config.DeadTime),

		// Buffered channel. We don't want to block sending to the client from the
		// server. The client may be stuck. Make the buffer large enough that it
		// should only max out in case of connection issues.
		WriteChan: make(chan irc.Message, 32768),

		ID:                  id,
		ConnectionStartTime: time.Now(),
		Server:              s,

		PreRegCapabs: make(map[string]struct{}),
	}
}

func (c *Client) String() string {
	return fmt.Sprintf("%d %s", c.ID, c.Conn.RemoteAddr())
}

// readLoop endlessly reads from the client's TCP connection. It parses each
// IRC protocol message and passes it to the server through the server's
// channel.
func (c *Client) readLoop() {
	defer c.Server.WG.Done()

	for {
		if c.Server.isShuttingDown() {
			break
		}

		// This means if a client sends us an invalid message that we cut them off.
		message, err := c.Conn.ReadMessage()
		if err != nil {
			log.Printf("Client %s: %s", c, err)
			c.Server.newEvent(Event{Type: DeadClientEvent, Client: c})
			break
		}

		c.Server.newEvent(Event{
			Type:    MessageFromClientEvent,
			Client:  c,
			Message: message,
		})
	}

	log.Printf("Client %s: Reader shutting down.", c)
}

// writeLoop endlessly reads from the client's channel, encodes each message,
// and writes it to the client's TCP connection.
//
// When the channel is closed, or if we have a write error, close the TCP
// connection. I have this here so that we try to deliver messages to the
// client before closing its socket and giving up.
func (c *Client) writeLoop() {
	defer c.Server.WG.Done()

	// Receive on the client's write channel.
	//
	// Ensure we also stop if the server is shutting down (indicated by the
	// ShutdownChan being closed). If we don't, then there is potential for us to
	// leak this goroutine. Consider the case where we have a new client, and
	// tell the server about it, but the server is shutting down, and so does not
	// see the new client event. In this case the server does not know that it
	// must close the write channel so that the client will end (if we were for
	// example using 'for message := range c.WriteChan', as it would block
	// forever).
	//
	// A problem with this is we are not guaranteed to process any remaining
	// messages on the write channel (and so inform the client about shutdown)
	// when we are shutting down. But it is an improvement on leaking the
	// goroutine.
Loop:
	for {
		select {
		case message, ok := <-c.WriteChan:
			if !ok {
				break Loop
			}

			err := c.Conn.WriteMessage(message)
			if err != nil {
				log.Printf("Client %s: %s", c, err)
				c.Server.newEvent(Event{Type: DeadClientEvent, Client: c})
				break Loop
			}
		case <-c.Server.ShutdownChan:
			break Loop
		}
	}

	err := c.Conn.Close()
	if err != nil {
		log.Printf("Client %s: Problem closing connection: %s", c, err)
	}

	log.Printf("Client %s: Writer shutting down.", c)
}

// quit means the client is quitting. Tell it why and clean up.
func (c *Client) quit(msg string) {
	c.messageFromServer("ERROR", []string{msg})
	close(c.WriteChan)

	if len(c.PreRegDisplayNick) > 0 {
		delete(c.Server.Nicks, canonicalizeNick(c.PreRegDisplayNick))
	}

	delete(c.Server.UnregisteredClients, c.ID)
}

func (c *Client) handleMessage(m irc.Message) {
	// Clients SHOULD NOT (section 2.3) send a prefix.
	if m.Prefix != "" {
		c.quit("No prefix permitted")
		return
	}

	// Non-RFC command that appears to be widely supported. Just ignore it for
	// now.
	if m.Command == "CAP" {
		return
	}

	// We may receive NOTICE when initiating connection to a server. Ignore it.
	if m.Command == "NOTICE" {
		return
	}

	// To register as a user client:
	// NICK
	// USER

	if m.Command == "NICK" {
		c.nickCommand(m)
		return
	}

	if m.Command == "USER" {
		c.userCommand(m)
		return
	}

	// To register as a server (using TS6):

	// If incoming client is initiator, they send this:

	// > PASS
	// > CAPAB
	// > SERVER

	// We check this info. If valid, reply:

	// < PASS
	// < CAPAB
	// < SERVER

	// They check our info. If valid, reply:

	// > SVINFO

	// We reply again:

	// < SVINFO
	// < Burst
	// < PING

	// They finish:

	// > Burst
	// > PING

	// Everyone ACKs the PINGs:

	// < PONG

	// > PONG

	// PINGs are used to know end of burst. Then we're linked.

	// If we initiate the link, then we send PASS/CAPAB/SERVER and expect it
	// in return. Beyond that, the process is the same.

	if m.Command == "PASS" {
		c.passCommand(m)
		return
	}

	if m.Command == "CAPAB" {
		c.capabCommand(m)
		return
	}

	if m.Command == "SERVER" {
		c.serverCommand(m)
		return
	}

	if m.Command == "SVINFO" {
		c.svinfoCommand(m)
		return
	}

	if m.Command == "ERROR" {
		c.errorCommand(m)
		return
	}

	// Let's say *all* other commands require you to be registered.
	// 451 ERR_NOTREGISTERED
	c.messageFromServer("451", []string{fmt.Sprintf("You have not registered.")})
}

func (c *Client) completeRegistration() {
	// RFC 2813 specifies messages to send upon registration.

	uc := NewUserClient(c)

	delete(c.Server.UnregisteredClients, uc.ID)
	c.Server.UserClients[c.ID] = uc

	// 001 RPL_WELCOME
	uc.messageFromServer("001", []string{
		fmt.Sprintf("Welcome to the Internet Relay Network %s",
			uc.nickUhost()),
	})

	// 002 RPL_YOURHOST
	uc.messageFromServer("002", []string{
		fmt.Sprintf("Your host is %s, running version %s",
			uc.Server.Config.ServerName,
			uc.Server.Config.Version),
	})

	// 003 RPL_CREATED
	uc.messageFromServer("003", []string{
		fmt.Sprintf("This server was created %s", uc.Server.Config.CreatedDate),
	})

	// 004 RPL_MYINFO
	// <servername> <version> <available user modes> <available channel modes>
	uc.messageFromServer("004", []string{
		// It seems ambiguous if these are to be separate parameters.
		uc.Server.Config.ServerName,
		uc.Server.Config.Version,
		"o",
		"n",
	})

	uc.lusersCommand()
	uc.motdCommand()
}

// Send an IRC message to a client. Appears to be from the server.
// This works by writing to a client's channel.
//
// Note: Only the server goroutine should call this (due to channel use).
func (c *Client) messageFromServer(command string, params []string) {
	// For numeric messages, we need to prepend the nick.
	// Use * for the nick in cases where the client doesn't have one yet.
	// This is what ircd-ratbox does. Maybe not RFC...
	if isNumericCommand(command) {
		nick := "*"
		if len(c.PreRegDisplayNick) > 0 {
			nick = c.PreRegDisplayNick
		}
		newParams := []string{nick}
		newParams = append(newParams, params...)
		params = newParams
	}

	c.maybeQueueMessage(irc.Message{
		Prefix:  c.Server.Config.ServerName,
		Command: command,
		Params:  params,
	})
}

// Send a message to the client. We send it to its write channel, which in turn
// leads to writing it to its TCP socket.
//
// This function won't block. If the client's queue is full, we flag it as
// having a full send queue.
//
// Not blocking is important because the server sends the client messages this
// way, and if we block on a problem client, everything would grind to a halt.
func (c *Client) maybeQueueMessage(m irc.Message) {
	if c.SendQueueExceeded {
		return
	}

	select {
	case c.WriteChan <- m:
	default:
		c.SendQueueExceeded = true
	}
}

func (c *Client) sendPASS(pass string) {
	// PASS <password>, TS, <ts version>, <SID>
	c.maybeQueueMessage(irc.Message{
		Command: "PASS",
		Params:  []string{pass, "TS", "6", c.Server.Config.TS6SID},
	})

	c.SentPASS = true
}

func (c *Client) sendCAPAB() {
	// CAPAB <space separated list>
	c.maybeQueueMessage(irc.Message{
		Command: "CAPAB",
		Params:  []string{"QS ENCAP"},
	})

	c.SentCAPAB = true
}

func (c *Client) sendSERVER() {
	// SERVER <name> <hopcount> <description>
	c.maybeQueueMessage(irc.Message{
		Command: "SERVER",
		Params: []string{
			c.Server.Config.ServerName,
			"1",
			c.Server.Config.ServerInfo,
		},
	})

	c.SentSERVER = true
}

func (c *Client) sendSVINFO() {
	// SVINFO <TS version> <min TS version> 0 <current time>
	epoch := time.Now().Unix()
	c.maybeQueueMessage(irc.Message{
		Command: "SVINFO",
		Params: []string{
			"6", "6", "0", fmt.Sprintf("%d", epoch),
		},
	})

	c.SentSVINFO = true
}

func (c *Client) registerServer() {
	// Possible it took a NICK... Doesn't make sense for it to do so, but since
	// it's been unregistered until now, a malicious server could have taken a
	// nick.
	if len(c.PreRegDisplayNick) > 0 {
		delete(c.Server.Nicks, canonicalizeNick(c.PreRegDisplayNick))
	}

	s := NewServerClient(c)

	delete(c.Server.UnregisteredClients, s.ID)
	c.Server.ServerClients[s.ID] = s
	c.Server.Servers[s.Name] = s.ID

	s.Server.noticeOpers(fmt.Sprintf("Established link to %s.", s.Name))

	s.sendBurst()
	s.sendPING()
}

func (c *Client) isSendQueueExceeded() bool {
	return c.SendQueueExceeded
}
