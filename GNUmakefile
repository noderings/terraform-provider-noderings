default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

# Formats examples and regenerates docs/ from the schema, templates/ and examples/.
generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

# Runs every test against the in-memory fake API. No account or network needed.
test:
	go test -v -cover -timeout=10m ./...

# Runs the same tests against the real NodeRings API. Needs NODERINGS_TOKEN,
# and NODERINGS_ACC_DOMAIN (a domain the account may create test zones under)
# for the DNS tests. Creates and deletes real resources.
testacc:
	TF_ACC=1 NODERINGS_ACC=1 go test -v -cover -timeout 120m ./...

# Reinstalls one VM through every distro and checks each guest over SSH.
# Needs the NODERINGS_ACC_VM_* variables; about an hour of one billed VM.
testacc-os:
	TF_ACC=1 NODERINGS_ACC=1 NODERINGS_ACC_OS_MATRIX=1 go test -v -timeout 180m ./internal/service/compute -run TestVirtualMachineGuestOSMatrix

.PHONY: fmt lint test testacc testacc-os build install generate
