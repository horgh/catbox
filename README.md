![catbox](images/catbox-with-text.png)

This is a minimal IRC daemon. I run a small network using
[ircd-ratbox](http://ratbox.org/) and had the idea to create an IRC server
capable of linking to it and eventually replacing it. My goal is to have a
minimal server that will meet this network's needs.

Current status: catbox is capable of linking to itself and to ircd-ratbox
servers. It is in active use on my network.


# Name
I chose the name because: My domain name is summercat.com, cats love boxes,
and as a tribute to ircd-ratbox.


# Features

  * Server to server communication using the TS6 protocol. In addition to
    being able to link to other catbox servers, it can link with other
    TS6 servers such as ircd-ratbox.
  * Channels, private messages, etc. Most of the basic IRC commands and
    features are present.
  * Channel operators.
  * Channel modes: +n, +s, +o.
  * User modes: +i, +o, +C.
  * Global IRC operators.
  * Operators can communicate network wide to other operators with WALLOPS.
  * Private (WHOIS shows no channels, no LIST).
  * Server connections are based on hosts rather than IPs. This means
    servers can have dynamic IPs.
  * Network wide connection notices sent to operators.


# Setup
First you need to build the server. To do this, run `go build`. You need a
working [go compiler](https://golang.org/dl/).

Then you need to configure it. This is done through a number of files.
Examples of these files are all under the `conf` directory. Copy and edit
them as you like.

Once you've done this, start the daemon like so:

    catbox -config catbox.conf


## catbox.conf
This file holds global settings for the server.

You'll probably want to change `listen-host`, `listen-port`, and
`server-name` at the minimum.


## opers.conf
This file defines IRC operators. A user can become an operator by using the
`/OPER` command with a username/password combination listed in this file.


## servers.conf
This file defines servers to link to and accept links from.


## users.conf
This file defines privileges and spoofs for users. One privilege is flood
exemption.


## TLS
If you want to listen on a TLS port, you must have a certificate and key
available.

To generate a self-signed certificate for TLS:

    openssl req -newkey rsa:4096 -x509 -keyout key.pem -out certificate.pem -days 3650 -nodes


# Logo
catbox logo (c) 2017 Bee
