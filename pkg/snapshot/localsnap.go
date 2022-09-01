package snapshot

import (
	"fmt"
	"golang.org/x/net/context"
	"strings"

	"github.com/libopenstorage/openstorage/api"
	"github.com/sirupsen/logrus"
)

const (
	pxdDriverName = "pxd.portworx.com"
)

type localSnapshotPlugin struct {
	Plugin
	pxClient *portworxClient
	log logrus.FieldLogger
}

func (l *localSnapshotPlugin) Init(config map[string]string) error {
	l.log.Infof("Init'ing portworx local snapshot")
	return nil
}

func (l *localSnapshotPlugin) CreateVolumeFromSnapshot(snapshotID, volumeType, volumeAZ string, iops *int64) (string, error) {
	volDriver, err := l.pxClient.getVolumeDriver()
	if err != nil {
		return "", err
	}
	vols, err := volDriver.Inspect([]string{snapshotID})
	if err != nil {
		return "", nil
	}
	if len(vols) == 0 {
		return "", fmt.Errorf("Snapshot %v not found", snapshotID)
	}

	locator := &api.VolumeLocator{
		Name: vols[0].Locator.VolumeLabels["pvName"],
	}
	volumeID, err := volDriver.Snapshot(snapshotID, false, locator, true)
	if err != nil {
		return "", err
	}
	return volumeID, err
}

func (l *localSnapshotPlugin) GetVolumeInfo(volumeID, volumeAZ string) (string, *int64, error) {
	return "portworx-snapshot", nil, nil
}

func (l *localSnapshotPlugin) CreateSnapshot(volumeID, volumeAZ string, tags map[string]string) (string, error) {
	volDriver, err := l.pxClient.getVolumeDriver()
	if err != nil {
		return "", err
	}

	vols, err := volDriver.Inspect([]string{volumeID})
	if err != nil {
		return "", err
	}
	if len(vols) == 0 {
		return "", fmt.Errorf("Volume %v not found", volumeID)
	}

	tags["pvName"] = vols[0].Locator.Name
	l.log.Infof("Tags: %v", tags)
	locator := &api.VolumeLocator{
		Name:         strings.TrimSpace(tags["velero.io/backup"]) + "_" + vols[0].Locator.Name,
		VolumeLabels: tags,
	}
	snapshotID, err := volDriver.Snapshot(volumeID, true, locator, true)
	if err != nil {
		return "", err
	}

	return snapshotID, err
}

func (l *localSnapshotPlugin) DeleteSnapshot(snapshotID string) error {
	volDriver, err := l.pxClient.getVolumeDriver()
	if err != nil {
		return err
	}

	return volDriver.Delete(context.Background(), snapshotID)
}
