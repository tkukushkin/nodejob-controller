# NodeJob

A Kubernetes controller with a single CRD, `NodeJob`. It runs a Job on every matching node and
re-runs it when the job template changes.

Why not a DaemonSet: its pod permanently occupies one unit of the node's pod limit for work that
takes seconds. The scenario is a node fix the cloud provider can't apply — a sysctl, a kernel
module, a one-off script.

`NodeJob` keeps exactly one Job per (matching node, current run hash) — the hash of the job
template combined with whatever `trigger` counts as one occasion. Ordering relative to other
workloads is best-effort; the controller never gates, cordons, or touches the node.

**Your command must be idempotent.** `backoffLimit` retries it within a run, and any change to the
run hash starts a fresh one.

## Example

```yaml
apiVersion: tkukushkin.github.io/v1alpha1
kind: NodeJob
metadata:
  name: fix-sysctl
  namespace: default
spec:
  trigger: OncePerBoot
  jobTemplate:
    spec:
      backoffLimit: 2
      activeDeadlineSeconds: 600
      template:
        spec:
          restartPolicy: Never
          hostNetwork: true
          nodeSelector:
            node-role.kubernetes.io/worker: ""
          containers:
            - name: fix-sysctl
              image: busybox:1.36
              securityContext:
                privileged: true
              command: ["sh", "-c", "sysctl -w net.core.somaxconn=4096"]
              resources:
                requests:
                  cpu: 10m
                  memory: 16Mi
```

## Reference

The spec has two fields.

### `trigger`

What counts as one occasion to run the task. A `Node` object survives a reboot — kubelet
re-registers under the same name — so the two values are genuinely different:

- **`OncePerNode`** (default) — for a fix written to disk: a systemd unit, a file under `/etc`, a
  `sysctl.d` drop-in. The run hash folds in the Node's UID rather than its name, so a machine
  reimaged under the same name gets the task again.
- **`OncePerBoot`** — for a fix that lives in memory only: a live `sysctl` write, a loaded kernel
  module, an `iptables` rule. The run hash also folds in `status.nodeInfo.bootID`, so every boot
  re-runs. A kubelet restart doesn't change `bootID` and correctly doesn't re-run; a node that
  hasn't published a `bootID` yet is skipped.

### `jobTemplate`

A `batchv1.JobTemplateSpec`, the same shape `CronJob` takes. It becomes the Job unchanged — the
controller only adds `nodeName`, an owner reference, its own labels, and the tolerations below.

Nodes are picked from the pod template, exactly the way a DaemonSet picks them:
`spec.template.spec.nodeSelector` and `.affinity.nodeAffinity`, matched with the same library the
scheduler uses. **Setting neither targets every node in the cluster, including the control plane.**

`nodeSelector`, `nodeAffinity` and `tolerations` take no part in the run hash, so retargeting
doesn't disturb a node that stays in the set. A node that leaves it loses its Job — and the Job is
the only record that the node was done, so bringing it back later re-runs the task.

#### Rejected fields

- `ttlSecondsAfterFinished` — the finished Job *is* the controller's memory that the node is done,
  so a TTL would erase it and the task would run again in a loop.
- `podAffinity`, `podAntiAffinity`, `nodeAffinity.preferredDuringSchedulingIgnoredDuringExecution` —
  the pod is pinned with `nodeName` and never reaches the scheduler.

#### Tolerations

Because the pod bypasses the scheduler, the controller adds the node-condition tolerations a
DaemonSet gets — `not-ready`, `unreachable`, `disk-pressure`, `memory-pressure`, `pid-pressure`,
`unschedulable`, plus `network-unavailable` under `hostNetwork`. Without them a node fix would
refuse to run on the broken and cordoned nodes that most need it. They go into the created Job, not
into your `jobTemplate`, and are the only thing the controller adds to your pod.

## Operating

`NodeJob` has no `.status` and emits no Events; a rollout is fully visible in the Jobs it creates.

```sh
kubectl get jobs -l tkukushkin.github.io/nodejob=<name>
```

Beyond that, read the controller's logs.

- **A failed Job is not retried.** The node already has a Job for the current run, so nothing is
  recreated. Recovery means editing `jobTemplate`, which re-runs the task everywhere.
- **Deleting a Job by hand** doesn't mark anything done; a fresh one appears on the next reconcile.
- **Deleting the `NodeJob`** cascades to every Job it owns via owner references, killing in-flight
  pods. No finalizer needed.

## Installation

The Helm chart is the only supported way to install this. It carries the CRD, the RBAC, and a
Deployment with leader election enabled.

```sh
helm upgrade --install nodejob-controller \
  oci://ghcr.io/tkukushkin/charts/nodejob-controller --version <version> \
  --namespace nodejob-controller --create-namespace
```

The chart and the image are published together from a GitHub release: the tag `vX.Y.Z` becomes both
the chart version and the image tag, so `--version 1.2.3` always gets the matching controller.

Customization is deliberately thin — `image`, `imagePullSecrets`, `resources`, `replicaCount`, and
`nodeSelector`, `tolerations`, `affinity`, `topologySpreadConstraints` for the controller pod.
Raising `replicaCount` buys faster failover, not throughput: leader election keeps one replica
reconciling while the rest hold the lease. That is why spreading them matters — two standbys on the
node that just died are worth nothing; `values.yaml` has commented examples.
