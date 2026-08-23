// Command zosmigration automates moving a zos zmachine deployment (VM + its zmount,
// plus the separate network deployment it references) from one node to another.
//
// You give it: S3 credentials, the source VM contract id, and the source + target
// node ids. It then does the whole flow:
//
//  1. creates the S3 bucket (if missing)
//  2. fetches the source VM deployment over RMB and auto-detects its zmount(s),
//     the zmachine, and the rootfs size (ZMachine.Size)
//  3. TRANSFER: presigns PUT URLs and calls zos.deployment.transfer on the source
//     node (pauses the VM, uploads the zmount disk(s) + the VM rootfs to S3), then
//     waits for the objects to appear in the bucket
//  4. (optional) fetches + deploys the referenced network deployment on the target
//  5. PREPARE: presigns GET URLs, creates the target contract, and calls
//     zos.deployment.prepare on the target node (provisions network+zmount and pulls
//     the data), without booting the VM
//  6. prints the follow-up `-start-contract` command to boot the VM once downloads
//     finish (downloads run in the node background with no client-visible signal)
//
// Deployments are re-signed with the owner mnemonic (DeploymentGet re-marshals the
// Env map, shifting the challenge hash), and the source contract's deployment_data
// is copied so the dashboard shows the workload type. ContractID and signatures are
// excluded from the challenge hash, so this stays consistent.
//
// Example:
//
//	MNEMONIC="word word ..." go run . \
//	  -s3-endpoint https://gateway.storjshare.io \
//	  -s3-access <ACCESS> -s3-secret <SECRET> \
//	  -src-node-id 396 -src-contract 270333 -src-net-contract 270332 \
//	  -dst-node-id 371
//
// then, once "all downloads complete" on the target:
//
//	MNEMONIC="..." go run . -dst-node-id 371 -start-contract <printed id>
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	substrate "github.com/threefoldtech/tfchain/clients/tfchain-client-go"
	"github.com/threefoldtech/zos_base/client"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
	"github.com/threefoldtech/zos_sdk_go/rmb-sdk-go/peer"
)

