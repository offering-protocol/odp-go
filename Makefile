.PHONY: format format-check spec-sync test tidy-check verify

format:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

format-check:
	test -z "$$(gofmt -l .)"

test:
	go test -race -coverprofile=coverage.out ./...

spec-sync:
	./scripts/verify-spec-sync.sh

tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

verify: format-check tidy-check
	go vet ./...
	go test -race -coverprofile=coverage.out ./...
