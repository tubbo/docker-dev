GOPATH?=~/go
GOX=$(GOPATH)/bin/gox

all:
	@go build ./cmd/docker-dev

install:
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
	@rm -rf docker-dev*

$(GOX):
	@go install github.com/mitchellh/gox

.PHONY: all release
