.PHONY: gen regen clean-gen build test test-soak vet clean

clean-gen:
	@rm -rf protocol
	@for f in *_gen.go; do if [ -e "$$f" ]; then rm -f -- "$$f"; fi; done

gen: clean-gen
	go run ./cmd/wayland-scanner


build:
	go build -o bin/wayland-scanner ./cmd/wayland-scanner
	go build -o bin/wayland-info ./cmd/wayland-info

test:
	go test -race ./...

test-soak:
	go test -race -count=1 -run TestSoak -v .

vet:
	go vet ./...

clean:
	rm -rf bin/