type config struct {
	// s3
	s3Endpoint, s3Access, s3Secret, s3Bucket, s3Region string
	s3Secure                                           bool
	presignExpiry                                      time.Duration
	// rmb / chain
	mnemonic, keyType, substrateURL, relayURL string
	timeout, uploadTimeout                    time.Duration
	// migration
	srcNodeID, srcNodeTwin, dstNodeID, dstNodeTwin uint32
	srcContract, srcNetContract                    uint64
	// start-only
	startContract uint64
	// behavior
	stageOnly bool
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run() error {
	cfg, err := parseFlags()
	if err != nil {
		return err
	}

	ctx := context.Background()
	mgr := substrate.NewManager(cfg.substrateURL)
	rpc, err := peer.NewRpcClient(ctx, cfg.mnemonic, mgr,
		peer.WithKeyType(cfg.keyType), peer.WithRelay(cfg.relayURL), peer.WithSession("zosmigration"))
	if err != nil {
		return fmt.Errorf("create rmb client: %w", err)
	}

	// start-only mode: boot a prepared deployment
	if cfg.startContract != 0 {
		twin, err := resolveTwin(mgr, cfg.dstNodeTwin, cfg.dstNodeID)
		if err != nil {
			return err
		}
		node := client.NewNodeClient(twin, rpc)
		if err := call(ctx, cfg.timeout, func(c context.Context) error { return node.DeploymentStart(c, cfg.startContract) }); err != nil {
			return fmt.Errorf("start: %w", err)
		}
		log.Printf("start accepted for contract %d — the VM will boot", cfg.startContract)
		return nil
	}

	return migrate(ctx, cfg, mgr, rpc)
}

func migrate(ctx context.Context, cfg config, mgr substrate.Manager, rpc *peer.RpcClient) error {
	if cfg.srcContract == 0 || cfg.srcNodeID == 0 && cfg.srcNodeTwin == 0 || cfg.dstNodeID == 0 && cfg.dstNodeTwin == 0 {
		return fmt.Errorf("-src-contract, a source node (-src-node-id/twin) and a target node (-dst-node-id/twin) are required")
	}

	s3, err := newS3(cfg)
	if err != nil {
		return fmt.Errorf("s3 client: %w", err)
	}
	if err := ensureBucket(ctx, s3, cfg.s3Bucket, cfg.s3Region); err != nil {
		return fmt.Errorf("ensure bucket: %w", err)
	}
	log.Printf("bucket %q ready on %s", cfg.s3Bucket, cfg.s3Endpoint)

	srcTwin, err := resolveTwin(mgr, cfg.srcNodeTwin, cfg.srcNodeID)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	dstTwin, err := resolveTwin(mgr, cfg.dstNodeTwin, cfg.dstNodeID)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	src := client.NewNodeClient(srcTwin, rpc)
	dst := client.NewNodeClient(dstTwin, rpc)

	// fetch the VM deployment and figure out what to move
	vmDl, err := getDeployment(ctx, src, cfg.srcContract, cfg.timeout)
	if err != nil {
		return fmt.Errorf("fetch VM deployment %d: %w", cfg.srcContract, err)
	}
	items, err := inspect(vmDl, cfg.srcContract)
	if err != nil {
		return err
	}
	log.Printf("VM deployment %d: %d transferable workload(s) detected", cfg.srcContract, len(items))
	for _, it := range items {
		log.Printf("  - %s (%s) -> s3://%s/%s", it.name, it.typ, cfg.s3Bucket, it.key)
	}

	// ---- TRANSFER (source node) ----
	uploads := make([]client.TransferItem, 0, len(items))
	for _, it := range items {
		// remove any stale object so we can detect the fresh upload's completion
		_ = s3.RemoveObject(ctx, cfg.s3Bucket, it.key, minio.RemoveObjectOptions{})
		put, err := s3.PresignedPutObject(ctx, cfg.s3Bucket, it.key, cfg.presignExpiry)
		if err != nil {
			return fmt.Errorf("presign put %s: %w", it.key, err)
		}
		uploads = append(uploads, client.TransferItem{WorkloadName: it.name, URL: put.String()})
	}
	log.Printf("calling transfer on source node %d (this pauses the VM)...", cfg.srcNodeID)
	if err := call(ctx, cfg.timeout, func(c context.Context) error {
		return src.DeploymentTransfer(c, cfg.srcContract, uploads)
	}); err != nil {
		// the ack often does not survive a flaky link; the node keeps uploading.
		log.Printf("note: transfer ack failed (%v) — continuing; will confirm via S3", err)
	}
	log.Printf("waiting for %d object(s) to finish uploading to S3 (timeout %s)...", len(items), cfg.uploadTimeout)
	if err := waitObjects(ctx, s3, cfg.s3Bucket, items, cfg.uploadTimeout); err != nil {
		return fmt.Errorf("transfer did not complete: %w", err)
	}
	log.Printf("all objects uploaded to S3")

	// ---- NETWORK (deploy on target) ----
	// Auto-detect the network deployment from the VM's network reference if the
	// caller didn't pass -src-net-contract.
	netContract := cfg.srcNetContract
	if netContract == 0 {
		if names := networkNames(vmDl); len(names) > 0 {
			if c, n, err := findNetworkContract(ctx, src, cfg.timeout, names); err == nil {
				netContract = c
				log.Printf("auto-detected network %q in contract %d", n, c)
			} else {
				log.Printf("could not auto-detect network contract (%v); assuming it already exists on the target", err)
			}
		}
	}
	if netContract != 0 {
		if err := migrateNetwork(ctx, cfg, mgr, src, dst, netContract); err != nil {
			return err
		}
	}

	// ---- PREPARE (target node) ----
	downloads := make([]client.TransferItem, 0, len(items))
	for _, it := range items {
		get, err := s3.PresignedGetObject(ctx, cfg.s3Bucket, it.key, cfg.presignExpiry, url.Values{})
		if err != nil {
			return fmt.Errorf("presign get %s: %w", it.key, err)
		}
		downloads = append(downloads, client.TransferItem{WorkloadName: it.name, URL: get.String(), Size: it.size})
	}

	if err := prepZeroVersion(&vmDl); err != nil {
		return fmt.Errorf("VM deployment: %w", err)
	}
	if err := reSign(vmDl.TwinID, cfg, &vmDl); err != nil {
		return fmt.Errorf("re-sign VM: %w", err)
	}
	body := bestBody(mgr, cfg.srcContract, vmDl.Metadata)
	vmContract, err := createNodeContract(mgr, cfg, hashHex(&vmDl), body)
	if err != nil {
		return fmt.Errorf("create VM contract: %w", err)
	}
	vmDl.ContractID = vmContract
	autoStart := !cfg.stageOnly
	log.Printf("preparing VM as contract %d on node %d (downloads=%d, auto-start=%v)", vmContract, cfg.dstNodeID, len(downloads), autoStart)
	if err := call(ctx, cfg.timeout, func(c context.Context) error {
		return dst.DeploymentPrepare(c, vmDl, downloads, autoStart)
	}); err != nil {
		log.Printf("note: prepare ack failed (%v) — the node keeps working; continuing", err)
	}

	if cfg.stageOnly {
		log.Printf("")
		log.Printf("=== migration staged: VM contract %d on node %d ===", vmContract, cfg.dstNodeID)
		log.Printf("the target is pulling the data in the background; boot it later with:")
		log.Printf("  MNEMONIC=... go run . -dst-node-id %d -start-contract %d", cfg.dstNodeID, vmContract)
		return nil
	}

	// wait until the target finishes downloading AND boots the VM: the zmachine
	// workload only reaches Ok after the auto-start that follows the downloads.
	vmName := zmachineName(items)
	log.Printf("waiting for the target to finish downloading and boot the VM (up to %s)...", cfg.uploadTimeout)
	if err := waitWorkloadOk(ctx, dst, vmContract, vmName, cfg.uploadTimeout); err != nil {
		return fmt.Errorf("VM did not come up on the target: %w", err)
	}
	log.Printf("")
	log.Printf("=== migration complete: VM %q is up on node %d (contract %d) ===", vmName, cfg.dstNodeID, vmContract)
	return nil
}

func zmachineName(items []transferItem) gridtypes.Name {
	for _, it := range items {
		if it.typ == zos.ZMachineType || it.typ == zos.ZMachineLightType {
			return it.name
		}
	}
	return ""
}

func migrateNetwork(ctx context.Context, cfg config, mgr substrate.Manager, src, dst *client.NodeClient, srcNetContract uint64) error {
	netDl, err := getDeployment(ctx, src, srcNetContract, cfg.timeout)
	if err != nil {
		return fmt.Errorf("fetch network deployment %d: %w", srcNetContract, err)
	}
	netName, err := firstNetworkName(netDl)
	if err != nil {
		return err
	}
	if err := prepZeroVersion(&netDl); err != nil {
		return fmt.Errorf("network deployment: %w", err)
	}
	if err := reSign(netDl.TwinID, cfg, &netDl); err != nil {
		return fmt.Errorf("re-sign network: %w", err)
	}
	body := bestBody(mgr, srcNetContract, netDl.Metadata)
	netContract, err := createNodeContract(mgr, cfg, hashHex(&netDl), body)
	if err != nil {
		return fmt.Errorf("create network contract: %w", err)
	}
	netDl.ContractID = netContract
	log.Printf("deploying network %q as contract %d on node %d", netName, netContract, cfg.dstNodeID)
	if err := call(ctx, cfg.timeout, func(c context.Context) error { return dst.DeploymentDeploy(c, netDl) }); err != nil {
		return fmt.Errorf("deploy network: %w", err)
	}
	log.Printf("waiting for network %q to provision...", netName)
	if err := waitWorkloadOk(ctx, dst, netContract, netName, 3*time.Minute); err != nil {
		return fmt.Errorf("network did not provision: %w", err)
	}
	log.Printf("network %q is up on node %d", netName, cfg.dstNodeID)
	return nil
}

// transferItem is a resolved workload to move: its S3 object key and (for a
// zmachine) the rootfs volume size to recreate on the target.
type transferItem struct {
	name gridtypes.Name
	typ  gridtypes.WorkloadType
	key  string
	size gridtypes.Unit
}

// inspect finds the zmount(s) and the zmachine in the deployment and derives the
// rootfs size from ZMachine.Size.
func inspect(dl gridtypes.Deployment, srcContract uint64) ([]transferItem, error) {
	var items []transferItem
	for i := range dl.Workloads {
		wl := &dl.Workloads[i]
		switch wl.Type {
		case zos.ZMountType:
			items = append(items, transferItem{
				name: wl.Name, typ: wl.Type,
				key: fmt.Sprintf("mig-%d/%s.raw", srcContract, wl.Name),
			})
		case zos.ZMachineType, zos.ZMachineLightType:
			var zm zos.ZMachine
			if err := json.Unmarshal(wl.Data, &zm); err != nil {
				return nil, fmt.Errorf("decode zmachine %q: %w", wl.Name, err)
			}
			items = append(items, transferItem{
				name: wl.Name, typ: wl.Type,
				key:  fmt.Sprintf("mig-%d/%s-rootfs.tar", srcContract, wl.Name),
				size: zm.Size,
			})
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no zmount/zmachine workloads found in deployment %d", srcContract)
	}
	return items, nil
}

// networkNames returns the znet names the VM's zmachine(s) attach to.
func networkNames(dl gridtypes.Deployment) []gridtypes.Name {
	var names []gridtypes.Name
	seen := map[gridtypes.Name]bool{}
	for i := range dl.Workloads {
		wl := &dl.Workloads[i]
		if wl.Type != zos.ZMachineType && wl.Type != zos.ZMachineLightType {
			continue
		}
		var zm zos.ZMachine
		if json.Unmarshal(wl.Data, &zm) != nil {
			continue
		}
		for _, iface := range zm.Network.Interfaces {
			if iface.Network != "" && !seen[iface.Network] {
				seen[iface.Network] = true
				names = append(names, iface.Network)
			}
		}
	}
	return names
}

// findNetworkContract lists the twin's deployments on the source node and returns
// the contract that holds a network workload matching one of the given names.
func findNetworkContract(ctx context.Context, node *client.NodeClient, timeout time.Duration, names []gridtypes.Name) (uint64, gridtypes.Name, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deps, err := node.DeploymentList(c)
	if err != nil {
		return 0, "", err
	}
	want := map[gridtypes.Name]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, d := range deps {
		for _, wl := range d.Workloads {
			if (wl.Type == zos.NetworkType || wl.Type == zos.NetworkLightType) && want[wl.Name] {
				return d.ContractID, wl.Name, nil
			}
		}
	}
	return 0, "", fmt.Errorf("no network deployment found for %v on the source node", names)
}

// ---- S3 helpers ----

func newS3(cfg config) (*minio.Client, error) {
	u, err := url.Parse(cfg.s3Endpoint)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if host == "" {
		host = cfg.s3Endpoint
	}
	return minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.s3Access, cfg.s3Secret, ""),
		Secure: cfg.s3Secure,
		Region: cfg.s3Region,
	})
}

