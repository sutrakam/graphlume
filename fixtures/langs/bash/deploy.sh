#!/usr/bin/env bash
# deploy helpers

VERSION="1.2.3"
readonly ROOT_DIR="/opt/app"
declare -r CONFIG_FILE="$ROOT_DIR/app.conf"
declare -i RETRIES=3
declare -a SERVERS=(alpha beta)
declare -A REGIONS=([eu]=1 [us]=2)
declare PLAIN_NAME
export APP_ENV=production
export PATH_EXTRA="/usr/local/bin"
export PLAIN_EXPORT
local_name=$(basename "$0")
count=0
items=()

alias ll='ls -l'
alias gs="git status"

log() {
    local level="$1"
    local message=$2
    echo "[$level] $message"
}

function usage {
    echo "usage: $0 [-h]"
}

function cleanup() {
    rm -rf "$TMP"
}

check_deps ()
{
    command -v git >/dev/null || return 1
    count=$((count + 1))
}

function build-image {
    docker build -t "$1" .
}

deploy:prod() {
    log info "deploying"
}

nested_outer() {
    inner_helper() {
        echo "inner"
    }
    inner_helper
}

if [ -n "$DEBUG" ]; then
    debug_log() {
        echo "debug: $*"
    }
fi

run_all() (
    cd "$ROOT_DIR" || exit 1
    log info "subshell function"
)

main() {
    local -r flag="$1"
    local i
    for i in "${SERVERS[@]}"; do
        log info "$i"
    done
    case "$flag" in
        -h) usage ;;
        *) check_deps ;;
    esac
    result=$(deploy:prod)
    VERSION="1.2.4"
}

main "$@"
