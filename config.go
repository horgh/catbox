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

	// Oper name to password.
	Opers map[string]string

	// Server name to its link information.
	Servers map[string]*ServerDefinition

	// User configuration info.
	UserConfigs []UserConfig

	// TS6 SID. Must be unique in the network. Format: [0-9][A-Z0-9]{2}
	TS6SID TS6SID
}

// ServerDefinition defines how to link to a server.
type ServerDefinition struct {
	Name               string
	Hostname           string
	Port               int
	Pass               string
	TLS                bool
	LastConnectAttempt time.Time
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
	configMap, err := config.ReadStringMap(file)
	if err != nil {
		return nil, err
	}

	requiredKeys := []string{
		"listen-host",
		"listen-port",
		"listen-port-tls",
		"certificate-file",
		"key-file",
		"server-name",
		"server-info",
		"motd",
		"max-nick-length",
		"ping-time",
		"dead-time",
		"connect-attempt-time",
		"opers-config",
		"servers-config",
		"users-config",
		"ts6-sid",
	}

	// Check each key we want is present and non-blank.
	for _, key := range requiredKeys {
		v, exists := configMap[key]
		if !exists {
			return nil, fmt.Errorf("Missing required key: %s", key)
		}

		// All options must be non-blank. Except those listed in this check.
		if len(v) == 0 && key != "listen-port" && key != "listen-port-tls" &&
			key != "certificate-file" && key != "key-file" {
			return nil, fmt.Errorf("Configuration value is blank: %s", key)
		}
	}

	// Populate our struct.

	c := &Config{}

	c.ListenHost = configMap["listen-host"]
	c.ListenPort = configMap["listen-port"]
	c.ListenPortTLS = configMap["listen-port-tls"]
	c.CertificateFile = configMap["certificate-file"]
	c.KeyFile = configMap["key-file"]
	c.ServerName = configMap["server-name"]
	c.ServerInfo = configMap["server-info"]
	c.MOTD = configMap["motd"]

	nickLen64, err := strconv.ParseInt(configMap["max-nick-length"], 10, 8)
	if err != nil {
		return nil, fmt.Errorf("Max nick length is not valid: %s", err)
	}
	c.MaxNickLength = int(nickLen64)

	c.PingTime, err = time.ParseDuration(configMap["ping-time"])
	if err != nil {
		return nil, fmt.Errorf("Ping time is in invalid format: %s", err)
	}

	c.DeadTime, err = time.ParseDuration(configMap["dead-time"])
	if err != nil {
		return nil, fmt.Errorf("Dead time is in invalid format: %s", err)
	}

	c.ConnectAttemptTime, err = time.ParseDuration(configMap["connect-attempt-time"])
	if err != nil {
		return nil, fmt.Errorf("Connect attempt time is in invalid format: %s", err)
	}

	opers, err := config.ReadStringMap(configMap["opers-config"])
	if err != nil {
		return nil, fmt.Errorf("Unable to load opers config: %s", err)
	}
	c.Opers = opers

	c.Servers = make(map[string]*ServerDefinition)
	servers, err := config.ReadStringMap(configMap["servers-config"])
	if err != nil {
		return nil, fmt.Errorf("Unable to load servers config: %s", err)
	}

	for name, v := range servers {
		link, err := parseLink(name, v)
		if err != nil {
			return nil, fmt.Errorf("Malformed server link information: %s: %s", name,
				err)
		}
		c.Servers[name] = link
	}

	usersConfig, err := config.ReadStringMap(configMap["users-config"])
	if err != nil {
		return nil, fmt.Errorf("Unable to load users config: %s", err)
	}

	for name, value := range usersConfig {
		userConfig, err := parseUserConfig(value)
		if err != nil {
			return nil, fmt.Errorf("Unable to parse user config %s: %s: %s", name,
				value, err)
		}
		c.UserConfigs = append(c.UserConfigs, userConfig)
	}

	if !isValidSID(configMap["ts6-sid"]) {
		return nil, fmt.Errorf("Invalid TS6 SID")
	}
	c.TS6SID = TS6SID(configMap["ts6-sid"])

	return c, nil
}

// Parse the value side of a server definition from the servers config.
// Format:
// <hostname>,<port>,<password>,<tls: 1 or 0>
func parseLink(name, s string) (*ServerDefinition, error) {
	pieces := strings.Split(s, ",")
	if len(pieces) != 4 {
		return nil, fmt.Errorf("Unexpected number of fields")
	}

	hostname := strings.TrimSpace(pieces[0])
	if len(hostname) == 0 {
		return nil, fmt.Errorf("You must specify a hostname")
	}
	// We could format check hostname. But when we try to listen we'll fail.

	port, err := strconv.ParseInt(strings.TrimSpace(pieces[1]), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("Invalid port: %s: %s", pieces[1], err)
	}

	pass := strings.TrimSpace(pieces[2])
	if len(pass) == 0 {
		return nil, fmt.Errorf("You must specify a password")
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
		return UserConfig{}, fmt.Errorf("Unexpected number of fields")
	}

	pieces := []string{}
	for _, piece := range piecesUntrimmed {
		pieces = append(pieces, strings.TrimSpace(piece))
	}

	if !isValidUserMask(pieces[0]) {
		return UserConfig{}, fmt.Errorf("Invalid user mask")
	}
	userMask := pieces[0]

	if !isValidHostMask(pieces[1]) {
		return UserConfig{}, fmt.Errorf("Invalid host mask")
	}
	hostMask := pieces[1]

	if pieces[2] != "1" && pieces[2] != "0" {
		return UserConfig{}, fmt.Errorf("Flood exempt flag must be 1 or 0.")
	}
	floodExempt := pieces[2] == "1"

	spoof := pieces[3]
	if len(spoof) > 0 {
		if !isValidHostname(spoof) {
			return UserConfig{}, fmt.Errorf("Invalid spoof hostname.")
		}
	}

	return UserConfig{
		UserMask:    userMask,
		HostMask:    hostMask,
		FloodExempt: floodExempt,
		Spoof:       spoof,
	}, nil
}
