# Unreleased

* Rehashing now reloads the TLS certificate and key.

# 1.9.0 (2018-07-28)

* Started tracking changes in a changelog.
* If a message is invalid, send a notice to opers about it rather than just
  log. This is to catch bugs arising from this behaviour.
* Send a notice to opers if there is an unprocessed buffer after a read
  error. This is again to catch bugs from this behaviour.
* Failing to set read deadlines now logs rather than triggers client quit.
  This is to allow for the read to happen which can have data in the
  buffer.
* Delay before flagging the client as dead if there is a write error. This
  is to give us a window to read anything further from the client that
  might be available.
