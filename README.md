# SUSS - System Update Support Service

This service implements support routines to run updates against hosts that act
as nodes of a Kubernetes cluster.

It do not implement the update itself, or depends on a specific management 
platform or task executor. It is supposed to be called from the update script, 
which do not need to interact with the kubernetes API itself.

Benefits:

* Allow to ensure that only one host is unavailable on update, even if the update
script is started at the same schedule on all hosts
* Allow to migrate critical workload away from hosts that are being updated

## Installation

There is a helm chart contained within the repo at `/chart` that deploys a DaemonSet
in the cluster. Because the chart uses host networking, it is not compatible with 
the [Baseline](https://kubernetes.io/docs/concepts/security/pod-security-standards/) 
Pod Security Standard. It needs to run in a namespace with the `pod-security.kubernetes.io/enforce: privileged` 
annotation.

Because SUSS is to be called with http from a script running directly on the host, 
the SUSS pod uses host networking. This allows it to be called by `curl localhost:9993`.
The port 9993 is the default port and can be changed by arguments.

There is an example of an update script here: [./update-example.sh](update-example.sh)

## Commands

SUSS implements the following commands that are supposed to be called from the 
update script. Every command is executed as the URL path, and do not need any arguments,
like `curl localhost:9993/version`.

* Commands are implemented to be cancelable and resumable at any time.
* Commands never timeout internally, it's up to the script if needed
* Commands return HTTP 200 if successful
* Commands will not return any data. To check the ongoing operation check the log,
or use the `/logstream` endpoint which uses `Tranfer-Encoding: chunked` with all 
logs from the commands. See [./update-example.sh](update-example.sh) for usage.

### /synchronize

Synchronize acquires a Lease for the current node. It returns if it successfully 
acquired the Lease. So only one Node will pass beyond this command.

### /teardown

Teardown cordons the node so that no new pods will be scheduled on it. 
It then terminates critical pods running on the current node. It uses API based 
eviction to allow cluster users to plan for this, e.g. with PodDistruptionBudget. 
It returns when all critical pods have exited. To check for critical pods without 
terminating them, you can use the `/criticalpods` endpoint, which just lists critical 
pods.

Critical Pods are pods labeled with `suss.world-direct.at/critical=true`. Pods 
may also be set explicitly to not critical with `suss.world-direct.at/critical=false`.

Based on the start arguments, suss also implements further critical workload heuristics:

* -considerStatefulSetCritical argument considers all pods part of an StatefulSet critical
* -considerSoleReplicasCritical argument considers all pods that are part of a
ReplicaSet if only on replica is running `status.replicas==1`

### /release

Releases the Lease acquired by `/synchronize`. It also set the `suss.world-direct.at/lastrelease` 
informative label on the node with the current UNIX timestamp value.

### /releasedelayed

The `/releasedelayed` command is supposed to be called from the update script 
if a reboot is required. The command only sets the `suss.world-direct.at/delayedrelease` 
label on the node.

After the node is restarted by the update script, the suss pod will also be started.
On startup it checks for the `suss.world-direct.at/delayedrelease` label to be 
present. And it so, it executes the `/release` command internally, so so releasing
the Lease.

This ensures successful restart of the host, and an operative kubernetes node.

## Endpoints

This endpoints are available which are not commands:

### /logstream

This command uses `Content-Type: text/plain` and `Transfer-Encoding: chunked` and
will return all log messages in real time. The server will never complete this 
request. It's up to the client to cancel it when no longer needed. Normally when 
the script is done.

### /version

Returns the release version of suss.

### /healthz

Returns OK, should be used for healthchecks.

## Arguments

*  -bindAddress: address to bind http socket (default "localhost:9993")
*  -kubeconfig: kubeconfig to use, if not set InClusterConfig is used, can be set by KUBECONFIG envvar.
This is normally only needed for debugging. On cluster deployment InClusterConfig is normally used.
*  -leasenamespace: the namespace for the lease, can be set by the NAMESPACE envvar
*  -nodename: the name of the node running the service. Can be set by NODENAME envvar
*  -considerSoleReplicasCritical: All pods part of a replicaset with only one replica are critical
*  -considerStatefulSetCritical: All pods part of a statefulset are critical

# How to release

suss uses [goreleaser](https://github.com/goreleaser/) to create releases.
It triggers if a tag matching `v*` is pushed.

It will build the binaries, and will push the image to `r.world-direct.at/library/suss`,
and the helm chart to `r.world-direct.at/library/helm-charts` using the Version 
as Image-Tag and Chart-Version.