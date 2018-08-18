package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/pkg/errors"
)

// Conn is a connection to a client/server
type Conn struct {
	conn   net.Conn
	rw     *bufio.ReadWriter
	ioWait time.Duration
	IP     net.IP
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
	if err := c.conn.SetReadDeadline(time.Now().Add(c.ioWait)); err != nil {
		// Do not treat this as fatal. There can be something available to read in
		// the buffer which we want to see.
		log.Printf("Error setting read deadline: %s", err)
	}

	line, err := c.rw.ReadString('\n')
	if err != nil {
		// There may be something read even with error.
		return line, errors.Wrap(err, "error reading")
	}

	return line, nil
}

// Write writes a string to the connection
func (c Conn) Write(s string) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.ioWait)); err != nil {
		return fmt.Errorf("error setting write deadline: %s", err)
	}

	sz, err := c.rw.WriteString(s)
	if err != nil {
		return err
	}

	if sz != len(s) {
		return fmt.Errorf("short write")
	}

	if err := c.rw.Flush(); err != nil {
		return fmt.Errorf("flush error: %s", err)
	}

	return nil
}
