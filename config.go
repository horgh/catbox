package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/horgh/config"
)

// Config holds a server's configuration.
type Config struct {
	ListenHost      string
	ListenPort      string
	ListenPortTLS   string
	CertificateFile string
	KeyFile         string
	ServerName      string

	// Description of server. This shows in WHOIS, etc.
	ServerInfo string

	MOTD string

	MaxNickLength int

	// Period of time a client can be idle before we send it a PING.
	PingTime time.Duration

	// Period of time a client can be idle before we consider it dead.
	DeadTime time.Duration

	// Time to wait between attempts connecting to servers (minimum).
	ConnectAttemptTime time.Duration

	// TS6 SID. Must be unique in the network. Format: [0-9][A-Z0-9]{2}
	TS6SID TS6SID

	AdminEmail string

	// Oper name to password.
	Opers map[string]string

	// Server name to its link information.
	Servers map[string]*ServerDefinition

	// User configuration info.
	UserConfigs []UserConfig
}

// ServerDefinition defines how to link to a server.
type ServerDefinition struct {
	Name     string
	Hostname string
	Port     int
	Pass     string
	TLS      bool
}

// UserConfig defines settings about users. Matched by usermask and hostmask.
type UserConfig struct {
	// For this configuration to apply at registration time, the user must match
	// the UserMask and HostMask.
	UserMask string
	HostMask string

	// Whether to grant the usermask/hostmask flood exemption.
	FloodExempt bool

	// If non-blank, a spoof to set instead of their host.
	Spoof string
}

// checkAndParseConfig checks configuration keys are present and in an
// acceptable format.
//
// We parse some values into alternate representations.
//
// This function populates both the server.Config and server.Opers fields.
func checkAndParseConfig(file string) (*Config, error) {
	m, err := config.ReadStringMap(file)
	if err != nil {
		return nil, err
	}

	c := &Config{}

	c.ListenHost = "0.0.0.0"
	if m["listen-host"] != "" {
		c.ListenHost = m["listen-host"]
	}

	c.ListenPort = "6667"
	if m["listen-port"] != "" {
		c.ListenPort = m["listen-port"]
	}

	c.ListenPortTLS = "-1"
	if m["listen-port-tls"] != "" {
		c.ListenPortTLS = m["listen-port-tls"]
	}

	if m["certificate-file"] != "" {
		c.CertificateFile = m["certificate-file"]
	}

	if m["key-file"] != "" {
		c.KeyFile = m["key-file"]
	}

	c.ServerName = "irc.example.com"
	if m["server-name"] != "" {
		c.ServerName = m["server-name"]
	}

	c.ServerInfo = "IRC"
	if m["server-info"] != "" {
		c.ServerInfo = m["server-info"]
	}

	c.MOTD = "Hello this is catbox"
	if m["motd"] != "" {
		c.MOTD = m["motd"]
	}

	c.MaxNickLength = 9
	if m["max-nick-length"] != "" {
		nickLen64, err := strconv.ParseInt(m["max-nick-length"], 10, 8)
		if err != nil {
			return nil, fmt.Errorf("max nick length is not valid: %s", err)
		}
		c.MaxNickLength = int(nickLen64)
	}

	c.PingTime = 30 * time.Second
	if m["ping-time"] != "" {
		c.PingTime, err = time.ParseDuration(m["ping-time"])
		if err != nil {
			return nil, fmt.Errorf("ping time is in invalid format: %s", err)
		}
	}

	c.DeadTime = 240 * time.Second
	if m["dead-time"] != "" {
		c.DeadTime, err = time.ParseDuration(m["dead-time"])
		if err != nil {
			return nil, fmt.Errorf("dead time is in invalid format: %s", err)
		}
	}

	c.ConnectAttemptTime = 60 * time.Second
	if m["connect-attempt-time"] != "" {
		c.ConnectAttemptTime, err = time.ParseDuration(m["connect-attempt-time"])
		if err != nil {
			return nil, fmt.Errorf("connect attempt time is in invalid format: %s",
				err)
		}
	}

	// opers.conf.

	if m["opers-config"] != "" {
		opers, err := config.ReadStringMap(m["opers-config"])
		if err != nil {
			return nil, fmt.Errorf("unable to load opers config: %s", err)
		}
		c.Opers = opers
	} else {
		c.Opers = map[string]string{}
	}

	// servers.conf.

	c.Servers = make(map[string]*ServerDefinition)

	if m["servers-config"] != "" {
		servers, err := config.ReadStringMap(m["servers-config"])
		if err != nil {
			return nil, fmt.Errorf("unable to load servers config: %s", err)
		}

		for name, v := range servers {
			link, err := parseLink(name, v)
			if err != nil {
				return nil, fmt.Errorf("malformed server link information: %s: %s",
					name, err)
			}
			c.Servers[name] = link
		}
	}

	// users.conf.

	if m["users-config"] != "" {
		usersConfig, err := config.ReadStringMap(m["users-config"])
		if err != nil {
			return nil, fmt.Errorf("unable to load users config: %s", err)
		}

		for name, value := range usersConfig {
			userConfig, err := parseUserConfig(value)
			if err != nil {
				return nil, fmt.Errorf("unable to parse user config %s: %s: %s", name,
					value, err)
			}
			c.UserConfigs = append(c.UserConfigs, userConfig)
		}
	}

	c.TS6SID = TS6SID("000")

	if m["ts6-sid"] != "" {
		if !isValidSID(m["ts6-sid"]) {
			return nil, fmt.Errorf("invalid TS6 SID")
		}
		c.TS6SID = TS6SID(m["ts6-sid"])
	}

	c.AdminEmail = m["admin-email"]

	return c, nil
}

