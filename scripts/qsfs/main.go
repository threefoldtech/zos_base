package main

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	substrate "github.com/threefoldtech/tfchain/clients/tfchain-client-go"
	"github.com/threefoldtech/zos_base/client"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
	"github.com/threefoldtech/zos_sdk_go/rmb-sdk-go/peer"
)

const (
	SubstrateURL = "wss://tfchain.dev.grid.tf:443"
	RelayURL     = "wss://relay.dev.grid.tf"
	Version      = 0

	// Node for the VM deployment
	VMNodeID = 337

	// Nodes for ZDB deployments (need at least 2 nodes with HRU)
	ZDBNodeID1 = 337
	ZDBNodeID2 = 31

	Twin        = 58
	NetworkName = "qsfsnetwork"

	// QSFS parameters (matching the TS script)
	// TS SDK creates count + 4 ZDBs: count "seq" for data, 4 "user" for meta
	QSFSDataZDBCount = 8 // "seq" mode ZDBs for data groups
	QSFSMetaZDBCount = 4 // "user" mode ZDBs for meta
	QSFSZDBPassword  = "mypassword"
	QSFSZDBSize      = 1 // GB per ZDB

	QSFSMinimalShards  = 2
	QSFSExpectedShards = 4
	QSFSPrefix         = "hamada"
	QSFSCacheSize      = 1 // GB

	SSHKey = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDTwULSsUubOq3VPWL6cdrDvexDmjfznGydFPyaNcn7gAL9lRxwFbCDPMj7MbhNSpxxHV2+/iJPQOTVJu4oc1N7bPP3gBCnF51rPrhTpGCt5pBbTzeyNweanhedkKDsCO2mIEh/92Od5Hg512dX4j7Zw6ipRWYSaepapfyoRnNSriW/s3DH/uewezVtL5EuypMdfNngV/u2KZYWoeiwhrY/yEUykQVUwDysW/xUJNP5o+KSTAvNSJatr3FbuCFuCjBSvageOLHePTeUwu6qjqe+Xs4piF1ByO/6cOJ8bt5Vcx0bAtI8/MPApplUU/JWevsPNApvnA/ntffI+u8DCwgP ashraf@thinkpad"

	Mnemonic = "junior sock chunk accident pilot under ask green endless remove coast wood"
)

// generateWGPrivateKey generates a WireGuard (Curve25519) private key
func generateWGPrivateKey() string {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		panic(err)
	}
	// Curve25519 key clamping
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64
	return base64.StdEncoding.EncodeToString(key[:])
}

// deriveEncryptionKey matches the TS SDK: md5(passphrase) → hex string → use ASCII bytes as 32-byte key
func deriveEncryptionKey(passphrase string) zos.EncryptionKey {
	hash := md5.Sum([]byte(passphrase))
	hexStr := hex.EncodeToString(hash[:]) // 32 hex chars
	return zos.EncryptionKey([]byte(hexStr))
}

func networkWorkload() gridtypes.Workload {
	b, _ := hex.DecodeString("9751c596c7c951aedad1a5f78f18b59515064adf660e0d55abead65e6fbbd628")
	return gridtypes.Workload{
		Version:     Version,
		Type:        zos.NetworkType,
		Description: "qsfs test network",
		Name:        NetworkName,
		Data: gridtypes.MustMarshal(zos.Network{
			NetworkIPRange: gridtypes.MustParseIPNet("10.201.0.0/16"),
			Subnet:         gridtypes.MustParseIPNet("10.201.1.0/24"),
			WGPrivateKey:   generateWGPrivateKey(),
			WGListenPort:   3012,
			Mycelium: &zos.Mycelium{
				Key: zos.Bytes(b),
			},
		}),
	}
}

func zdbWorkload(name string, size gridtypes.Unit, password string, mode zos.ZDBMode) gridtypes.Workload {
	return gridtypes.Workload{
		Version:     Version,
		Name:        gridtypes.Name(name),
		Type:        zos.ZDBType,
		Description: "qsfs zdb",
		Data: gridtypes.MustMarshal(zos.ZDB{
			Size:     size,
			Mode:     mode,
			Password: password,
			Public:   true,
		}),
	}
}

func diskWorkload(name string, size gridtypes.Unit) gridtypes.Workload {
	return gridtypes.Workload{
		Name:        gridtypes.Name(name),
		Version:     Version,
		Type:        zos.ZMountType,
		Description: "vm disk",
		Data: gridtypes.MustMarshal(zos.ZMount{
			Size: size,
		}),
	}
}

