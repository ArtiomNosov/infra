.PHONY: build run test deps
build:
	mkdir -p bin
	CGO_ENABLED=0 go build -o bin/sandbox-code-executor .

run:
	go run . 

test:
	go test -race -v ./...

deps:
	go mod download
	go mod tidy
