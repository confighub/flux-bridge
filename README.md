# Flux Bridge

ConfigHub bridge to integrate with FluxCD using external artifacts.

## Installation

For these instructions we will use a local kind cluster. You can easily adapt them to a cluster of your own. First create the cluster.

```shell
kind create cluster --name flux-bridge-dev
```

Then install Flux v2.7.0 or greater.

```shell
flux install
```

We will be using the ExternalArtifact source controller which is behind a feature flag, so we need to enable that.

```shell
kubectl -n flux-system patch deployment kustomize-controller --type='json' -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--feature-gates=ExternalArtifact=true"}]'
```

Now create the Flux Bridge customer worker config in ConfigHub from the base config in this repository.

```shell
cub unit create flux-bridge-config https://raw.githubusercontent.com/confighub/flux-bridge/refs/heads/main/manifests/base.yaml
```

Then apply it to the cluster.

```shell
cub unit get flux-bridge-config --data-only | kubectl apply -f -
```

You don't strictly have to store the config in ConfigHub. You can just apply it directly to the cluster. But ConfigHub will let you easily make changes and track them over time. In this simple demo case, no customizations are necessary.

Next, create the Worker entity in ConfigHub.

```shell
cub worker create flux-bridge
```

Now, apply the Custom Worker secret resource to the kind cluster.

```shell
cub worker install flux-bridge --export-secret-only | kubectl apply -f -
```

The Flux Bridge worker config depends on this secret and it will not reconcile until the secret is applied. Now that it is applied the Flux Bridge Worker should boot successfully and connect to ConfigHub. You can verify that it has connected.

```shell
cub worker list
```

Which will show something like this.

```shell
NAME           CONDITION       SPACE          LAST-SEEN           
flux-bridge    Ready           default        2025-10-31 20:37:53
```

## Demo Deployment

We can use a common Helm chart to check if the installation was successful, for example the reloader helm chart. First render it into ConfigHub.

```shell
cub helm install --namespace reloader reloader reloader --repo https://stakater.github.io/stakater-charts
```

Then set its target to the target advertised by the flux-bridge for the local cluster.

```shell
cub unit set-target reloader flux-bridge-poc
```

Then apply.

```shell
cub unit apply reloader --wait=false
```

Now you can check status in ConfigHub.

```shell
cub unit list
```

Or also in Flux.

```shell
kubectl get kustomizations -A
```

## Development

The following dependencies are required to setup the local dev environment.

* cub
* kind
* kubectl
* flux
* docker

Start off with creating a worker for the bridge.

```shell
cub worker create flux-bridge
```

Running dev deploy will create a Kind cluster with Flux installed along with a local build of the flux-bridge. It will also get the worker secret from ConfigHub and create a secret in the cluster.

```shell
make dev-deploy
```

After the setup has run successfully the bridge should be deployed in the `confighub` namespace.

```shell
kubectl -n confighub get pods
```