func qsfsWorkload(name string, cache gridtypes.Unit, encKey zos.EncryptionKey, metaBackends []zos.ZdbBackend, dataBackends []zos.ZdbBackend) gridtypes.Workload {
	return gridtypes.Workload{
		Version:     Version,
		Name:        gridtypes.Name(name),
		Type:        zos.QuantumSafeFSType,
		Description: "qsfs disk",
		Data: gridtypes.MustMarshal(zos.QuantumSafeFS{
			Cache: cache,
			Config: zos.QuantumSafeFSConfig{
				MinimalShards:     QSFSMinimalShards,
				ExpectedShards:    QSFSExpectedShards,
				RedundantGroups:   0,
				RedundantNodes:    0,
				MaxZDBDataDirSize: 32,
				Encryption: zos.Encryption{
					Algorithm: "AES",
					Key:       encKey,
				},
				Meta: zos.QuantumSafeMeta{
					Type: "zdb",
					Config: zos.QuantumSafeConfig{
						Prefix: QSFSPrefix,
						Encryption: zos.Encryption{
							Algorithm: "AES",
							Key:       encKey,
						},
						Backends: metaBackends,
					},
				},
				Groups:      []zos.ZdbGroup{{Backends: dataBackends}},
				Compression: zos.QuantumCompression{Algorithm: "snappy"},
			},
		}),
	}
}

func vmWorkload(mounts map[string]string) gridtypes.Workload {
	return gridtypes.Workload{
		Version: Version,
		Name:    "vm",
		Type:    zos.ZMachineType,
		Data: gridtypes.MustMarshal(zos.ZMachine{
			FList: "https://hub.grid.tf/tf-official-vms/ubuntu-24.04-latest.flist",
			Network: zos.MachineNetwork{
				Interfaces: []zos.MachineInterface{
					{
						Network: NetworkName,
						IP:      net.ParseIP("10.201.1.2"),
					},
				},
				Planetary: true,
				Mycelium: func() *zos.MyceliumIP {
					seed := make([]byte, 6)
					if _, err := rand.Read(seed); err != nil {
						panic(err)
					}
					return &zos.MyceliumIP{
						Network: NetworkName,
						Seed:    zos.Bytes(seed),
					}
				}(),
			},
			Size: 5 * gridtypes.Gigabyte,
			ComputeCapacity: zos.MachineCapacity{
				CPU:    1,
				Memory: 1024 * gridtypes.Megabyte,
			},
			Entrypoint: "/sbin/zinit init",
			Mounts: func() []zos.MachineMount {
				var mnt []zos.MachineMount
				for k, v := range mounts {
					mnt = append(mnt, zos.MachineMount{
						Name:       gridtypes.Name(k),
						Mountpoint: v,
					})
				}
				return mnt
			}(),
			Env: map[string]string{
				"SSH_KEY": SSHKey,
			},
		}),
	}
}

func contractMetadata(workloadType, name, projectName string) string {
	meta := map[string]interface{}{
		"version":     3,
		"type":        workloadType,
		"name":        name,
		"projectName": projectName,
	}
	b, _ := json.Marshal(meta)
	return string(b)
}

func createDeployment(twinID uint32, workloads []gridtypes.Workload, metadata, description string) gridtypes.Deployment {
	return gridtypes.Deployment{
		Version:     Version,
		TwinID:      twinID,
		Metadata:    metadata,
		Description: description,
		Workloads:   workloads,
		SignatureRequirement: gridtypes.SignatureRequirement{
			WeightRequired: 1,
			Requests: []gridtypes.SignatureRequest{
				{
					TwinID: twinID,
					Weight: 1,
				},
			},
		},
	}
}

func signAndHash(dl *gridtypes.Deployment, twinID uint32, identity substrate.Identity) string {
	if err := dl.Valid(); err != nil {
		panic("invalid deployment: " + err.Error())
	}
	if err := dl.Sign(twinID, identity); err != nil {
		panic(err)
	}
	hash, err := dl.ChallengeHash()
	if err != nil {
		panic("failed to create hash")
	}
	return hex.EncodeToString(hash)
}

func deployAndWait(ctx context.Context, node *client.NodeClient, sub *substrate.Substrate, identity substrate.Identity, dl *gridtypes.Deployment, nodeID uint32, hashHex string) {
	ips := countPublicIPs(dl)

	contractID, err := sub.CreateNodeContract(identity, nodeID, dl.Metadata, hashHex, ips, nil)
	if err != nil {
		panic(fmt.Sprintf("failed to create contract on node %d: %v", nodeID, err))
	}
	dl.ContractID = contractID
	fmt.Printf("Created contract %d on node %d\n", contractID, nodeID)

	if err := node.DeploymentDeploy(ctx, *dl); err != nil {
		panic(fmt.Sprintf("failed to deploy on node %d: %v", nodeID, err))
	}
	fmt.Printf("Deployment sent to node %d\n", nodeID)
}

