BINARY := sirius
CMD := ./cmd/sirius
BIN_DIR := bin

.PHONY: build run test lint fmt tidy clean spec-list spec-validate

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

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)

# --- specs -------------------------------------------------------------
spec-list:
	openspec list --specs

spec-validate:
	openspec validate --all
