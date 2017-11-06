# IRC

[![Build
Status](https://travis-ci.org/horgh/irc.svg)](https://travis-ci.org/horgh/irc)
[![GoDoc](https://godoc.org/github.com/horgh/irc?status.svg)](https://godoc.org/github.com/horgh/irc)
[![Go Report
Card](https://goreportcard.com/badge/github.com/horgh/irc)](https://goreportcard.com/report/github.com/horgh/irc)

This package provides functionality for working with the IRC protocol.
Specifically, it provides decoding and encoding of IRC messages.

It is useful for writing IRC servers and bots.
[catbox](https://github.com/horgh/catbox) uses it, as does
[godrop](https://github.com/horgh/godrop).

In general it follows [RFC 1459](https://tools.ietf.org/html/rfc1459). RFC
1459 is mostly compatible with at the message format level with [RFC
2812](https://tools.ietf.org/html/rfc2812). Where there is a difference,
this package favours RFC 1459.

Due to the vagaries of IRC servers and clients in the wild, this package is
lenient and will decode messages even if they are not fully RFC compliant.
For example:

  * It silently ignores trailing spaces in messages in certain cases (in
    locations where they should be considered invalid).
  * It allows messages to end with bare LF rather than the required CRLF.
