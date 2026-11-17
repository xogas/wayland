.PHONY: generate vet lint test integration_test clean

generate:
	rm -rf protocols
	rm -f ./*_gen.go
	go run ./cmd/wayland-gen -root wayland-protocols -out .

vet:
	go vet ./...
	go vet -tags=integration ./...

lint:
	golangci-lint run ./...

test:
	go test -race ./...

integration_test:
	go test -race -count=1 -tags=integration ./...

clean:
	rm -rf bin/