// Parse the value side of a server definition from the servers config.
// Format:
// <hostname>,<port>,<password>,<tls: 1 or 0>
func parseLink(name, s string) (*ServerDefinition, error) {
	pieces := strings.Split(s, ",")
	if len(pieces) != 4 {
		return nil, fmt.Errorf("unexpected number of fields")
	}

	hostname := strings.TrimSpace(pieces[0])
	if len(hostname) == 0 {
		return nil, fmt.Errorf("you must specify a hostname")
	}
	// We could format check hostname. But when we try to listen we'll fail.

	port, err := strconv.ParseInt(strings.TrimSpace(pieces[1]), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %s: %s", pieces[1], err)
	}

	pass := strings.TrimSpace(pieces[2])
	if len(pass) == 0 {
		return nil, fmt.Errorf("you must specify a password")
	}

	return &ServerDefinition{
		Name:     name,
		Hostname: hostname,
		Port:     int(port),
		Pass:     pass,
		TLS:      pieces[3] == "1",
	}, nil
}

// Parse the value part of a user config line.
// This is a comma separated value.
// A line looks like so:
// <name> = <user mask>,<host mask>,<flood exempt = 1|0>,<spoof>
//
// This function takes the portion after the equals sign and parses it.
//
// <name> is an identifier for readability in the config. We don't use it beyond
// that.
//
// <user mask> and <host mask> define how to match the user's raw user and
// host. If they both match, the user falls under this config.
//
// Spoof may be empty.
func parseUserConfig(s string) (UserConfig, error) {
	piecesUntrimmed := strings.Split(s, ",")
	if len(piecesUntrimmed) != 4 {
		return UserConfig{}, fmt.Errorf("unexpected number of fields")
	}

	pieces := []string{}
	for _, piece := range piecesUntrimmed {
		pieces = append(pieces, strings.TrimSpace(piece))
	}

	if !isValidUserMask(pieces[0]) {
		return UserConfig{}, fmt.Errorf("invalid user mask")
	}
	userMask := pieces[0]

	if !isValidHostMask(pieces[1]) {
		return UserConfig{}, fmt.Errorf("invalid host mask")
	}
	hostMask := pieces[1]

	if pieces[2] != "1" && pieces[2] != "0" {
		return UserConfig{}, fmt.Errorf("flood exempt flag must be 1 or 0")
	}
	floodExempt := pieces[2] == "1"

	spoof := pieces[3]
	if len(spoof) > 0 {
		if !isValidHostname(spoof) {
			return UserConfig{}, fmt.Errorf("invalid spoof hostname")
		}
	}

	return UserConfig{
		UserMask:    userMask,
		HostMask:    hostMask,
		FloodExempt: floodExempt,
		Spoof:       spoof,
	}, nil
}
