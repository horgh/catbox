This file holds information that is more useful for developers or is a bit
too in depth to be in the REAMDE.


# Design philosophy

  * Support a subset of RFC 2812 / 1459 which I think makes sense. In
    practice this means what is critical for a minimal IRC server. This is
    also influenced by how the network I run operates (typically no channel
    operators and well behaved users), meaning we don't need many things,
    such as many channel modes.
  * Minimal configuration.
  * Simple and extensible.
  * If there are extra parameters to commands, ignore them.


# Some differences from RFC 2812 / RFC 1459
This is not exhaustive.

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
  * Channel modes: Only +nos
  * WHO: Support only 'WHO #channel'. And shows all nicks on that channel.
  * CONNECT: Single parameter only.
  * LINKS: No parameters supported.
  * LUSERS: Include +s channels in channel count.
  * VERSION: No parameter used.
  * TIME: No parameter used.
  * WHOWAS: Always say no such nick.


# How flood control works
  * Each client has a counter that starts out at UserMessageLimit (10)
  * Every message we process from the client, we decrement it by one.
  * If the counter is zero, we queue the message.
  * Each second (woken by our alarm goroutine), we increment the client's
    counter by 1 to a maximum of UserMessageLimit, and process queued messages
    until the counter is zero.
  * If there are too many queued messages, we disconnect the client for
    flooding (ExcessFloodThreshold).

This is similar to ircd-ratbox's algorithm.

While client message events and alarm events go to the same channel, if a client
sends a large number of messages, they will trigger an excess flood. This means
the daemon should not be overwhelmed by a single client.


# External documentation and references
  * https://tools.ietf.org/html/rfc2812
  * https://tools.ietf.org/html/rfc1459
  * TS6 docs:
    * charybdis's ts6-protocol.txt
    * ircd-ratbox's ts6.txt, ts5.txt, README.TSora
  * ircv3
  * http://ircdocs.horse/


## TS6 notes
  * Nick TS changes when: Client connects or when it changes its nick.
  * Channel TS changes when: Channel created
  * Server to server (ircd-ratbox) commands I'm most interested in:
    * Burst: SID, UID, SJOIN, ERROR, PING, PONG
    * Post-burst: INVITE, JOIN, KILL, NICK, NOTICE, PART, PRIVMSG, QUIT, SID,
      SJOIN, TOPIC, UID, SQUIT, ERROR, PING, PONG, MODE (user)
  * I believe "simple modes" are things like +ntisk. As opposed to status modes
    such as +o/+v. Ban/exemption type modes are not simple either.
