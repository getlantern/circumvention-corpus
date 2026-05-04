# Makefile for the circumvention-corpus.
#
# Two binaries: corpus-mcp (the MCP server, the canonical interface) and
# corpus-site (the static-site generator that produces dist/ for
# Cloudflare Pages / any static host).

GO ?= go

.PHONY: all test mcp site clean

all: test mcp site

test:
	$(GO) test ./...

mcp:
	$(GO) build -o corpus-mcp ./cmd/corpus-mcp/

site:
	$(GO) run ./cmd/corpus-site/ --corpus . --out dist

clean:
	rm -rf dist corpus-mcp
