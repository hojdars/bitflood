test:
	@go test ./...

build:
	@go build

run: build
	./bitflood $(FILE)

peer:
	@go run cmd/peerconnect/main.go