func ensureBucket(ctx context.Context, s3 *minio.Client, bucket, region string) error {
	err := s3.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region})
	if err == nil {
		return nil
	}
	if exists, e := s3.BucketExists(ctx, bucket); e == nil && exists {
		return nil
	}
	return err
}

func waitObjects(ctx context.Context, s3 *minio.Client, bucket string, items []transferItem, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	done := make(map[string]bool)
	for {
		for _, it := range items {
			if done[it.key] {
				continue
			}
			if info, err := s3.StatObject(ctx, bucket, it.key, minio.StatObjectOptions{}); err == nil {
				done[it.key] = true
				log.Printf("  uploaded: %s (%d bytes)", it.key, info.Size)
			}
		}
		if len(done) == len(items) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out with %d/%d objects uploaded", len(done), len(items))
		}
		time.Sleep(15 * time.Second)
	}
}

// ---- deployment / chain helpers ----

func getDeployment(ctx context.Context, node *client.NodeClient, contract uint64, timeout time.Duration) (gridtypes.Deployment, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return node.DeploymentGet(c, contract)
}

func call(ctx context.Context, timeout time.Duration, fn func(context.Context) error) error {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(c)
}

func prepZeroVersion(dl *gridtypes.Deployment) error {
	if dl.Version != 0 {
		return fmt.Errorf("deployment version is %d, but a fresh prepare/deploy requires version 0", dl.Version)
	}
	return dl.Valid()
}

