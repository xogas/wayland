.PHONY: test vet generate clean

generate:
	rm -rf protocols
	rm -f ./*_gen.go
	go run ./cmd/wayland-gen -root wayland-protocols -out .

test:
	go test -race ./...

vet:
	go vet ./...

clean:
	rm -rf bin/
