.PHONY: build test test-e2e vet fmt check

# Build all packages
build:
	go build ./...

# Run all tests
test:
	go test ./...

# Run e2e tests with verbose output
test-e2e:
	go test -v ./internal/test/...

# Run go vet
vet:
	go vet ./...

# Format code
fmt:
	go fmt ./...

# Full check: vet, fmt, build, test
check: vet fmt build test
