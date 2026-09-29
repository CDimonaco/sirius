BINARY := sirius
CMD := ./cmd/sirius
BIN_DIR := bin

.PHONY: build run test lint fmt fmt-check tidy tidy-check clean spec-list spec-validate

build:
	go build -o $(BIN_DIR)/$(BINARY) $(CMD)

run:
	go run $(CMD)

test:
	go test -race ./...

lint:
	golangci-lint run --timeout 5m

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "these files are not gofmt'd:"; echo "$$out"; exit 1; fi

tidy:
	go mod tidy

tidy-check: tidy
	@git diff --exit-code go.mod go.sum

clean:
	rm -rf $(BIN_DIR)

# --- specs -------------------------------------------------------------
spec-list:
	openspec list --specs

spec-validate:
	openspec validate --all