func hashHex(dl *gridtypes.Deployment) string {
	h, _ := dl.ChallengeHash()
	return hex.EncodeToString(h)
}

// bestBody returns the source contract's on-chain deployment_data (so the dashboard
// shows the type), falling back to the deployment metadata.
func bestBody(mgr substrate.Manager, srcContract uint64, metadata string) string {
	sub, err := mgr.Substrate()
	if err != nil {
		return metadata
	}
	defer sub.Close()
	c, err := sub.GetContract(srcContract)
	if err != nil {
		log.Printf("warning: could not read deployment_data of contract %d (dashboard label may be blank): %v", srcContract, err)
		return metadata
	}
	if d := c.ContractType.NodeContract.DeploymentData; d != "" {
		return d
	}
	return metadata
}

func createNodeContract(mgr substrate.Manager, cfg config, hashHex, body string) (uint64, error) {
	id, err := identityFrom(cfg.keyType, cfg.mnemonic)
	if err != nil {
		return 0, err
	}
	sub, err := mgr.Substrate()
	if err != nil {
		return 0, fmt.Errorf("connect substrate: %w", err)
	}
	defer sub.Close()
	return sub.CreateNodeContract(id, cfg.dstNodeID, body, hashHex, 0, nil)
}

func reSign(twin uint32, cfg config, dl *gridtypes.Deployment) error {
	id, err := identityFrom(cfg.keyType, cfg.mnemonic)
	if err != nil {
		return err
	}
	return dl.Sign(twin, identitySigner{id: id, keyType: cfg.keyType})
}

