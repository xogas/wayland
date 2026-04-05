.PHONY: gen build test vet clean

gen:
	go run ./cmd/wayland-scanner -batch wayland-protocols -o .

build:
	go build -o bin/wayland-scanner ./cmd/wayland-scanner
	go build -o bin/wayland-info ./cmd/wayland-info

test:
	go test -race ./...

vet:
	go vet ./...

clean:
	rm -rf bin/
