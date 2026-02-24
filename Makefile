APP     := gal-cli
LDFLAGS := -s -w

.PHONY: build clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(APP) .

clean:
	rm -f $(APP)
