.PHONY: test vet clean

test:
	go test -race ./...

vet:
	go vet ./...

clean:
	rm -rf bin/
