package internal

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/horgh/irc"
)

// Client represents a client connection.
type Client struct {
	nick       string
	serverHost string
	serverPort uint16

	writeTimeout time.Duration
	readTimeout  time.Duration

	conn net.Conn
	rw   *bufio.ReadWriter

	recvChan chan irc.Message
	sendChan chan irc.Message
	errChan  chan error
	doneChan chan struct{}
	wg       *sync.WaitGroup

	channels map[string]struct{}
	mutex    *sync.Mutex
}

// NewClient creates a Client.
func NewClient(nick, serverHost string, serverPort uint16) *Client {
	return &Client{
		nick:       nick,
		serverHost: serverHost,
		serverPort: serverPort,

		writeTimeout: 30 * time.Second,
		readTimeout:  100 * time.Millisecond,

		channels: map[string]struct{}{},
		mutex:    &sync.Mutex{},
	}
}

// Start starts a client's connection and registers.
//
// The client responds to PING commands.
//
// All messages received from the server will be sent on the receive channel.
//
// Messages you send to the send channel will be sent to the server.
//
// If an error occurs, we send a message on the error channel. If you receive a
// message on that channel, you must stop the client.
//
// The caller must call Stop() to clean up the client.
func (c *Client) Start() (
	<-chan irc.Message,
	chan<- irc.Message,
	<-chan error,
	error,
) {
	if err := c.connect(); err != nil {
		return nil, nil, nil, fmt.Errorf("error connecting: %s", err)
	}

	if err := c.writeMessage(irc.Message{
		Command: "NICK",
		Params:  []string{c.nick},
	}); err != nil {
		_ = c.conn.Close()
		return nil, nil, nil, err
	}

	if err := c.writeMessage(irc.Message{
		Command: "USER",
		Params:  []string{c.nick, "0", "*", c.nick},
	}); err != nil {
		_ = c.conn.Close()
		return nil, nil, nil, err
	}

	c.recvChan = make(chan irc.Message, 512)
	c.sendChan = make(chan irc.Message, 512)
	c.errChan = make(chan error, 512)
	c.doneChan = make(chan struct{})

	c.wg = &sync.WaitGroup{}

	c.wg.Add(1)
	go c.reader(c.recvChan)

	c.wg.Add(1)
	go c.writer(c.sendChan)

	return c.recvChan, c.sendChan, c.errChan, nil
}

// connect opens a new connection to the server.
func (c *Client) connect() error {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	conn, err := dialer.Dial("tcp", fmt.Sprintf("%s:%d", c.serverHost,
		c.serverPort))
	if err != nil {
		return fmt.Errorf("error dialing: %s", err)
	}

	c.conn = conn
	c.rw = bufio.NewReadWriter(bufio.NewReader(c.conn), bufio.NewWriter(c.conn))
	return nil
}

func (c Client) reader(recvChan chan<- irc.Message) {
	defer c.wg.Done()

	for {
		select {
		case <-c.doneChan:
			close(recvChan)
			return
		default:
		}

		m, err := c.readMessage()
		if err != nil {
			// If we time out waiting for a read to succeed, just ignore it and try
			// again. We want a short timeout on that so we frequently check whether
			// we should end.
			//
			// There's no accessible error variable to compare with
			if strings.Contains(err.Error(), "i/o timeout") {
				continue
			}

			c.errChan <- fmt.Errorf("error reading message: %s", err)
			close(recvChan)
			return
		}

		if m.Command == "PING" {
			if err := c.writeMessage(irc.Message{
				Command: "PONG",
				Params:  []string{m.Params[0]},
			}); err != nil {
				c.errChan <- fmt.Errorf("error sending pong: %s", err)
				close(recvChan)
				return
			}
		}

		if m.Command == "JOIN" {
			if m.SourceNick() == c.nick {
				c.mutex.Lock()
				c.channels[m.Params[0]] = struct{}{}
				c.mutex.Unlock()
			}
		}

		recvChan <- m
	}
}

func (c Client) writer(sendChan <-chan irc.Message) {
	defer c.wg.Done()

LOOP:
	for {
		select {
		case <-c.doneChan:
			break LOOP
		case m, ok := <-sendChan:
			if !ok {
				break
			}
			if err := c.writeMessage(m); err != nil {
				c.errChan <- fmt.Errorf("error writing message: %s", err)
				break
			}
		}
	}

	for range sendChan {
	}
}

// writeMessage writes an IRC message to the connection.
func (c Client) writeMessage(m irc.Message) error {
	buf, err := m.Encode()
	if err != nil && err != irc.ErrTruncated {
		return fmt.Errorf("unable to encode message: %s", err)
	}

	if err := c.conn.SetWriteDeadline(time.Now().Add(
		c.writeTimeout)); err != nil {
		return fmt.Errorf("unable to set deadline: %s", err)
	}

	sz, err := c.rw.WriteString(buf)
	if err != nil {
		return err
	}

	if sz != len(buf) {
		return fmt.Errorf("short write")
	}

	if err := c.rw.Flush(); err != nil {
		return fmt.Errorf("flush error: %s", err)
	}

	log.Printf("client %s: sent: %s", c.nick, strings.TrimRight(buf, "\r\n"))
	return nil
}

// readMessage reads a line from the connection and parses it as an IRC message.
func (c Client) readMessage() (irc.Message, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
		return irc.Message{}, fmt.Errorf("unable to set deadline: %s", err)
	}

	line, err := c.rw.ReadString('\n')
	if err != nil {
		return irc.Message{}, err
	}

	log.Printf("client %s: read: %s", c.nick, strings.TrimRight(line, "\r\n"))

	m, err := irc.ParseMessage(line)
	if err != nil && err != irc.ErrTruncated {
		return irc.Message{}, fmt.Errorf("unable to parse message: %s: %s", line,
			err)
	}

	return m, nil
}

// Stop shuts down the client and cleans up.
//
// You must not send any messages on the send channel after calling this
// function.
func (c *Client) Stop() {
	// Tell reader and writer to end.
	close(c.doneChan)

	// We won't be sending anything further to writer. Let it clean up.
	close(c.sendChan)

	// Wait for reader and writer to end.
	c.wg.Wait()

	// We know the reader and writer won't be sending on the error channel any
	// more.
	close(c.errChan)

	_ = c.conn.Close()

	for range c.recvChan {
	}
	for range c.errChan {
	}
}

// GetNick retrieves the client's nick.
func (c Client) GetNick() string { return c.nick }

// GetReceiveChannel retrieves the receive channel.
func (c Client) GetReceiveChannel() <-chan irc.Message { return c.recvChan }

// GetSendChannel retrieves the send channel.
func (c Client) GetSendChannel() chan<- irc.Message { return c.sendChan }

// GetErrorChannel retrieves the error channel.
func (c Client) GetErrorChannel() <-chan error { return c.errChan }

// GetChannels retrieves the IRC channels the client is on.
func (c Client) GetChannels() []string {
	var channels []string
	c.mutex.Lock()
	for k := range c.channels {
		channels = append(channels, k)
	}
	c.mutex.Unlock()
	return channels
}
