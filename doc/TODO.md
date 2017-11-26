# TODO
  * PART command can come from server with just nick as prefix rather than
    UID. Happens using OPME on ratbox. Causes unknown user error and server
    split
  * Op desync issue - should be de-opped if we have an op and link to a
    server where the channel already exists. Can see not-op on one side and
    op on the catbox side
  * Make canonicalizeNick and canonicalizeChannel return error if the names are
    invalid? Right now it is a bit error prone because we can canonicalize
    invalid names.
  * Consider combining cleanup user logic in server's killCommand() with
    cleanupKilledUser()
  * Consolidate repeated topic setting logic (user TOPIC, server TOPIC, TB)
  * Log to file
  * Additional automated testing
  * Easy updating
  * Add command to dump out config (the current catbox config as seen from
    the config file). Partly this will be useful because not everything
    gets reloaded on rehash.

  * MODE command on channel should return when channel was created
  * A command (NSA) to retrieve TLS/ciphers in use by all clients/servers. Sent
    remotely so each server can get back to us with their local info. It will
    require a parameter, channel name or mask. So we can see all relevant
    info for a channel or user, or if an operator, for all users.
  * Limit on number of modes applied only important for modes with parameters?
    Or only user status modes?
  * +v/-v


## Future
  * Daemonize. There is no support in Go for this right now.


## RFC
  * Channel modes: +v/+b/+i/+k/etc
  * KICK


# Maybe

## Unimportant
  * NAMES
  * LIST
  * STATS (more flags)
  * ADMIN
  * INFO
  * Multi line motd
  * Respond to remote STATS requests
  * Support sending more remote queries (e.g. STATS to another server)
  * Retain channel creation times and topics through restarts


## Non-standard
  * Upgrade in place (is this possible with TLS connections without
    disconnecting them?)
  * Server console.
  * Upgrade without losing connections
  * Inform clients when someone whois's them.
  * Bots built into the ircd
  * Exchange K:Lines during server burst
  * Persistent K:Lines (currently they are in memory only)


## Design
  * Drop messageUser/messageFromServer? messageUser all together,
    messageFromServer to be reply()?
