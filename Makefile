GOPATH?=~/go
GOX=$(GOPATH)/bin/gox

docker-dev:
	@go build ./cmd/docker-dev

install: docker-dev
	@go install ./cmd/docker-dev

release: $(GOX)
	$(GOX) -os="darwin linux" -arch="amd64" -ldflags "-X main.Version=$(VERSION)" ./cmd/docker-dev
	mv docker-dev_linux_amd64 docker-dev
	tar czvf docker-dev-$(VERSION)-linux-amd64.tar.gz ./docker-dev
	mv docker-dev_darwin_amd64 docker-dev
	zip docker-dev-$(VERSION)-darwin-amd64.zip ./docker-dev

test:
	@go test -v ./...

clean:
	@rm -rf docker-dev* tags

$(GOX):
	@go install github.com/mitchellh/gox

restart: stop start

stop:
	@launchctl unload ~/Library/LaunchAgents/io.github.tubbo.docker-dev.plist

start:
	@launchctl load ~/Library/LaunchAgents/io.github.tubbo.docker-dev.plist

tags:
	@ctags -R .

.PHONY: all install release test clean restart stop start
