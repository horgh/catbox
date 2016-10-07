# catbox
This is yet another IRC server! I'm creating it because I enjoy working with
IRC and I thought it would be fun and good practice with Go. Also, I run a
small IRC network, and it would be nice to have my own server for it. Right now
I use ircd-ratbox which is great, but I wanted to try building one myself.

I call it catbox. I went with the name for a few reasons: My domain name is
summercat.com so I already have a cat reference, cats love boxes, and because
of it's similar to ircd-ratbox!

Features:

  * Client protocol is generally RFC 2812/1459. It does not fully implement the
    protocol and diverges in some cases for simplicity.
  * Server to server communication using the TS6 protocol. This means it is able
    to link to TS6 ircds (to a degree), such as ircd-ratbox. It can also link to
    other instances of itself.
  * Channels, private messages, etc. Most of the basic IRC commands and features
    one expects are present.
  * No channel operators. (Maybe in the future)
  * No channel modes beyond +ns which is always set. (Maybe more in the future)
  * No user modes beyond +i and +o. (Maybe more in the future)
  * Global IRC operators.
  * Operators can communicate network wide to other operators with WALLOPS.
  * Private (WHOIS shows no channels, no LIST).
  * Server to server connections allow server IPs to change without
    configuration updates (i.e., permitting dynamic server IPs)

Design philosophy:

  * Only a subset of RFC 2812 / 1459 which I personally think makes sense. Only
    what is critical for a minimal IRC server. As simple as possible. If the
    RFC suggests something I don't like, and I think clients will be compliant,
    then I'll probably do something else. I'll try to track differences.
  * Minimal configuration
  * Simple and easily extensible
  * If there are extra parameters to commands, ignore them.


# Setup
The daemon always listens on a plaintext port and a TLS port. This means you
must have a certificate and key available.

To generate a self-signed certificate for TLS:

    openssl req -newkey rsa:4096 -x509 -keyout key.pem -out certificate.pem -days 3650 -nodes

Copy the three configuration files {example,opers-example,servers-example}.conf
and update their options as you need. Below I make suggestions for what you will
want to update.

Once you have edited the configuration files, you can start the daemona like so:

    ./ircd -config server.conf


## server.conf (example.conf)

  * You will probably need to change listen-host
  * You will probably want to change server-name
  * You will probably want to change the opers-config and servers-config paths.

The other options you may find it acceptable to leave as they are.


## opers.conf (opers-example.conf)
This file defines operators. Any user connected can become an oper by using the
/OPER command and using a combination listed in this file. You should change
the default.


## servers.conf (servers-example.conf)
This file defines servers to try to link to (and accept links from). You may not
want any at first. Comment out the example server if so.


# Some differences from RFC 2812 / RFC 1459
This is not exhaustive, but some of the differences are:

  * Only # channels supported.
  * Much more restricted characters in channels/nicks/users.
  * Do not support parameters to the LUSERS command.
  * Do not support parameters to the MOTD command.
  * Not supporting forwarding PING/PONG to other servers (by users).
  * No wildcards or target server support in WHOIS command.
  * Added DIE command.
  * WHOIS command: No server target, and only single nicks.
  * WHOIS command: Currently not going to show any channels.
  * WHOIS command: Always send to remote server if remote user.
  * User modes: Only +oiC
  * Channel modes: Only +ns
  * No channel ops or voices.
  * WHO: Support only 'WHO #channel'. And shows all nicks on that channel.
  * CONNECT: Single parameter only.
  * LINKS: No parameters supported.
  * LUSERS: Include +s channels in channel count.


# External documentation and references
  * https://tools.ietf.org/html/rfc2812
  * https://tools.ietf.org/html/rfc1459
  * TS6 docs:
    * charybdis's ts6-protocol.txt
    * ircd-ratbox's ts6.txt, ts5.txt, README.TSora
  * ircv3
  * http://ircdocs.horse/


# TS6 notes
  * Nick TS changes when: Client connects or when it changes its nick.
  * Channel TS changes when: Channel created
  * Server to server (ircd-ratbox) commands I'm most interested in:
    * Burst: SID, UID, SJOIN, ERROR, PING, PONG
    * Post-burst: INVITE, JOIN, KILL, NICK, NOTICE, PART, PRIVMSG, QUIT, SID,
      SJOIN, TOPIC, UID, SQUIT, ERROR, PING, PONG, MODE (user)
