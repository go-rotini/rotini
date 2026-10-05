#!/bin/bash
# Starts rotini-loop.tape's split terminal: tmux on its own socket with two panes running bash with
# only the demo prompt, both in the working directory, the left one active; then attaches. The
# session is built before attaching, so no keystroke can arrive before tmux is ready.
set -euo pipefail
shell="bash --noprofile --rcfile '$DEMO/bashrc'"
t() { tmux -L rotini-demo "$@"; }
t -f "$DEMO/tmux.conf" new-session -d -s demo -x 160 -y 40 "$shell"
t split-window -h -t demo -c "$PWD" "$shell"
t select-pane -t demo:0.0
exec tmux -L rotini-demo attach -t demo
