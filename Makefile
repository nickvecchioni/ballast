MODULE := github.com/nickvecchioni/ballast
BINDIR := bin
GOFLAGS := -trimpath
LDFLAGS := -s -w

BINARIES := collector engine controller kubectl-cost sidecar

.PHONY: all build clean test lint fmt vet $(BINARIES)

all: build

build: $(BINARIES)

collector:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/ballast-collector ./cmd/collector

engine:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/ballast-engine ./cmd/engine

controller:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/ballast-controller ./cmd/controller

kubectl-cost:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/kubectl-cost ./cmd/kubectl-cost

sidecar:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINDIR)/ballast-sidecar ./cmd/sidecar

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
	docker build -f Dockerfile.collector -t ballast-collector:latest .

docker-engine:
	docker build -f Dockerfile.engine -t ballast-engine:latest .

docker-controller:
	docker build -f Dockerfile.controller -t ballast-controller:latest .

helm-template:
	helm template ballast deploy/helm/ballast

helm-install:
	helm install ballast deploy/helm/ballast
