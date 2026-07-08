package environment

import (
	"net/http"
	"os"
	"slices"
	"strconv"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	substrate "github.com/threefoldtech/tfchain/clients/tfchain-client-go"
	"github.com/threefoldtech/zos_base/pkg"

	"github.com/threefoldtech/zos_base/pkg/kernel"
)

const (
	baseExtendedURL = "https://raw.githubusercontent.com/threefoldtech/zos_config/main/"

	defaultHubURL   = "https://hub.threefold.me"
	defaultV4HubURL = "https://v4.hub.threefold.me"

	defaultFlistURL   = "redis://hub.threefold.me:9900"
	defaultV4FlistURL = "redis://v4.hub.threefold.me:9940"

	defaultHubStorage   = "zdb://hub.threefold.me:9900"
	defaultV4HubStorage = "zdb://v4.hub.threefold.me:9940"
)

var defaultGeoipURLs = []string{
	"https://geoip.threefold.me/",
	"https://geoip.grid.tf/",
	"https://02.geoip.grid.tf/",
	"https://03.geoip.grid.tf/",
}

// PubMac specify how the mac address of the public nic
// (in case of public-config) is calculated
type PubMac string

const (
	// PubMacRandom means the mac of the public nic will be chosen by the system
	// the value won't change across reboots, but is based on the node id
	// (default)
	PubMacRandom PubMac = "random"
	// PubMacSwap means the value of the mac is swapped with the physical nic
	// where the public traffic is eventually going through
	PubMacSwap PubMac = "swap"
)

// Environment holds information about running environment of a node
// it defines the different constant based on the running mode (dev, test, prod)
type Environment struct {
	RunningMode RunMode

	FlistURL   string
	HubStorage string
	BinRepo    string

	HubURL   string
	V4HubURL string

	FarmID pkg.FarmID
	Orphan bool

	FarmSecret   string
	SubstrateURL []string
	// IMPORTANT NOTICE:
	//   SINCE RELAYS FOR A NODE IS STORED ON THE CHAIN IN A LIMITED SPACE
	//   PLEASE MAKE SURE THAT ANY ENV HAS NO MORE THAN FOUR RELAYS CONFIGURED
	RelaysURLs    []string
	ActivationURL []string
	GraphQL       []string
	GeoipURLs     []string
	KycURL        string
	RegistrarURL  string

	// private vlan to join
	// if set, zos will use this as its priv vlan
	PrivVlan *uint16

	// pub vlan to join
	// if set, zos will use this as it's pub vlan
	// only in a single nic setup
	PubVlan *uint16

	// PubMac value from environment
	PubMac PubMac
}

// RunMode type
type RunMode string

func (r RunMode) String() string {
	switch r {
	case RunningDev:
		return "development"
	case RunningQA:
		return "qa"
	case RunningMain:
		return "production"
	case RunningTest:
		return "testing"
	}

	return "unknown"
}

// Possible running mode of a node
const (
	// RunningDev mode
	RunningDev RunMode = "dev"
	// RunningQA mode
	RunningQA RunMode = "qa"
	// RunningTest mode
	RunningTest RunMode = "test"
	// RunningMain mode
	RunningMain RunMode = "prod"

	// Orphanage is the default farmid where nodes are registered
	// if no farmid were specified on the kernel command line
	OrphanageDev  pkg.FarmID = 0
	OrphanageTest pkg.FarmID = 0
	OrphanageMain pkg.FarmID = 0
)

