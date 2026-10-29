.PHONY: all build run daemon sprites vet tidy clean dist

all: build

tidy:
	go mod tidy

build: tidy
	mkdir -p bin
	go build -o bin/relayd ./cmd/relayd
	go build -o bin/relay ./cmd/relay

# запуск интерфейса (relayd стартует сам, если он ещё не запущен)
run: build
	PATH="$$PWD/bin:$$PATH" ./bin/relay

daemon: build
	./bin/relayd

# пересобрать спрайты маскота из assets/sheets (нужны Pillow и numpy)
sprites:
	python3 tools/sprites/build.py

vet:
	go vet ./...

# кросс-сборка под Linux и Windows
dist: tidy
	mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -o dist/relay-linux-amd64/relay   ./cmd/relay
	GOOS=linux   GOARCH=amd64 go build -o dist/relay-linux-amd64/relayd  ./cmd/relayd
	GOOS=windows GOARCH=amd64 go build -o dist/relay-windows-amd64/relay.exe  ./cmd/relay
	GOOS=windows GOARCH=amd64 go build -o dist/relay-windows-amd64/relayd.exe ./cmd/relayd

clean:
	rm -rf bin dist
