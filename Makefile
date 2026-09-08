BINARY  := tokentrace
VERSION ?= 0.2.0
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

# Release matrix, kept in sync with .goreleaser.yml (see release_test.go).
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: all build test vet fmt install demo cross snapshot clean

all: fmt vet test build

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/tokentrace

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

install:
	go install $(LDFLAGS) ./cmd/tokentrace

# demo seeds a throwaway record log and prints real stats output from it.
demo:
	@dir="$$(mktemp -d)" && \
	TOKENTRACE_DIR="$$dir" go run ./scripts/seeddemo && \
	echo '$$ tokentrace stats --by model' && \
	TOKENTRACE_DIR="$$dir" go run $(LDFLAGS) ./cmd/tokentrace stats --by model && \
	echo && echo '$$ tokentrace stats --by project --since 7d' && \
	TOKENTRACE_DIR="$$dir" go run $(LDFLAGS) ./cmd/tokentrace stats --by project --since 7d && \
	echo && echo '$$ tokentrace stats --by day' && \
	TOKENTRACE_DIR="$$dir" go run $(LDFLAGS) ./cmd/tokentrace stats --by day && \
	rm -rf "$$dir"

# cross builds every released platform. Proves the matrix compiles without
# goreleaser installed; CI's release job is the real artifact producer.
cross:
	@rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "  building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build $(LDFLAGS) -o dist/$(BINARY)_$${os}_$${arch}$$ext ./cmd/tokentrace || exit 1; \
	done

# snapshot runs the real release pipeline locally, without publishing.
snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf $(BINARY) dist
