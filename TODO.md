# TODO
  * Daemonize
  * Log to file
  * Additional automated testing


## RFC
  * Channel modes: +o/+v/+b/+i/+k/etc
  * User modes: ?
  * INVITE
  * KICK
  * TIME
  * ADMIN
  * INFO
  * WHOWAS
  * AWAY


## Basics
  * Anti-abuse (throttling etc)


## Unimportant
  * NAMES
  * LIST
  * STATS (more flags)
  * Multi line motd
  * Respond to remote STATS requests
  * Support sending more remote queries (e.g. STATS to another server)


## Non-standard
  * Upgrade in place (is this possible with TLS connections? or at all?)
  * Server console.
  * Upgrade without losing connections
  * Inform clients when someone whois's them.
  * Bots built into the ircd
  * Exchange K:Lines during server burst
  * User spoofs
  * A command to retrieve TLS/ciphers in use by all clients/servers. Sent
    remotely so each server can get back to us with their local info.
  * Persistent K:Lines (currently they are in memory only)


## Design
  * Drop messageUser/messageFromServer? messageUser all together,
    messageFromServer to be reply()?
