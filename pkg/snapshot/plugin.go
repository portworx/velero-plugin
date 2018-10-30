package snapshot

import (
	"fmt"

	"github.com/heptio/ark/pkg/cloudprovider"
	"github.com/heptio/ark/pkg/util/collections"
	volumeclient "github.com/libopenstorage/openstorage/api/client/volume"
	"github.com/libopenstorage/openstorage/volume"
	"github.com/portworx/sched-ops/k8s"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// serviceName is the name of the portworx service
	serviceName = "portworx-service"

	// namespace is the kubernetes namespace in which portworx
	// daemon set
	// runs
	namespace = "kube-system"

	// Config parameters
	configType = "type"

	typeLocal = "local"
	typeCloud = "cloud"
)

func getVolumeDriver() (volume.VolumeDriver, error) {
	var endpoint string
	svc, err := k8s.Instance().GetService(serviceName, namespace)
	if err == nil {
		endpoint = svc.Spec.ClusterIP
	} else {
		return nil, fmt.Errorf("Failed to get k8s service spec: %v", err)
	}

	if len(endpoint) == 0 {
		return nil, fmt.Errorf("Failed to get endpoint for portworx volume driver")
	}

	clnt, err := volumeclient.NewDriverClient("http://"+endpoint+":9001", "pxd", "", "stork")
	if err != nil {
		return nil, err
	}
	return volumeclient.VolumeDriver(clnt), nil
}

// Plugin for managing Portworx snapshots
type Plugin struct {
	Log    logrus.FieldLogger
	plugin cloudprovider.BlockStore
}

// Init the plugin
func (p *Plugin) Init(config map[string]string) error {
	p.Log.Infof("Init'ing portworx plugin with config %v", config)
	if snapType, ok := config[configType]; !ok || snapType == typeLocal {
		p.plugin = &localSnapshotPlugin{log: p.Log}
	} else if snapType == typeCloud {
		p.plugin = &cloudSnapshotPlugin{log: p.Log}
	} else {
		err := fmt.Errorf("Snapshot type %v not supported", snapType)
		p.Log.Errorf("%v", err)
		return err
	}

	return p.plugin.Init(config)
}

// CreateVolumeFromSnapshot Create a volume form given snapshot
func (p *Plugin) CreateVolumeFromSnapshot(snapshotID, volumeType, volumeAZ string, iops *int64) (string, error) {
	return p.plugin.CreateVolumeFromSnapshot(snapshotID, volumeType, volumeAZ, iops)
}

// GetVolumeInfo Get information about the volume
func (p *Plugin) GetVolumeInfo(volumeID, volumeAZ string) (string, *int64, error) {
	return p.plugin.GetVolumeInfo(volumeID, volumeAZ)
}

// CreateSnapshot Create a snapshot
func (p *Plugin) CreateSnapshot(volumeID, volumeAZ string, tags map[string]string) (string, error) {
	return p.plugin.CreateSnapshot(volumeID, volumeAZ, tags)
}

// DeleteSnapshot Delete a snapshot
func (p *Plugin) DeleteSnapshot(snapshotID string) error {
	return p.plugin.DeleteSnapshot(snapshotID)
}

// GetVolumeID Get the volume ID from the spec
func (p *Plugin) GetVolumeID(pv runtime.Unstructured) (string, error) {
	if !collections.Exists(pv.UnstructuredContent(), "spec.portworxVolume") {
		return "", nil
	}

	return collections.GetString(pv.UnstructuredContent(), "spec.portworxVolume.volumeID")
}

// SetVolumeID Set the volume ID in the spec
func (p *Plugin) SetVolumeID(pv runtime.Unstructured, volumeID string) (runtime.Unstructured, error) {
	pwx, err := collections.GetMap(pv.UnstructuredContent(), "spec.portworxVolume")
	if err != nil {
		return nil, err
	}

	pwx["volumeID"] = volumeID

	return pv, nil
}
