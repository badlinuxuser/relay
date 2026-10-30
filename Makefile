.PHONY: all build run install uninstall sprites vet tidy clean dist

all: build

tidy:
	go mod tidy

build: tidy
	mkdir -p bin
	go build -o bin/relay ./cmd/relay

# запуск без установки
run: build
	./bin/relay

# собрать и добавить в PATH (Linux: ~/.local/bin, Windows: %LOCALAPPDATA%\\Programs\\relay)
install: build
	./bin/relay install

uninstall:
	./bin/relay uninstall

# пересобрать спрайты маскота из assets/sheets (нужны Pillow и numpy)
sprites:
	python3 tools/sprites/build.py

vet:
	go vet ./...

# кросс-сборка под Linux и Windows
dist: tidy
	mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -o dist/relay-linux-amd64/relay ./cmd/relay
	GOOS=windows GOARCH=amd64 go build -o dist/relay-windows-amd64/relay.exe ./cmd/relay

clean:
	rm -rf bin dist
