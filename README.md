# Summary

Yet another IRC server! I'm creating it because I enjoy working with IRC and I
thought it would be good practice. I run a small IRC network, and it would be
nice to have my own server for it. Right now I use ircd-ratbox.

The main ideas I plan for it are (in no particular order):

  * Server to server connections (to other instances)
  * Server to server connections (to ircd-ratbox)
  * Only a small subset of RFC 2812 which I personally think makes sense. Only
    what is critical for a minimal IRC server. As simple as possible. If the
    RFC suggests something I don't like, and I think clients will be compliant,
    then I'll probably do something else. I'll try to track differences.
  * TLS
  * Upgrade without losing connections
  * Minimal configuration
  * Simple and easily extensible
  * Server to server connections allowing server IPs to change without
    configuration updates (i.e., permitting dynamic server IPs)
  * Cool features as I come up with them. Some ideas I have:
    * Inform clients when someone whois's them.
    * Inform clients about TLS ciphers in use (both on connect and in their
      whois)
    * Bots could be built right into the ircd
    * Highly private (very restricted whois, list, etc)


# Differences from RFC 2812

  * Only # channels supported.
  * Much more restricted characters in channels/nicks/users.
  * Do not support parameters to the LUSERS command.
  * Do not support parameters to the MOTD command.


# Todo

  * Enforce nick/channel lengths
  * Deal with PRIVMSG length being too long to send to others
  * QUIT
  * PING response
  * PING/PONG
  * WHOIS
  * OPER
  * Clean shutdown
  * Client connection loss
  * Server to server
    * Update LUSERS counts.
  * Server to server (ircd-ratbox)
  * TLS
  * Upgrade in place
  * Check required configuration keys on startup


## Maybe
  * CTCP PING
  * TOPIC
  * MODE (channels)
  * MODE (users)
  * LIST
  * Channel keys
  * INVITE
  * KICK
  * NAMES
  * NOTICE
  * VERSION
  * STATS
  * LINKS
  * TIME
  * ADMIN
  * INFO
  * WHO
  * WHOWAS
  * KILL
  * AWAY
  * Multi line motd
  * Reload configuration without restart