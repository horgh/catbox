# TODO

  * Daemonize
  * Log to file
  * Additional automated testing


## Maybe

  * Drop messageUser/messageFromServer? messageUser all together,
    messageFromServer to be reply()?
  * LIST
  * Channel keys
  * INVITE
  * KICK
  * NAMES
  * VERSION
  * STATS (more flags)
  * TIME
  * ADMIN
  * INFO
  * WHOWAS
  * AWAY
  * Multi line motd
  * Upgrade in place (is this possible with TLS connections? or at all?)
  * Server console.
  * Anti-abuse (throttling etc)
  * Upgrade without losing connections
  * Inform clients when someone whois's them.
  * Bots could be built into the ircd
  * Persistent K:Lines (currently they are in memory only)
  * Respond to remote STATS requests
  * Support sending more remote queries (e.g. STATS to another server)
  * Exchange K:Lines during server burst
  * User spoofs
  * A command to retrieve TLS/ciphers in use by all clients/servers. Sent
    remotely so each server can get back to us with their local info.