var (
	pool    substrate.Manager
	subURLs []string

	envDev = Environment{
		RunningMode: RunningDev,
		SubstrateURL: []string{
			"wss://tfchain.dev.grid.tf/",
			"wss://tfchain.02.dev.grid.tf",
		},
		RelaysURLs: []string{
			"wss://relay.dev.grid.tf",
		},
		ActivationURL: []string{
			"https://activation.dev.grid.tf/activation/activate",
			"https://activation.02.dev.grid.tf/activation/activate",
		},
		HubURL:     defaultHubURL,
		V4HubURL:   defaultV4HubURL,
		FlistURL:   defaultFlistURL,
		HubStorage: defaultHubStorage,
		BinRepo:    "tf-zos-v3-bins.dev",
		GraphQL: []string{
			"https://graphql.dev.grid.tf/graphql",
			"https://graphql.02.dev.grid.tf/graphql",
		},
		KycURL:       "https://kyc.dev.grid.tf",
		RegistrarURL: "http://registrar.dev4.grid.tf",
		GeoipURLs:    defaultGeoipURLs,
	}

	envTest = Environment{
		RunningMode: RunningTest,
		SubstrateURL: []string{
			"wss://tfchain.test.grid.tf/",
			"wss://tfchain.02.test.grid.tf",
		},
		RelaysURLs: []string{
			"wss://relay.test.grid.tf",
		},
		ActivationURL: []string{
			"https://activation.test.grid.tf/activation/activate",
			"https://activation.02.test.grid.tf/activation/activate",
		},
		HubURL:     defaultHubURL,
		V4HubURL:   defaultV4HubURL,
		FlistURL:   defaultFlistURL,
		HubStorage: defaultHubStorage,
		BinRepo:    "tf-zos-v3-bins.test",
		GraphQL: []string{
			"https://graphql.test.grid.tf/graphql",
			"https://graphql.02.test.grid.tf/graphql",
		},
		KycURL:       "https://kyc.test.grid.tf",
		RegistrarURL: "http://registrar.test4.grid.tf",
		GeoipURLs:    defaultGeoipURLs,
	}

	envQA = Environment{
		RunningMode: RunningQA,
		SubstrateURL: []string{
			"wss://tfchain.qa.grid.tf/",
			"wss://tfchain.02.qa.grid.tf/",
		},
		RelaysURLs: []string{
			"wss://relay.qa.grid.tf",
		},
		ActivationURL: []string{
			"https://activation.qa.grid.tf/activation/activate",
			"https://activation.02.qa.grid.tf/activation/activate",
		},
		HubURL:     defaultHubURL,
		V4HubURL:   defaultV4HubURL,
		FlistURL:   defaultFlistURL,
		HubStorage: defaultHubStorage,
		BinRepo:    "tf-zos-v3-bins.qanet",
		GraphQL: []string{
			"https://graphql.qa.grid.tf/graphql",
			"https://graphql.02.qa.grid.tf/graphql",
		},
		KycURL:       "https://kyc.qa.grid.tf",
		RegistrarURL: "https://registrar.qa4.grid.tf",
		GeoipURLs:    defaultGeoipURLs,
	}

	envProd = Environment{
		RunningMode: RunningMain,
		SubstrateURL: []string{
			"wss://tfchain.grid.tf/",
			"wss://tfchain.02.grid.tf",
			"wss://02.tfchain.grid.tf/",
			"wss://03.tfchain.grid.tf/",
			"wss://04.tfchain.grid.tf/",
		},
		RelaysURLs: []string{
			"wss://relay.grid.tf",
		},
		ActivationURL: []string{
			"https://activation.grid.threefold.me/activation/activate",
			"https://activation.grid.tf/activation/activate",
			"https://activation.02.grid.tf/activation/activate",
			"https://activation.grid.threefold.me/activation/activate",
		},
		HubURL:     defaultHubURL,
		V4HubURL:   defaultV4HubURL,
		FlistURL:   defaultFlistURL,
		HubStorage: defaultHubStorage,
		BinRepo:    "tf-zos-v3-bins",
		GraphQL: []string{
			"https://graphql.grid.threefold.me/graphql",
			"https://graphql.grid.tf/graphql",
			"https://graphql.02.grid.tf/graphql",
			"https://graphql.grid.threefold.me/graphql",
		},
		KycURL:       "https://kyc.threefold.me",
		RegistrarURL: "https://registrar.prod4.threefold.me",
		GeoipURLs:    defaultGeoipURLs,
	}
)

// MustGet returns the running environment of the node
// panics on error
func MustGet() Environment {
	env, err := Get()
	if err != nil {
		panic(err)
	}

	return env
}

// Get return the running environment of the node
func Get() (Environment, error) {
	params := kernel.GetParams()
	env, err := getEnvironmentFromParams(params)
	if err != nil {
		return Environment{}, err
	}

	return env, nil
}

// GetSubstrate gets a client to subsrate blockchain
func GetSubstrate() (substrate.Manager, error) {
	env, err := Get()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get boot environment")
	}
	updatedSubURLs := env.SubstrateURL

	slices.Sort(subURLs)
	slices.Sort(updatedSubURLs)

	// if substrate url changed then update subURLs and update pool with new manager only if the old connection is broken
	if !slices.Equal(subURLs, updatedSubURLs) {
		// before attempting to update the manager check if pool variable maintain a healthy connection
		// pool.Row() checks the health of the connection and if all the urls used in pool are down, then it will return error
		if pool != nil {
			cl, _, err := pool.Raw()
			if err == nil {
				cl.Client.Close()
				return pool, nil
			}
		}

		log.Debug().Strs("substrate_urls", updatedSubURLs).Msg("updating to sub manager with url")
		pool = substrate.NewManager(env.SubstrateURL...)
		subURLs = updatedSubURLs
	}

	// poolOnce.Do(func() {
	// 	pool = substrate.NewManager(env.SubstrateURL...)
	// })

	return pool, nil
}

