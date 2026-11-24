#!/bin/sh
# POSIX helpers and edge cases

set -eu

typeset -r BUILD_ID=42
declare -rx FROZEN="yes"
readonly -a PRIMES=(2 3 5)
readonly SOLO=1 OTHER=2
declare -g GLOBAL_FLAG=on
LOG_LEVEL=${LOG_LEVEL:-info} TZ=UTC
PATH="$PATH:/opt/bin"

alias la=ls
alias grep='grep --color=auto'

die() { echo "error: $*" >&2; exit 1; }

is_root() { [ "$(id -u)" -eq 0 ]; }

function with_keyword() {
    local tmp
    tmp=$(mktemp)
    TEMP_COPY=$tmp
    export INNER_EXPORT=1
    readonly INNER_RO=2
    echo "$tmp"
}

retry() {
    attempts=0
    until "$@"; do
        attempts=$((attempts + 1))
        [ "$attempts" -ge 3 ] && return 1
    done
}

cat <<'EOF'
fake_in_heredoc() {
    echo "not a definition"
}
EOF

echo "text with fake_in_string() { inside }"

export -f die
export BEFORE_FN

FOO=1 env | grep FOO
for f in *.txt; do
    name="$f"
done
while read -r line; do
    seen="$line"
done < /dev/null

case "$1" in
    start) started=yes ;;
esac

trap cleanup EXIT

[ -f /etc/os-release ] && . /etc/os-release

pipeline() {
    ls | while read -r entry; do
        echo "$entry"
    done
}

if command -v curl >/dev/null; then
    fetch() { curl -fsSL "$1"; }
else
    fetch() { wget -qO- "$1"; }
fi

DEFAULT_ARGS=(--verbose --force)
SPACED = not_an_assignment
