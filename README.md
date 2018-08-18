![catbox](doc/catbox-with-text.png)

[![Build
Status](https://travis-ci.org/horgh/catbox.svg)](https://travis-ci.org/horgh/catbox)
[![Go Report
Card](https://goreportcard.com/badge/github.com/horgh/catbox)](https://goreportcard.com/report/github.com/horgh/catbox)

catbox is an IRC daemon. I run a small network using it.


# Why the name?
My domain name is summercat.com, cats love boxes, and a tribute to
ircd-ratbox (the IRC daemon I used in the past).


# Features
* Server to server communication using the TS6 protocol. In addition to
  being able to link to other catbox servers, it can link with other TS6
  servers such as ircd-ratbox.
* Most basic IRC commands and features are present.
* Channel modes: +n, +s, +o.
* User modes: +i, +o, +C.
* Global IRC operators.
* Network wide operator communication with WALLOPS.
* Private (WHOIS shows no channels, LIST isn't supported).
* Server connections are based on hosts rather than IPs. This means servers
  can have dynamic IPs.
* Network wide connection notices sent to operators.
* Flood protection.
* K: line style connection banning.
* TLS. Certificate/key hotloading at rehash time as well.


# Setup
1. Build the server. You need a working [Go
   compiler](https://golang.org/dl/). An easy way to do this is to run `go
   get -u github.com/horgh/catbox`.
2. Configure it. This is done through configuration files. Examples are in
   the `conf` directory. Copy and edit them. All settings are optional and
   have defaults.
3. Start the daemon: `catbox -conf catbox.conf`


# Configuration

## catbox.conf
This file holds global settings for the server.

You'll probably want to change `listen-host`, `listen-port`, and
`server-name` at minimum.

If you want to listen on a TLS port, you must have a certificate and key
available.


## opers.conf
This file defines IRC operators. A user can become an operator by using the
`OPER` command with a username and password combination listed in this file.


## servers.conf
This file defines servers to link to and accept links from.


## users.conf
This file defines privileges and hostname spoofs for users. The only
privilege right now is flood exemption.


# Logo
catbox logo (c) 2017 Bee
