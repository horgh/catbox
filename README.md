![catbox](doc/catbox-with-text.png)

[![Build
Status](https://travis-ci.org/horgh/catbox.svg)](https://travis-ci.org/horgh/catbox)
[![Go Report
Card](https://goreportcard.com/badge/github.com/horgh/catbox)](https://goreportcard.com/report/github.com/horgh/catbox)

catbox is an IRC server with a focus on being small and understandable. The
goal is security.


# Features
* Server to server communication using the TS6 protocol
* Channel modes: +n, +s, +o
* User modes: +i, +o, +C
* Global IRC operators
* Network wide operator communication with WALLOPS
* Private (WHOIS shows no channels, LIST isn't supported)
* Server connections are based on hosts rather than IPs
* Network wide connection notices sent to operators
* Flood protection
* K: line style connection banning
* TLS


# Installation
1. Download catbox from the Releases tab on GitHub, or build from source
   (`go build`).
2. Configure catbox through config files. There are example configs in the
   `conf` directory. All settings are optional and have defaults.
3. Run it, e.g. `./catbox -conf catbox.conf`. I typically run catbox
   inside tmux using [this program](bin/tmux-run.sh).


# Configuration

## catbox.conf
Global server settings.


## opers.conf
IRC operators.


## servers.conf
The servers to link with.


## users.conf
Privileges and hostname spoofs for users.

The only privilege right now is flood exemption.


## TLS
A setup for a network might look like this:

* Give each server a certificate with 2 SANs: Its own hostname, e.g.
  server1.example.com, and the network hostname, e.g. irc.example.com.
* Set up irc.example.com with DNS round-robin listing each server's IP.
* List each server by its own hostname in servers.conf.

Clients connect to the network hostname and verify against it. Servers
connect to each other by server hostname and verify against it.


# Why the name?
My domain name is summercat.com, cats love boxes, and a tribute to
ircd-ratbox, the IRC daemon I used in the past.


# Logo
catbox logo (c) 2017 Bee
