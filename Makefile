.PHONY: generate fmt test vet check build run
generate:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate
fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')
test:
	go test ./...
vet:
	go vet ./...
check: generate fmt vet test
	git diff --exit-code
build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/noten ./cmd/web
run:
	mkdir -p tmp && DATABASE_PATH=./tmp/noten.db go run ./cmd/web