func waitForDeployment(ctx context.Context, node *client.NodeClient, contractID uint64) gridtypes.Deployment {
	fmt.Printf("Waiting for deployment (contract %d) to be ready...\n", contractID)
	for {
		got, err := node.DeploymentGet(ctx, contractID)
		if err == nil {
			allReady := true
			for _, wl := range got.Workloads {
				if wl.Result.State == gridtypes.StateInit || wl.Result.IsNil() {
					allReady = false
					break
				}
			}
			if allReady {
				return got
			}
		}
		time.Sleep(3 * time.Second)
	}
}

func countPublicIPs(dl *gridtypes.Deployment) uint32 {
	var count uint32
	for _, wl := range dl.Workloads {
		if wl.Type == zos.PublicIPType {
			var data zos.PublicIP
			if err := json.Unmarshal(wl.Data, &data); err != nil {
				panic(err)
			}
			if data.V4 {
				count++
			}
		}
	}
	return count
}

// extractZDBResults extracts ZDB backends from deployment results, filtering by mode
func extractZDBResults(dl gridtypes.Deployment, mode zos.ZDBMode) []zos.ZdbBackend {
	var backends []zos.ZdbBackend
	for _, wl := range dl.Workloads {
		if wl.Type != zos.ZDBType {
			continue
		}
		// Check the ZDB mode matches
		var zdbData zos.ZDB
		if err := json.Unmarshal(wl.Data, &zdbData); err != nil {
			continue
		}
		if zdbData.Mode != mode {
			continue
		}
		if wl.Result.State != gridtypes.StateOk {
			fmt.Printf("WARNING: ZDB %s state is %s: %s\n", wl.Name, wl.Result.State, wl.Result.Error)
			continue
		}
		var result zos.ZDBResult
		if err := json.Unmarshal(wl.Result.Data, &result); err != nil {
			panic(fmt.Sprintf("failed to parse ZDB result for %s: %v", wl.Name, err))
		}
		// TS SDK uses result.IPs[1] (second IP) for the address
		if len(result.IPs) > 1 {
			backends = append(backends, zos.ZdbBackend{
				Address:   fmt.Sprintf("[%s]:%d", result.IPs[1], result.Port),
				Namespace: result.Namespace,
				Password:  QSFSZDBPassword,
			})
		} else if len(result.IPs) > 0 {
			backends = append(backends, zos.ZdbBackend{
				Address:   fmt.Sprintf("[%s]:%d", result.IPs[0], result.Port),
				Namespace: result.Namespace,
				Password:  QSFSZDBPassword,
			})
		}
	}
	return backends
}

