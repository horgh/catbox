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
  * Not supporting forwarding PING/PONG to other servers.
  * No wildcards or target server support in WHOIS command.
  * Added DIE command.
  * WHOIS command: No server target, and only single nicks.
  * WHOIS command: Currently not going to show any channels.
  * User modes: Only +o
  * Channel modes: Only +n
  * WHO: Support only 'WHO #channel'. And shows all nicks on that channel.


# Todo

  * Server to server
    * Update LUSERS counts.
  * Server to server (ircd-ratbox)
  * TLS
  * Upgrade in place


## Maybe

  * CTCP PING
  * TOPIC
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
  * WHOWAS
  * KILL
  * AWAY
  * Multi line motd
  * Reload configuration without restart
