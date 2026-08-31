BINARY  := tokentrace
VERSION ?= 0.1.0
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: all build test vet fmt install demo clean

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

clean:
	rm -f $(BINARY)
