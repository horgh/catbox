# TODO

## Higher priority
* Convert tests to use stretchr/testify.
* Show IPs to opers in WHOIS with 378 numeric.
* Op desync issue - should be de-opped if we have an op and link to a
  server where the channel already exists. Can see not-op on one side and
  op on the catbox side. I think this is because of us clearing modes on
  SJOIN commands, but those cleared modes only get sent locally.
* PASS command for users to authenticate.
  * Authenticated user should show in WHOIS with 330 numeric.
* Automatically spoof people's hosts.
* WHOWAS.
* Many log calls should probably go to opers. Right now they will probably
  always be missed.
* Additional tests.
* Loading config should error if there is an unknown option
* Channel mode +i


## Uncategorized/unprioritized
* Command to dump out entire state. Servers, channels, nicks, modes, etc.
  This could be used for monitoring that every server is in sync.
* Switch config to TOML
* Make canonicalizeNick and canonicalizeChannel return error if the names
  are invalid? Right now it is a bit error prone because we can
  canonicalize invalid names.
* Consider combining cleanup user logic in server's killCommand() with
  cleanupKilledUser()
* Consolidate repeated topic setting logic (user TOPIC, server TOPIC, TB)
* Add command to dump out config (the current catbox config as seen from
  the config file). Partly this will be useful because not everything gets
  reloaded on rehash.
* A command (NSA) to retrieve TLS/ciphers in use by all clients/servers.
  Sent remotely so each server can get back to us with their local info. It
  will require a parameter, channel name or mask. So we can see all
  relevant info for a channel or user, or if an operator, for all users.
* Limit on number of modes applied only important for modes with
  parameters? Or only user status modes?


## Lower priority
* PART command can come from server with just nick as prefix rather than
  UID. Happens using OPME on ratbox. Causes unknown user error and server
  split
  * Because I'm not linking with ratbox currently.
* Log to file


## RFC
* Channel modes: +v/+b/+k/etc
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
* Server console.
* Inform clients when someone WHOIS's them.
* Exchange K:Lines during server burst
* Persistent K:Lines (currently they are in memory only)


## Design
* Drop messageUser/messageFromServer? messageUser all together,
  messageFromServer to be reply()?


# No
* Bots built into the ircd
* Daemonize.
  * There is no support in Go for this right now.
  * Using init system seems sufficient
* Upgrade in place without losing connections
  * Not really feasible with current TLS library as connection state can't
    be kept.
