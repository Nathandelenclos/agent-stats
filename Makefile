.PHONY: build test clean

build:
	mkdir -p bin
	go build -o bin/ .

test:
	go test ./...

clean:
	$(RM) bin/agent-stats bin/agent-stats.exe
