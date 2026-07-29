package provision

import "github.com/threefoldtech/zosbase/pkg/gridtypes"

func (e *NativeEngine) GetDeployment(twin uint32, contractID uint64) (gridtypes.Deployment, error) {
	return e.storage.Get(twin, contractID, WithDeleted())
}

func (e *NativeEngine) GetDeployments(twin uint32) ([]gridtypes.Deployment, error) {
	ids, err := e.storage.ByTwin(twin, WithDeleted())
	if err != nil {
		return nil, err
	}
	deployments := make([]gridtypes.Deployment, 0, len(ids))
	for _, id := range ids {
		dep, err := e.storage.Get(twin, id, WithDeleted())
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, dep)
	}
	return deployments, nil
}

func (e *NativeEngine) GetTwins() ([]uint32, error) {
	return e.storage.Twins(WithDeleted())
}

func (e *NativeEngine) GetWorkload(twin uint32, contractID uint64, name gridtypes.Name) (gridtypes.Workload, bool, error) {
	dep, err := e.storage.Get(twin, contractID, WithDeleted())
	if err != nil {
		return gridtypes.Workload{}, false, err
	}
	for i := range dep.Workloads {
		if dep.Workloads[i].Name == name {
			return dep.Workloads[i], true, nil
		}
	}
	wl, err := e.storage.Current(twin, contractID, name, WithDeleted())
	if err != nil {
		return gridtypes.Workload{}, false, nil // not found, no error
	}
	return wl, true, nil
}
