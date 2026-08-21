.PHONY: conformance consumer-smoke format format-check interoperability spec-sync spec-update test tidy-check verify

conformance:
	./scripts/run-conformance.sh

consumer-smoke:
	./scripts/verify-consumer.sh

interoperability:
	./scripts/run-node-interoperability.sh

format:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

format-check:
	test -z "$$(gofmt -l .)"

test:
	go test -race -coverprofile=coverage.out ./...

spec-sync:
	./scripts/verify-spec-sync.sh

spec-update:
	./scripts/verify-spec-sync.sh --update

tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum

verify: spec-sync format-check tidy-check
	go vet ./...
	go test -race -coverprofile=coverage.out ./...