func getEnvironmentFromParams(params kernel.Params) (Environment, error) {
	var env Environment
	runmode := ""
	if modes, ok := params.Get("runmode"); ok {
		if len(modes) >= 1 {
			runmode = modes[0]
		}
	} else {
		runmode = os.Getenv("ZOS_RUNMODE")
	}

	if len(runmode) == 0 {
		runmode = string(RunningMain)
	}

	switch RunMode(runmode) {
	case RunningDev:
		env = envDev
	case RunningQA:
		env = envQA
	case RunningTest:
		env = envTest
	case RunningMain:
		env = envProd
	default:
		env = envProd
	}

	config, err := getConfig(env.RunningMode, baseExtendedURL, http.DefaultClient)
	if err != nil {
		// maybe the node can't reach the internet right now
		// this will enforce node to skip config
		// or we can keep retrying untill it can fetch config
		config = Config{}
	}

	if substrate, ok := params.Get("substrate"); ok && len(substrate) > 0 {
		env.SubstrateURL = substrate
	} else if substrate := config.SubstrateURL; len(substrate) > 0 {
		env.SubstrateURL = substrate
	}

	if relay, ok := params.Get("relay"); ok && len(relay) > 0 {
		env.RelaysURLs = relay
	} else if relay := config.RelaysURLs; len(relay) > 0 {
		env.RelaysURLs = relay
	}

	if activation, ok := params.Get("activation"); ok && len(activation) > 0 {
		env.ActivationURL = activation
	} else if activation := config.ActivationURL; len(activation) > 0 {
		env.ActivationURL = activation
	}

	if graphql := config.GraphQL; len(graphql) > 0 {
		env.GraphQL = graphql
	}

	if bin := config.BinRepo; len(bin) > 0 {
		env.BinRepo = bin
	}

	if kyc := config.KycURL; len(kyc) > 0 {
		env.KycURL = kyc
	}

	if registrar := config.RegistrarURL; len(registrar) > 0 {
		env.RegistrarURL = registrar
	}

	if geoip := config.GeoipURLs; len(geoip) > 0 {
		env.GeoipURLs = geoip
	}

	// flist url and hub storage urls shouldn't listen to changes in config as long as we can't change it at run time.
	// it would cause breakage in vmd that needs a reboot to be recovered.
	if flist := config.FlistURL; len(flist) > 0 {
		env.FlistURL = flist
	}

	if storage := config.HubStorage; len(storage) > 0 {
		env.HubStorage = storage
	}

	// maybe we should verify that we're using a working hub url
	if hub := config.HubURL; len(hub) > 0 {
		env.HubURL = hub[0]
	}

	// some modules needs v3 hub url even if the node is of v4
	if hub := config.V4HubURL; len(hub) > 0 {
		env.V4HubURL = hub[0]
	}

	// if the node running v4 chage urls to use v4 hub
	if params.IsV4() {
		env.FlistURL = defaultV4FlistURL
		if flist := config.V4FlistURL; len(flist) > 0 {
			env.FlistURL = flist
		}

		env.HubStorage = defaultV4HubStorage
		if storage := config.V4HubStorage; len(storage) > 0 {
			env.HubStorage = storage
		}
	}

	if farmSecret, ok := params.Get("secret"); ok {
		if len(farmSecret) > 0 {
			env.FarmSecret = farmSecret[len(farmSecret)-1]
		}
	}

	farmerID, found := params.Get("farmer_id")
	if !found || len(farmerID) < 1 || farmerID[0] == "" {
		// fmt.Println("Warning: no valid farmer_id found in kernel parameter, fallback to orphanage")
		env.Orphan = true

		switch env.RunningMode {
		case RunningDev:
			env.FarmID = OrphanageDev
		case RunningTest:
			env.FarmID = OrphanageTest
		case RunningMain:
			env.FarmID = OrphanageMain
		}

	} else {
		env.Orphan = false
		id, err := strconv.ParseUint(farmerID[0], 10, 32)
		if err != nil {
			return env, errors.Wrap(err, "wrong format for farm ID")
		}
		env.FarmID = pkg.FarmID(id)
	}

	if vlan, found := params.GetOne("vlan:priv"); found {
		if !slices.Contains([]string{"none", "untagged", "un"}, vlan) {
			tag, err := strconv.ParseUint(vlan, 10, 16)
			if err != nil {
				return env, errors.Wrap(err, "failed to parse priv vlan value")
			}
			tagU16 := uint16(tag)
			env.PrivVlan = &tagU16
		}
	}

	if vlan, found := params.GetOne("vlan:pub"); found {
		if !slices.Contains([]string{"none", "untagged", "un"}, vlan) {
			tag, err := strconv.ParseUint(vlan, 10, 16)
			if err != nil {
				return env, errors.Wrap(err, "failed to parse pub vlan value")
			}
			tagU16 := uint16(tag)
			env.PubVlan = &tagU16
		}
	}

	if mac, found := params.GetOne("pub:mac"); found {
		v := PubMac(mac)
		if slices.Contains([]PubMac{PubMacRandom, PubMacSwap}, v) {
			env.PubMac = v
		} else {
			env.PubMac = PubMacRandom
		}
	} else {
		env.PubMac = PubMacRandom
	}

	// Checking if there environment variable
	// override default settings

	if e := os.Getenv("ZOS_SUBSTRATE_URL"); e != "" {
		env.SubstrateURL = []string{e}
	}

	if e := os.Getenv("ZOS_FLIST_URL"); e != "" {
		env.FlistURL = e
	}

	if e := os.Getenv("ZOS_BIN_REPO"); e != "" {
		env.BinRepo = e
	}

	return env, nil
}
