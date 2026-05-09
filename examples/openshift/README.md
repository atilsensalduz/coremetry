# Coremetry on OpenShift — vanilla manifests

These are flat OpenShift manifests for users who do not want to
install Helm. They deploy:

- ClickHouse (StatefulSet, 1 replica, 100 GiB PVC)
- Redis (Deployment, 1 replica, ephemeral)
- Coremetry (Deployment, 2 replicas, no persistence — state lives
  in ClickHouse)
- A `Route` exposing the Coremetry web UI on TLS-edge
- A `Service` with both HTTP (8088) and OTLP/gRPC (4317) ports

The chart in `charts/coremetry` remains the supported install path.
This example exists for clusters where Helm is not allowed, or for
operators who want a starting point they can `oc apply -f` and then
tweak by hand.

## Apply order

```bash
oc new-project coremetry        # or: oc project <existing-ns>

# Edit the placeholder secret values FIRST
$EDITOR examples/openshift/05-coremetry-secret.yaml

oc apply -f examples/openshift/
```

The files are numbered so a single `apply -f <dir>` works — kubectl
applies in lexical order and the implicit dependency chain is
namespace → RBAC → backing stores → coremetry → Route.

## OpenShift-specific notes

- **No `runAsUser` pin.** Restricted-v2 SCC assigns a random UID
  per namespace. The Coremetry binary is static and only writes to
  `/tmp` (an `emptyDir`), so any non-root UID works. If you see
  `unable to validate against any security context constraint`, run
  `oc adm policy add-scc-to-user nonroot-v2 -z coremetry -n <ns>`.
- **Route TLS-edge** terminates at the router. If you need
  re-encrypt or pass-through, change `route.spec.tls.termination`
  in `08-coremetry-route.yaml`.
- **ClickHouse** is a single-replica StatefulSet for dev/POC. For
  production scale-out (1B spans/day+) use a real CH operator
  (Altinity or ClickHouse-Inc) and point Coremetry at it via
  `COREMETRY_CH_ADDR` — the bundled StatefulSet here is a starter.

## "init not ready" — what it means

The Coremetry pod has a `wait-for-clickhouse` init container. The
log line `[init] not ready (attempt N/60)` is **not an error**; it
is the init container politely waiting for ClickHouse to become
reachable on `:9000`. It will retry every 2s for 2 minutes.

If you see this message persist past 60 attempts, check:

- `oc get pods -l app.kubernetes.io/component=clickhouse` — is the
  CH pod `Running` and `Ready`?
- `oc logs <ch-pod>` — did CH crash on startup (often: PVC
  permissions, or `restricted-v2` SCC blocking the CH image's
  baked-in UID)?
- The CH service name + port match `COREMETRY_CH_ADDR` in the
  ConfigMap (`04-coremetry-config.yaml`). Default:
  `coremetry-clickhouse:9000`.

## Pointing at an external ClickHouse

Skip `02-clickhouse.yaml`, then in `04-coremetry-config.yaml`:

```yaml
clickhouse:
  addr: "ch.databases.svc.cluster.local:9000"
  database: "coremetry_prod"      # any name — CREATE DATABASE on first run
  username: "coremetry"
```

Set the password in `05-coremetry-secret.yaml` under
`clickhouse-password`. The same env-var override
(`COREMETRY_CH_DATABASE`) lets you reuse one CH cluster across
dev/staging/prod environments by varying just the database name.
