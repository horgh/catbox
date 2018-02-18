#!/bin/bash
#
# This is a way to run catbox in a tmux session.
#
# It runs in such a way as if catbox exits, the tmux window stays around so
# that we can inspect catbox's recent output. This is useful for debugging.
#
# It would probably be better to run catbox via systemd or something, but I
# don't want catbox's output to be logged anywhere. I only want recent output
# to be accessible. Possibly systemd could be made to do that, but anyway.

set -e

tmux start-server
tmux new-session -d -s catbox

tmux set-option -g set-remain-on-exit on
tmux set-option -g history-limit 10000
tmux set-option -g prefix2 C-a
tmux set-option -g prefix C-a
tmux bind-key C-a send-prefix
tmux set-window-option -g mode-keys vi

tmux new-window /home/ircd/catbox/catbox -conf /home/ircd/catbox/catbox.conf
