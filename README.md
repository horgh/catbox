![catbox](images/catbox-with-text.png)

This is a minimal IRC daemon. I run a small network using
[ircd-ratbox](http://ratbox.org/) and had the idea to create an IRC server
capable of linking to it and eventually replacing it. My goal is to have a
minimal server that will meet this network's needs.


# Status
catbox is capable of linking to itself and to ircd-ratbox servers. It is in
active use on my network.


# Why the name?
My domain name is summercat.com, cats love boxes, and a tribute to
ircd-ratbox.


# Features

  * Server to server communication using the TS6 protocol. In addition to
    being able to link to other catbox servers, it can link with other
    TS6 servers such as ircd-ratbox.
  * Most basic IRC commands and features are present.
  * Channel modes: +n, +s, +o.
  * User modes: +i, +o, +C.
  * Global IRC operators.
  * Network wide operator communication with WALLOPS.
  * Private (WHOIS shows no channels, LIST isn't supported).
  * Server connections are based on hosts rather than IPs. This means
    servers can have dynamic IPs.
  * Network wide connection notices sent to operators.


# Setup
  1. Build the server. To do this, run `go build`. You need a working [go
     compiler](https://golang.org/dl/).
  2. Configure it. This is done through configuration files. Examples of
     are in the `conf` directory. Copy and edit them as you like. All
     settings are optional and have defaults.
  3. Start the daemon: `catbox -conf catbox.conf`


# Configuration

## catbox.conf
This file holds global settings for the server.

You'll probably want to change `listen-host`, `listen-port`, and
`server-name` at the minimum. However, all settings are optional and have
defaults.


## opers.conf
This file defines IRC operators. A user can become an operator by using the
`/OPER` command with a username/password combination listed in this file.


## servers.conf
This file defines servers to link to and accept links from.


## users.conf
This file defines privileges and spoofs for users. One privilege is flood
exemption.


# TLS
If you want to listen on a TLS port, you must have a certificate and key
available.

To generate a self-signed certificate for TLS:

    openssl req -newkey rsa:4096 -x509 -keyout key.pem -out certificate.pem -days 3650 -nodes


# Logo
catbox logo (c) 2017 Bee
