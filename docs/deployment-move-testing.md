# Testing a deployment move between two nodes (zmount + rootfs)

This is a dev runbook for the `zos.deployment.transfer` / `prepare` / `start` feature
(branch `feat/deployment-transfer`). It moves a zmachine (mycelium network + zmount)
from **node A** to **node B**, preserving zmount data *and* rootfs changes, via S3.

## The flow at a glance

```
node A                         S3 (MinIO)                 node B
  │  transfer (PUT) ──────────▶ zmount.raw                   │
  │  transfer (PUT) ──────────▶ rootfs.tar                   │
  │                                 │  prepare (GET) ◀────────┤  creates net+zmount+rootfs, pulls data
  │                                 └───────────────────────▶ │  (VM NOT booted yet)
  │                                            start ─────────┤  boots the VM on B
  ▼ cancel old contract                                       ▼ VM running on B
```

`transfer` pauses the source VM first, so the copy is consistent.

## Prerequisites

1. **Two zos-light nodes** A and B on the same farm/network, both reachable over RMB,
   both running a node image **built from this branch** (see "Build the node image").
2. **A twin + mnemonic** you control (the deployment owner), KYC-verified on the target
   chain env — `prepare` runs the same validation as `deploy` (twin match, signature,
   contract hash). Dev/QA net is fine.
3. **A MinIO (or S3) bucket** reachable from both nodes, plus the `mc` client to mint
   presigned URLs.
4. **A running source deployment on A**: a mycelium network + a zmount + a zmachine that
   mounts the zmount. Write a marker file into both the rootfs and the zmount so you can
   prove they survived the move.

## Build the node image

zoslight is already wired to this branch via a local replace (added during implementation):

```
# in the zoslight repo
grep 'zos_base =>' go.mod
# replace github.com/threefoldtech/zos_base => /home/afouda/code/github/threefoldtech/zosbase
go build ./...        # sanity: compiles against the branch
```

Build/flash the node image the way you normally do for zoslight (the replace makes the
image include these RMB endpoints). For a throwaway test you can also run the affected
daemons (api_gateway, provisiond, storaged) from source on a dev node.

> Before pushing the branch, drop the local `replace` and pin a real `zos_base`
> pseudo-version instead — the path replace only works on your machine.

## Mint presigned URLs (MinIO example)

```bash
mc alias set m http://MINIO:9000 ACCESS SECRET
mc mb m/zos-move

# uploads (used by node A on transfer) — PUT
ZMOUNT_PUT=$(mc share upload --expire 12h m/zos-move/zmount.raw | awk '/share:/{print $2}')
ROOTFS_PUT=$(mc share upload --expire 12h m/zos-move/rootfs.tar | awk '/share:/{print $2}')
# downloads (used by node B on prepare) — GET
ZMOUNT_GET=$(mc share download --expire 12h m/zos-move/zmount.raw | awk '/share:/{print $2}')
ROOTFS_GET=$(mc share download --expire 12h m/zos-move/rootfs.tar | awk '/share:/{print $2}')
```

The node streams a definite Content-Length, so plain presigned PUT/GET work (no chunked).

## Driver program (uses the client SDK added on this branch)

`client.NodeClient` gained `DeploymentTransfer`, `DeploymentPrepare`, `DeploymentStart`.
Workload names below (`"data"`, `"vm"`) are the `Name` fields in *your* deployment.

```go
package main

import (
	"context"
	"time"

	"github.com/threefoldtech/zos_base/client"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_sdk_go/rmb-sdk-go"
)

func main() {
	cl, err := rmb.Default() // or a peer-backed rmb.Client for your env
	if err != nil { panic(err) }

	const (
		twin       = 1234        // your twin id
		nodeATwin  = 1111        // node A twin id
		nodeBTwin  = 2222        // node B twin id
		contractA  = 100         // running contract on A
	)

	nodeA := client.NewNodeClient(nodeATwin, cl)
	nodeB := client.NewNodeClient(nodeBTwin, cl)
	ctx := context.Background()

	// 0) grab the running deployment from A (you'll reuse it for prepare on B)
	dl, err := nodeA.DeploymentGet(ctx, contractA)
	if err != nil { panic(err) }

	// 1) A: pause + upload zmount + rootfs to S3
	must(nodeA.DeploymentTransfer(ctx, contractA, []client.TransferItem{
		{WorkloadName: "data", URL: ZMOUNT_PUT},                       // zmount
		{WorkloadName: "vm",   URL: ROOTFS_PUT},                       // zmachine rootfs
	}))

	// 2) stage a contract on B with the SAME deployment hash (see note below),
	//    set dl.ContractID = <new B contract>, dl.Version = 0.
	dl.ContractID = 200
	dl.Version = 0

	// 3) B: prepare (provision net+zmount, skip VM) and pull data back
	must(nodeB.DeploymentPrepare(ctx, dl, []client.TransferItem{
		{WorkloadName: "data", URL: ZMOUNT_GET},
		{WorkloadName: "vm",   URL: ROOTFS_GET, Size: rootfsSize}, // rootfs volume size
	}))

	// 4) B: boot the VM
	time.Sleep(2 * time.Second)
	must(nodeB.DeploymentStart(ctx, dl.ContractID))

	// 5) A: cancel the old contract on chain to decommission the source
}

func must(err error) { if err != nil { panic(err) } }
```

### The on-chain contract for B (the manual bit)

`prepare` validates against a node contract for **B** whose `DeploymentHash` matches the
deployment. In production the grid/tfchain orchestration creates it; for a manual test,
create a node contract on B for the same hash (`dl.ChallengeHash()`) using your usual tool
(tfchain client / `tfcmd` / grid client), then use that contract id as `dl.ContractID`
in step 2. Same-contract migration (updating the existing contract's node id on chain) is
a later refinement — for now use a fresh contract on B.

## Verify

- `nodeB.DeploymentGet(ctx, 200)` → zmount + network `StateOk`, zmachine `StateOk` after start.
- On node B host: the zmount file exists at `/mnt/<pool>/vdisks/<twin>-200-data` and the
  rootfs volume at `/mnt/<pool>/rootfs:<twin>-200-vm/rw`.
- Console into the VM (mycelium IP is derived from the deployment seed, so it's the same as
  on A) and confirm your **marker files** in both the rootfs and the mounted zmount.

## Cleanup

Cancel the source contract on A (normal decommission path); the node tears down the paused
deployment. Delete the S3 objects.

## Known limitations (this phase)

- `transfer` pauses vCPUs but does not `fsync` the guest — tiny consistency risk; for a
  clean test, quiesce the app inside the VM before transferring.
- The rootfs volume quota on B is set at `prepare` time from the `Size` you pass; make it
  match the VM's rootfs size.
- Same-contract (in-place node reassignment) orchestration is not implemented — use a new
  contract on B as above.
```
