# After a substitution reference with an empty replacement the grammar stops
# building rule and assignment nodes; the definitions below are read from the
# flat run of words that is left. See KNOWN_GAPS.md for what that cannot see.
CC = gcc
SRC = a.c b.c
BOARD ?= $(notdir $(BOARD_DIR:/=))
CFLAGS := -O2
LD ?= ld
CFLAGS += -Wl,--defsym=entry=1
LIBS += -lm
.PHONY: all
all: main
	echo done

define RUN
	echo run
endef

$(BUILD)/x.o: y
	cc
deploy: all
export OUT = out
stamp:: dep
SHELL := /bin/sh
