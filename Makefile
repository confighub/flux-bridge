IMG_NAME ?= ghcr.io/confighubai/flux-bridge
DEV_CLUSTER_NAME ?= "flux-bridge-dev"

test-unit:
	go test -timeout 15s ./...

build:
	CGO_ENABLED=0 GOOS=linux go build -o ./bin/flux-bridge .

docker-build: build
	docker build -t ${IMG_NAME}:dev .

dev-deploy: docker-build
	@if ! kind get clusters | grep -q "^${DEV_CLUSTER_NAME}$$"; then \
		kind create cluster --name ${DEV_CLUSTER_NAME}; \
		flux install; \
		kubectl -n flux-system patch deployment kustomize-controller --type='json' -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--feature-gates=ExternalArtifact=true"}]'; \
		kubectl create namespace confighub; \
		cub worker create flux-bridge --allow-exists; \
		cub unit create --allow-exists flux-bridge ./manifests/dev.yaml; \
		cub worker install --export-secret-only flux-bridge | kubectl -n confighub apply -f -; \
		cub unit get flux-bridge --data-only | kubectl -n confighub apply -f -; \
	fi
	kind load docker-image --name ${DEV_CLUSTER_NAME} ${IMG_NAME}:dev
	kubectl -n confighub rollout restart deployment flux-bridge
