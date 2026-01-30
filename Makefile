# kup6s-pages Makefile

# Variablen
REGISTRY ?= ghcr.io/kleinundpartner
VERSION ?= latest
OPERATOR_IMG ?= $(REGISTRY)/kup6s-pages-operator:$(VERSION)
SYNCER_IMG ?= $(REGISTRY)/kup6s-pages-syncer:$(VERSION)

.PHONY: all build test deploy clean

all: build

## Build

build: build-operator build-syncer

build-operator:
	go build -o bin/operator ./cmd/operator

build-syncer:
	go build -o bin/syncer ./cmd/syncer

## Test

test:
	go test ./... -v

## Docker

docker-build: docker-build-operator docker-build-syncer

docker-build-operator:
	docker build -t $(OPERATOR_IMG) -f Dockerfile.operator .

docker-build-syncer:
	docker build -t $(SYNCER_IMG) -f Dockerfile.syncer .

docker-push: docker-push-operator docker-push-syncer

docker-push-operator:
	docker push $(OPERATOR_IMG)

docker-push-syncer:
	docker push $(SYNCER_IMG)

## Deploy

deploy: deploy-crd deploy-rbac deploy-operator deploy-nginx

deploy-crd:
	kubectl apply -f deploy/crd.yaml

deploy-rbac:
	kubectl apply -f deploy/rbac.yaml

deploy-operator:
	kubectl apply -f deploy/operator.yaml

deploy-nginx:
	kubectl apply -f deploy/nginx.yaml

undeploy:
	kubectl delete -f deploy/nginx.yaml --ignore-not-found
	kubectl delete -f deploy/operator.yaml --ignore-not-found
	kubectl delete -f deploy/rbac.yaml --ignore-not-found
	kubectl delete -f deploy/crd.yaml --ignore-not-found

## Development

run-operator:
	go run ./cmd/operator --pages-domain=pages.localhost --cluster-issuer=selfsigned

run-syncer:
	go run ./cmd/syncer --sites-root=./tmp/sites --sync-interval=30s

## Utilities

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin/
	rm -rf tmp/
