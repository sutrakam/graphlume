.DEFAULT_GOAL := help
.SUFFIXES: .c .o
.SECONDARY:

GO ?= go
BIN_DIR ::= bin
PKGS = ./...
EMPTY =
SHELL := /bin/bash
MAKEFLAGS += --no-print-directory
EXTRA_TARGETS += docs

NAME = tool
$(NAME): main.go
	$(GO) build -o $(BIN_DIR)/$@ .

help:
	@grep -E '^[a-zA-Z_-]+:' $(MAKEFILE_LIST)

fmt vet::
	$(GO) $@ $(PKGS)

%.pb.go: %.proto
	protoc --go_out=. $<

lint: ; golangci-lint run

docs-html: docs
	# a comment line inside a recipe is not a definition
	@echo building

define run-tool
	$(GO) run ./cmd/$(1)
endef

ifneq ($(wildcard .env),)
include .env
endif

check-%:
	@echo checking $*

$(BIN_DIR)/%.gz: $(BIN_DIR)/%
	gzip -k $<

# No space before the operator, and a target-specific assignment.
LEVEL?=3
QUIET:=1
TAGS+=extra
tidy: GOFLAGS = -mod=vendor
