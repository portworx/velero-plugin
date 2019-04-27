package snapshot

import (
	"fmt"

	"github.com/heptio/velero/pkg/plugin/velero"
	volumeclient "github.com/libopenstorage/openstorage/api/client/volume"
	"github.com/libopenstorage/openstorage/volume"
	"github.com/pkg/errors"
	"github.com/portworx/sched-ops/k8s"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	plugin velero.VolumeSnapshotter
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
func (p *Plugin) GetVolumeID(unstructuredPV runtime.Unstructured) (string, error) {
	pv := new(v1.PersistentVolume)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredPV.UnstructuredContent(), pv); err != nil {
		return "", errors.WithStack(err)
	}

	if pv.Spec.PortworxVolume == nil {
		return "", nil
	}

	if pv.Spec.PortworxVolume.VolumeID == "" {
		return "", errors.New("spec.portworxVolume.volumeID")
	}

	return pv.Spec.PortworxVolume.VolumeID, nil
}

// SetVolumeID Set the volume ID in the spec
func (p *Plugin) SetVolumeID(unstructuredPV runtime.Unstructured, volumeID string) (runtime.Unstructured, error) {
	pv := new(v1.PersistentVolume)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredPV.UnstructuredContent(), pv); err != nil {
		return nil, errors.WithStack(err)
	}

	if pv.Spec.PortworxVolume == nil {
		return nil, errors.New("spec.portworxVolume not found")
	}

	pv.Spec.PortworxVolume.VolumeID = volumeID

	res, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pv)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &unstructured.Unstructured{Object: res}, nil
}
