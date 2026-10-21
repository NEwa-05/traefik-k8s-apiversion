# traefik-k8s-apiversion

This repository contains a tool to analyse the syntax from your v2 traefik deployment and help you validate which k8s traefik object needs to be reapply with the new api version

## Old method

This script was exporting every containous api object and recreate them with the traefik.io version without remove the old one.

```shell
#!/bin/bash

set -e

declare -a RESOURCES 
declare -a OBJECTS

for RES in $(kubectl api-resources | grep traefik.containo.us | awk '{print $1}'); do
  RESOURCES+=($RES)
  if [ $(kubectl get $RES.traefik.containo.us  -A --ignore-not-found=true| wc -l) != 0 ]; then
    for OBJ in $(kubectl get $RES.traefik.containo.us -A --ignore-not-found=true -o custom-columns=:metadata.namespace,:metadata.name | tail -n +2); do
      OBJECTS+=($OBJ)
    done
  fi
done

for ING in "${OBJECTS[@]}"; do
  NAMESPACE=$(echo $INT+G | cut -d'/' -f1)
  INGNAME=$(echo $ING | cut -d'/' -f2)
  for RES in "${RESOURCES[@]}"; do
    if kubectl get $RES -n $NAMESPACE $INGNAME --ignore-not-found=true; then
      echo "Migrating $RES/$INGNAME in namespace $NAMESPACE to traefik.io/v1alpha1"
      migrate_resource $res $NAMESPACE $INGNAME
    fi
  done
done

kubectl get $i -n $NAMESPACE $INGNAME -o json| jq '.apiVersion = "traefik.io/v1alpha1"| del (.metadata.annotations."kubectl.kubernetes.io/last-applied-configuration",.metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp, .metadata.generation)' | kubectl apply -f -
```

## New method

`tkav` is a Go tool that replaces the script above. It:

1. discovers every resource served under the `traefik.containo.us` API group,
2. lists their objects page by page (`-page-size`, 100 by default), so no single API response gets large,
3. compares each object with its `traefik.io` counterpart, if any,
4. exports the `traefik.io/v1alpha1` version of each object to **its own YAML file**.

Every `apiVersion` set to `traefik.containo.us/...` is rewritten to `traefik.io/...`. The fields set by the API server (`uid`, `resourceVersion`, `generation`, `creationTimestamp`, `managedFields`, `status`) and the `kubectl.kubernetes.io/last-applied-configuration` annotation are removed. Owner references pointing to a `traefik.containo.us` object are dropped with a warning: they hold the UID of the old object, so the garbage collector would delete the new object once the old one is removed.

The old objects are never modified or deleted.

### Why one file per object

etcd rejects requests above 1.5 MiB by default, and a client-side `kubectl apply` stores the whole object in the `last-applied-configuration` annotation, which is limited to 256 KiB. Writing one file per object means each object is sent in its own request. The tool warns about any object too large for a client-side apply, and recommends `kubectl apply --server-side`, which does not use that annotation.

### Install

```shell
go install github.com/NEwa-05/traefik-k8s-apiversion/cmd/tkav@latest
```

### Usage

The tool uses your kubeconfig (`$KUBECONFIG` or `~/.kube/config`). Every command accepts `-kubeconfig`, `-context`, `-namespace` (or `-n`) and `-page-size`.

List the `traefik.containo.us` objects and the state of their `traefik.io` version:

```shell
tkav list
```

```
KIND              NAMESPACE   NAME            TRAEFIK.IO STATUS
IngressRoute      apps        route-1         missing
IngressRouteTCP   other       db              missing
Middleware        apps        auth            up-to-date
TLSOption         other       modern          out-of-sync

4 traefik.containo.us object(s): 2 missing, 1 out-of-sync, 1 up-to-date, 0 without traefik.io CRD
```

| Status        | Meaning                                                                 |
|---------------|-------------------------------------------------------------------------|
| `missing`     | No `traefik.io` object with this name exists yet                        |
| `out-of-sync` | A `traefik.io` object exists, with a different `spec`                   |
| `up-to-date`  | A `traefik.io` object exists, with the same `spec`                      |
| `crd-missing` | The `traefik.io` CRD for this kind is not installed (Traefik v2.10+ CRDs needed) |

Export the `traefik.io` version of the objects that still need it:

```shell
tkav export -output-dir traefik-io-manifests
```

Manifests are written to `<output-dir>/<namespace>/<kind>-<name>.yaml`. Up-to-date objects are skipped unless `-all` is set. The tool refuses to write into a non-empty directory unless `-overwrite` is set.

Review the manifests, then apply them before upgrading to Traefik v3:

```shell
kubectl apply --server-side -R -f traefik-io-manifests
```

If another field manager (Helm, Argo CD, ...) already owns fields of an `out-of-sync` object, server-side apply reports a conflict. Add `--force-conflicts` once you have checked that the exported version is the one to keep.

Prebuilt binaries for Linux, macOS and Windows (amd64 and arm64) are attached to each [GitHub release](https://github.com/NEwa-05/traefik-k8s-apiversion/releases), with a `checksums.txt` file.

### Development

The [Makefile](Makefile) needs Go 1.27.1 and [golangci-lint](https://golangci-lint.run/) v2 (configured in [.github/.golangci.yml](.github/.golangci.yml)):

| Target           | Description                                                              |
|------------------|--------------------------------------------------------------------------|
| `make`           | Lint, test and build `tkav` for the current platform                     |
| `make build`     | Build `tkav` for the current platform                                    |
| `make build-all` | Build for Linux, macOS and Windows (amd64, arm64) into `dist/`, with checksums |
| `make test`      | Run the tests with the race detector                                     |
| `make lint`      | Run golangci-lint                                                        |
| `make fmt`       | Format the code (gofmt, goimports)                                       |
| `make clean`     | Remove build outputs                                                     |

The version embedded in the binary (`tkav version`) comes from `git describe`, and can be overridden with `make build VERSION=...`.

### Release

Pushing a tag starting with `v` runs the [release workflow](.github/workflows/release.yml): it lints, tests, builds the binaries for every platform and publishes them in a GitHub release named after the tag.

```shell
git tag v1.0.0
git push origin v1.0.0
```

## Credits

`tkav`, its tests, Makefile, lint configuration and release workflow were written with [Claude Code](https://claude.com/claude-code) (Claude Opus 5.5, by Anthropic), based on the original shell script above.
