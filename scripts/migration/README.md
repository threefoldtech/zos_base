# zosmigration

One command to move a zos zmachine (VM + zmount + its network) between nodes. You
give it **S3 credentials**, the **source VM contract id**, and the **source + target
node ids** — it does the rest.

Builds against this repo via `replace github.com/threefoldtech/zos_base => ../..`.

## What it automates

1. Creates the S3 bucket (if missing).
2. Fetches the source VM deployment over RMB and **auto-detects** the zmount(s), the
   zmachine, and the rootfs size (`ZMachine.Size`) — no workload names to type.
3. **Transfer**: presigns PUT URLs and calls `zos.deployment.transfer` on the source
   node (pauses the VM, uploads zmount disk(s) + rootfs to S3), then waits for the
   objects to land in the bucket.
4. **Auto-detects** the network the VM attaches to (from its zmachine interface) and
   finds its contract via `DeploymentList` on the source — then fetches + deploys that
   network on the target. (Pass `-src-net-contract` to override, or skip auto-detect.)
5. **Prepare**: presigns GET URLs, creates the target contract, calls
   `zos.deployment.prepare` on the target (provisions network+zmount and pulls the
   data) — without booting the VM.
6. Prints the follow-up `-start-contract` command.

It re-signs deployments with the owner mnemonic (DeploymentGet re-marshals the `Env`
map, shifting the challenge hash) and copies the source contract's `deployment_data`
so the dashboard shows the workload type.

## Usage

```bash
go build -o zosmigration .

# full migration: transfer -> network -> prepare
MNEMONIC="word word ..." ./zosmigration \
  -s3-endpoint https://gateway.storjshare.io \
  -s3-access  <ACCESS> \
  -s3-secret  <SECRET> \
  -src-node-id 396 -src-contract 270333 \
  -dst-node-id 371

# then, once the target logs "all downloads complete", boot the VM:
MNEMONIC="word word ..." ./zosmigration -dst-node-id 371 -start-contract <printed id>
```

- `-src-net-contract` is **optional** — the network is auto-detected from the VM
  contract. Pass it only to override; if the network already exists on the target and
  you want to skip re-deploying it, it will still be (idempotently) re-deployed.
- S3 creds can also come from `S3_ACCESS` / `S3_SECRET` env vars.
- Defaults: bucket `zos-migration`, region `global` (Storj), devnet substrate/relay.
  Override `-substrate` / `-relay` for test/main.
- `-upload-timeout` (default 2h) bounds the wait for slow uplinks.

## Start / waiting

By default the tool prepares with **auto-start**: the node boots the VM once its
downloads finish, and the tool **waits until the zmachine reaches `Ok`** before it
exits — so one command runs start-to-finish and stops exactly when the VM is up on
the new node.

- `-stage-only` prepares without auto-start/wait and prints a `-start-contract`
  command to boot it later (fire-and-exit).
- `-start-contract <id>` boots an already-prepared deployment on `-dst-node-id` and
  exits (used by `-stage-only`, or to retry a boot).

Auto-start runs in the **target node's api-gateway**, so both nodes must run a zos
build that carries the `start` flag on `zos.deployment.prepare`.

## Prerequisites

- Both nodes run a zos build with the transfer/prepare/start support.
- The owner **mnemonic** (the deployment's `twin_id`); it signs the RMB calls, the
  on-chain contract creation, and the deployments.