func firstNetworkName(dl gridtypes.Deployment) (gridtypes.Name, error) {
	for _, wl := range dl.Workloads {
		if wl.Type == zos.NetworkType || wl.Type == zos.NetworkLightType {
			return wl.Name, nil
		}
	}
	return "", fmt.Errorf("no network workload found in the network deployment")
}

func waitWorkloadOk(ctx context.Context, node *client.NodeClient, contract uint64, name gridtypes.Name, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if dl, err := getDeployment(ctx, node, contract, 20*time.Second); err == nil {
			if wl, err := dl.Get(name); err == nil {
				if wl.Result.State.IsOkay() {
					return nil
				}
				if wl.Result.State == gridtypes.StateError {
					return fmt.Errorf("workload %q errored: %s", name, wl.Result.Error)
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %q", name)
		}
		time.Sleep(2 * time.Second)
	}
}

func resolveTwin(mgr substrate.Manager, twin, nodeID uint32) (uint32, error) {
	if twin != 0 {
		return twin, nil
	}
	if nodeID == 0 {
		return 0, fmt.Errorf("a node twin or node id is required")
	}
	sub, err := mgr.Substrate()
	if err != nil {
		return 0, fmt.Errorf("connect substrate: %w", err)
	}
	defer sub.Close()
	node, err := sub.GetNode(nodeID)
	if err != nil {
		return 0, fmt.Errorf("resolve node %d: %w", nodeID, err)
	}
	return uint32(node.TwinID), nil
}

func identityFrom(keyType, mnemonic string) (substrate.Identity, error) {
	if keyType == "ed25519" {
		return substrate.NewIdentityFromEd25519Phrase(mnemonic)
	}
	return substrate.NewIdentityFromSr25519Phrase(mnemonic)
}

type identitySigner struct {
	id      substrate.Identity
	keyType string
}

func (s identitySigner) Sign(msg []byte) ([]byte, error) { return s.id.Sign(msg) }
func (s identitySigner) Type() string                    { return s.keyType }

// ---- flags ----

func parseFlags() (config, error) {
	var cfg config
	var (
		srcNodeID   = flag.Uint("src-node-id", 0, "source node id")
		srcNodeTwin = flag.Uint("src-node-twin", 0, "source node twin (else -src-node-id)")
		dstNodeID   = flag.Uint("dst-node-id", 0, "target node id")
		dstNodeTwin = flag.Uint("dst-node-twin", 0, "target node twin (else -dst-node-id)")
	)
	flag.StringVar(&cfg.mnemonic, "mnemonic", os.Getenv("MNEMONIC"), "owner twin mnemonic (or MNEMONIC env)")
	flag.StringVar(&cfg.keyType, "keytype", "sr25519", "mnemonic key type: sr25519 or ed25519")
	flag.StringVar(&cfg.substrateURL, "substrate", "wss://tfchain.dev.grid.tf/ws", "substrate websocket url")
	flag.StringVar(&cfg.relayURL, "relay", "wss://relay.dev.grid.tf", "rmb relay url")
	flag.DurationVar(&cfg.timeout, "timeout", 60*time.Second, "per-RMB-call timeout")
	flag.DurationVar(&cfg.uploadTimeout, "upload-timeout", 2*time.Hour, "how long to wait for S3 uploads to finish")

	flag.StringVar(&cfg.s3Endpoint, "s3-endpoint", "", "S3 endpoint url (e.g. https://gateway.storjshare.io)")
	flag.StringVar(&cfg.s3Access, "s3-access", os.Getenv("S3_ACCESS"), "S3 access key (or S3_ACCESS env)")
	flag.StringVar(&cfg.s3Secret, "s3-secret", os.Getenv("S3_SECRET"), "S3 secret key (or S3_SECRET env)")
	flag.StringVar(&cfg.s3Bucket, "s3-bucket", "zos-migration", "S3 bucket to use")
	flag.StringVar(&cfg.s3Region, "s3-region", "global", "S3 region for signing")
	flag.BoolVar(&cfg.s3Secure, "s3-secure", true, "use https for S3")
	flag.DurationVar(&cfg.presignExpiry, "presign-expiry", 24*time.Hour, "presigned URL lifetime")

	flag.Uint64Var(&cfg.srcContract, "src-contract", 0, "source VM contract id")
	flag.Uint64Var(&cfg.srcNetContract, "src-net-contract", 0, "source network contract id (optional if the network already exists on target)")
	flag.Uint64Var(&cfg.startContract, "start-contract", 0, "start-only: boot this prepared contract on -dst-node-id and exit")
	flag.BoolVar(&cfg.stageOnly, "stage-only", false, "prepare without auto-start/wait (print the start command and exit)")
	flag.Parse()

	cfg.srcNodeID, cfg.srcNodeTwin = uint32(*srcNodeID), uint32(*srcNodeTwin)
	cfg.dstNodeID, cfg.dstNodeTwin = uint32(*dstNodeID), uint32(*dstNodeTwin)

	if cfg.mnemonic == "" {
		return cfg, fmt.Errorf("-mnemonic (or MNEMONIC env) is required")
	}
	if cfg.startContract == 0 { // full migration needs S3
		if cfg.s3Endpoint == "" || cfg.s3Access == "" || cfg.s3Secret == "" {
			return cfg, fmt.Errorf("-s3-endpoint, -s3-access and -s3-secret are required")
		}
		if !strings.Contains(cfg.s3Endpoint, "://") {
			cfg.s3Endpoint = "https://" + cfg.s3Endpoint
		}
	}
	return cfg, nil
}
