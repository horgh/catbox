package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"

	"summercat.com/irc"
)

// Conn is a connection to a client/server
type Conn struct {
	// conn: The connection if we are actively connected.
	conn net.Conn

	// rw: Read/write handle to the connection
	rw *bufio.ReadWriter
}

// NewConn initializes a Conn struct
func NewConn(conn net.Conn) Conn {
	return Conn{
		conn: conn,
		rw:   bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn)),
	}
}

// Close closes the underlying connection
func (c Conn) Close() error {
	return c.conn.Close()
}

// RemoteAddr returns the remote network address.
func (c Conn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// Read reads a line from the connection.
func (c Conn) Read() (string, error) {
	line, err := c.rw.ReadString('\n')
	if err != nil {
		return "", err
	}

	log.Printf("Read: %s", strings.TrimRight(line, "\r\n"))

	return line, nil
}

// ReadMessage reads a line from the connection and parses it as an IRC message.
func (c Conn) ReadMessage() (irc.Message, error) {
	buf, err := c.Read()
	if err != nil {
		return irc.Message{}, err
	}

	m, err := irc.ParseMessage(buf)
	if err != nil {
		return irc.Message{}, fmt.Errorf("Unable to parse message: %s: %s", buf,
			err)
	}

	return m, nil
}

// Write writes a string to the connection
func (c Conn) Write(s string) error {
	sz, err := c.rw.WriteString(s)
	if err != nil {
		return err
	}

	if sz != len(s) {
		return fmt.Errorf("Short write")
	}

	err = c.rw.Flush()
	if err != nil {
		return fmt.Errorf("Flush error: %s", err)
	}

	log.Printf("Sent: %s", strings.TrimRight(s, "\r\n"))

	return nil
}

// WriteMessage writes an IRC message to the connection.
func (c Conn) WriteMessage(m irc.Message) error {
	buf, err := m.Encode()
	if err != nil {
		return fmt.Errorf("Unable to encode message: %s", err)
	}

	return c.Write(buf)
}