func main() {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	identity, err := substrate.NewIdentityFromSr25519Phrase(Mnemonic)
	if err != nil {
		panic(err)
	}

	mgr := substrate.NewManager(SubstrateURL)
	sub, err := mgr.Substrate()
	if err != nil {
		panic(err)
	}

	cl, err := peer.NewRpcClient(context.Background(), Mnemonic, mgr, peer.WithRelay(RelayURL))
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Second)
	defer cancel()

	// Get node clients for ZDB nodes and VM node
	getNodeClient := func(nodeID uint32) *client.NodeClient {
		nodeInfo, err := sub.GetNode(nodeID)
		if err != nil {
			panic(fmt.Sprintf("failed to get node %d: %v", nodeID, err))
		}
		fmt.Printf("Node %d twin ID: %d\n", nodeID, nodeInfo.TwinID)
		return client.NewNodeClient(uint32(nodeInfo.TwinID), cl)
	}

	zdbNode1Client := getNodeClient(ZDBNodeID1)
	zdbNode2Client := getNodeClient(ZDBNodeID2)
	vmNodeClient := getNodeClient(VMNodeID)

	// ================= Step 1: Deploy ZDBs =================
	// TS SDK creates count (8) "seq" ZDBs for data + 4 "user" ZDBs for meta = 12 total
	// Distributed round-robin across 2 nodes
	fmt.Println("\n================= Deploying ZDBs =================")

	totalZDBs := QSFSDataZDBCount + QSFSMetaZDBCount // 12

	var zdbWorkloads1 []gridtypes.Workload
	var zdbWorkloads2 []gridtypes.Workload

	for i := 0; i < totalZDBs; i++ {
		name := fmt.Sprintf("qsfs_zdb_%d", i)
		var mode zos.ZDBMode = zos.ZDBModeSeq
		if i >= QSFSDataZDBCount {
			mode = zos.ZDBModeUser
		}
		wl := zdbWorkload(name, QSFSZDBSize*gridtypes.Gigabyte, QSFSZDBPassword, mode)

		// Round-robin across 2 nodes (matching TS SDK distribution)
		if i%2 == 0 {
			zdbWorkloads1 = append(zdbWorkloads1, wl)
		} else {
			zdbWorkloads2 = append(zdbWorkloads2, wl)
		}
	}

	// Create and deploy ZDB deployment on node 1
	qsfsMeta := contractMetadata("qsfs", "testqsfs", "qsfs/testqsfs")
	zdbDL1 := createDeployment(Twin, zdbWorkloads1, qsfsMeta, "qsfs zdb deployment")
	hashHex1 := signAndHash(&zdbDL1, Twin, identity)
	fmt.Printf("ZDB deployment 1 hash: %s\n", hashHex1)
	deployAndWait(ctx, zdbNode1Client, sub, identity, &zdbDL1, ZDBNodeID1, hashHex1)

	// Create and deploy ZDB deployment on node 2
	zdbDL2 := createDeployment(Twin, zdbWorkloads2, qsfsMeta, "qsfs zdb deployment")
	hashHex2 := signAndHash(&zdbDL2, Twin, identity)
	fmt.Printf("ZDB deployment 2 hash: %s\n", hashHex2)
	deployAndWait(ctx, zdbNode2Client, sub, identity, &zdbDL2, ZDBNodeID2, hashHex2)

	// Wait for ZDB deployments to complete
	fmt.Println("\n================= Waiting for ZDB results =================")
	zdbResult1 := waitForDeployment(ctx, zdbNode1Client, zdbDL1.ContractID)
	zdbResult2 := waitForDeployment(ctx, zdbNode2Client, zdbDL2.ContractID)

	fmt.Println("\nZDB Deployment 1 result:")
	_ = enc.Encode(zdbResult1)
	fmt.Println("\nZDB Deployment 2 result:")
	_ = enc.Encode(zdbResult2)

	// ================= Step 2: Extract ZDB backends =================
	// Separate "seq" mode backends (data groups) from "user" mode backends (meta)
	fmt.Println("\n================= Extracting ZDB backends =================")

	var dataBackends []zos.ZdbBackend
	dataBackends = append(dataBackends, extractZDBResults(zdbResult1, zos.ZDBModeSeq)...)
	dataBackends = append(dataBackends, extractZDBResults(zdbResult2, zos.ZDBModeSeq)...)

	var metaBackends []zos.ZdbBackend
	metaBackends = append(metaBackends, extractZDBResults(zdbResult1, zos.ZDBModeUser)...)
	metaBackends = append(metaBackends, extractZDBResults(zdbResult2, zos.ZDBModeUser)...)

	fmt.Printf("Collected %d data backends (seq) and %d meta backends (user)\n", len(dataBackends), len(metaBackends))
	for i, b := range dataBackends {
		fmt.Printf("  Data backend %d: %s ns=%s\n", i, b.Address, b.Namespace)
	}
	for i, b := range metaBackends {
		fmt.Printf("  Meta backend %d: %s ns=%s\n", i, b.Address, b.Namespace)
	}

	if len(dataBackends) == 0 || len(metaBackends) == 0 {
		panic("not enough ZDB backends available, cannot create QSFS")
	}

	// ================= Step 3: Deploy VM with QSFS =================
	fmt.Println("\n================= Deploying VM with QSFS =================")

	encKey := deriveEncryptionKey("hamada")

	vmMeta := contractMetadata("vm", "vm_with_qsfs", "vm/vm_with_qsfs")
	vmDeployment := createDeployment(Twin, []gridtypes.Workload{
		networkWorkload(),
		diskWorkload("mydisk", 1*gridtypes.Gigabyte),
		qsfsWorkload("myqsfsdisk", QSFSCacheSize*gridtypes.Gigabyte, encKey, metaBackends, dataBackends),
		vmWorkload(map[string]string{
			"mydisk":     "/mydisk",
			"myqsfsdisk": "/myqsfsdisk",
		}),
	}, vmMeta, "test deploying VM with QSFS via Go client")

	hashHex3 := signAndHash(&vmDeployment, Twin, identity)
	fmt.Printf("VM deployment hash: %s\n", hashHex3)
	deployAndWait(ctx, vmNodeClient, sub, identity, &vmDeployment, VMNodeID, hashHex3)

	// ================= Step 4: Get deployment info =================
	fmt.Println("\n================= Getting deployment information =================")
	vmResult := waitForDeployment(ctx, vmNodeClient, vmDeployment.ContractID)
	_ = enc.Encode(vmResult)

	fmt.Println("\n================= Deployment Summary =================")
	fmt.Printf("ZDB Contract 1: %d (node %d)\n", zdbDL1.ContractID, ZDBNodeID1)
	fmt.Printf("ZDB Contract 2: %d (node %d)\n", zdbDL2.ContractID, ZDBNodeID2)
	fmt.Printf("VM Contract:    %d (node %d)\n", vmDeployment.ContractID, VMNodeID)
}
