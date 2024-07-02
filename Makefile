test:
	@go test ./...

build:
	@go build

run: build
	./bitflood $(FILE)

peer:
	@go run cmd/peerconnect/main.go

verify_parts:
	@go run cmd/verify_parts/main.go $(FILE)