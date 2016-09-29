package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"summercat.com/irc"
)

// Conn is a connection to a client/server
type Conn struct {
	// conn: The connection if we are actively connected.
	conn net.Conn

	// rw: Read/write handle to the connection
	rw *bufio.ReadWriter

	ioWait time.Duration

	IP net.IP
}

// NewConn initializes a Conn struct
func NewConn(conn net.Conn, ioWait time.Duration) Conn {
	tcpAddr, err := net.ResolveTCPAddr("tcp", conn.RemoteAddr().String())
	// This shouldn't happen.
	if err != nil {
		log.Fatalf("Unable to resolve TCP address: %s", err)
	}

	return Conn{
		conn:   conn,
		rw:     bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn)),
		ioWait: ioWait,
		IP:     tcpAddr.IP,
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
	// Deadline so we will eventually give up.
	err := c.conn.SetDeadline(time.Now().Add(c.ioWait))
	if err != nil {
		return "", fmt.Errorf("Unable to set deadline: %s", err)
	}

	line, err := c.rw.ReadString('\n')
	if err != nil {
		return "", err
	}

	log.Printf("Read: %s", strings.TrimRight(line, "\r\n"))

	return line, nil
}

// Write writes a string to the connection
func (c Conn) Write(s string) error {
	// Deadline so we will eventually give up.
	err := c.conn.SetDeadline(time.Now().Add(c.ioWait))
	if err != nil {
		return fmt.Errorf("Unable to set deadline: %s", err)
	}

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
