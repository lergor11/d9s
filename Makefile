BINARY := d9s

.PHONY: build test vet lint run clean

build:
	go build -o $(BINARY) ./cmd/d9s

test:
	go test ./...

vet:
	go vet ./...

lint: vet
	gofmt -l . | (! grep .)

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY)
