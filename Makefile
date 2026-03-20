MODULE := github.com/nickvecchioni/infracost
BINDIR := bin
GOFLAGS := -trimpath
LDFLAGS := -s -w

BINARIES := collector engine controller kubectl-cost sidecar

.PHONY: all build clean test lint fmt vet $(BINARIES)

all: build

build: $(BINARIES)

collector:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/infracost-collector ./cmd/collector

engine:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/infracost-engine ./cmd/engine

controller:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/infracost-controller ./cmd/controller

kubectl-cost:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/kubectl-cost ./cmd/kubectl-cost

sidecar:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/infracost-sidecar ./cmd/sidecar

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .

vet:
	go vet ./...

clean:
	rm -rf $(BINDIR)

docker-collector:
	docker build -f Dockerfile.collector -t infracost-collector:latest .

docker-engine:
	docker build -f Dockerfile.engine -t infracost-engine:latest .

docker-controller:
	docker build -f Dockerfile.controller -t infracost-controller:latest .

helm-template:
	helm template infracost deploy/helm/infracost

helm-install:
	helm install infracost deploy/helm/infracost
