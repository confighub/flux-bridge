# Flux Bridge

ConfigHub bridge to integrate with FluxCD using external artifacts.

## Usage

Check [ConfigHub Docs](https://docs.confighub.com/get-started/examples/flux-bridge/) to learn how to use this bridge.

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
