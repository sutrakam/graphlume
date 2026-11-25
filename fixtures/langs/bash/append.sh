#!/usr/bin/env bash

hooks=('first')
hooks+=('second')
hooks+=("third" "fourth")

PATH_LIST="a"
PATH_LIST+=":b"

declare -a stages=(build)
stages+=(test)

counter=0
counter+=1

declare -i total=1
export EXTRA_FLAGS="-x"
EXTRA_FLAGS+=" -y"

register() {
  hooks+=('inside')
}
