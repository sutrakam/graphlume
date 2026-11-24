# Variables, in each assignment operator.
CC = gcc
CFLAGS := -O2 -Wall
LDFLAGS ?= -lm
CFLAGS += -g
CFLAGS += -fPIC
export GOFLAGS = -trimpath
override PREFIX = /usr/local
BUILD_DIR=build
VERSION != git describe --tags
SRCS = $(wildcard src/*.c)
OBJS = $(SRCS:.c=.o)

.PHONY: all clean install test

all: app lib.a

app: $(OBJS)
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

lib.a: $(OBJS)
	ar rcs $@ $^

# Several targets sharing one rule.
test unit-test: app
	./app --self-test
	@echo "running tests with $(CC)"

clean:
	rm -rf $(BUILD_DIR) app lib.a

install: app
	install -m 755 app $(PREFIX)/bin/app

# Pattern and suffix rules are not definitions.
%.o: %.c
	$(CC) $(CFLAGS) -c -o $@ $<

.c.o:
	$(CC) -c $<

$(BUILD_DIR)/%.txt: %.in
	cp $< $@

# A target with a recipe only; the prerequisite names are not definitions.
release: clean all test
	tar czf app-$(VERSION).tgz app

# Multi-line macro.
define COMPILE_template
$(1).o: $(1).c
	$(CC) -c $$<
endef

define greet
	@echo hello $(1)
endef

ifeq ($(CC),gcc)
GCC_ONLY = yes
endif

ifdef DEBUG
CFLAGS += -DDEBUG
debug-info:
	@echo debugging
endif

$(foreach prog,alpha beta,$(eval $(call COMPILE_template,$(prog))))

include config.mk
-include local.mk

vpath %.c src
