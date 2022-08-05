package snapshot

import (
	"fmt"

	"github.com/libopenstorage/openstorage/api"
	"github.com/libopenstorage/openstorage/volume"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/uuid"
)

const (
	configCred = "credId"
)

type cloudSnapshotPlugin struct {
	Plugin
	log    logrus.FieldLogger
	credID string
}

func (c *cloudSnapshotPlugin) Init(config map[string]string) error {
	c.credID = config[configCred]

	c.log.Infof("Init'ing portworx cloud snapshot with credID %v", c.credID)
	return nil
}

func (c *cloudSnapshotPlugin) CreateVolumeFromSnapshot(snapshotID, volumeType, volumeAZ string, iops *int64) (string, error) {
	volDriver, err := getVolumeDriver()
	if err != nil {
		return "", err
	}

	// Enumerating can be expensive but we need to do it to get the original
	// volume name. Velero already has it so it can pass it down to us.
	// CloudBackupRestore can also be updated to restore to the original volume
	// name.
	enumRequest := &api.CloudBackupEnumerateRequest{
		CloudBackupGenericRequest: api.CloudBackupGenericRequest{
			CredentialUUID: c.credID,
			CloudBackupID:  snapshotID,
		},
	}

	enumResponse, err := volDriver.CloudBackupEnumerate(enumRequest)
	if err != nil {
		return "", err
	}

	srcVolumeName := ""
	for _, backup := range enumResponse.Backups {
		if backup.ID == snapshotID {
			srcVolumeName = backup.SrcVolumeName
			break
		}
	}

	if srcVolumeName == "" {
		msg := fmt.Sprintf("could not find backup associated with ID: %v", snapshotID)
		c.log.Infof(msg)
		return "", fmt.Errorf("%v", msg)
	}

	// Create a new name for restore PV
	restorePVName := "pvc-" + string(uuid.NewUUID())

	response, err := volDriver.CloudBackupRestore(&api.CloudBackupRestoreRequest{
		ID:                snapshotID,
		CredentialUUID:    c.credID,
		RestoreVolumeName: restorePVName,
	})
	if err != nil {
		c.log.Infof("Error starting cloudsnap restore from snapshot %v (source volume %v) to %v", snapshotID, srcVolumeName, restorePVName)
		return "", err
	}

	c.log.Infof("Started cloud snapshot restore %v to volume %v", snapshotID, restorePVName)
	err = volume.CloudBackupWaitForCompletion(volDriver, response.Name,
		api.CloudRestoreOp)
	if err != nil {
		c.log.Errorf("Error restoring %v to volume %v: %v", snapshotID, restorePVName, err)
		return "", err
	}

	c.log.Infof("Finished cloud snapshot restore %v for %v to volume %v", response.Name, snapshotID, restorePVName)
	return restorePVName, nil
}

func (c *cloudSnapshotPlugin) GetVolumeInfo(volumeID, volumeAZ string) (string, *int64, error) {
	return "portworx-cloudsnapshot", nil, nil
}

func (c *cloudSnapshotPlugin) CreateSnapshot(volumeID, volumeAZ string, tags map[string]string) (string, error) {
	volDriver, err := getVolumeDriver()
	if err != nil {
		return "", err
	}

	createResp, err := volDriver.CloudBackupCreate(&api.CloudBackupCreateRequest{
		VolumeID:       volumeID,
		Full:           true,
		CredentialUUID: c.credID,
	})
	if err != nil {
		return "", err
	}

	c.log.Infof("Started cloud snapshot backup %v for %v", createResp.Name, volumeID)
	err = volume.CloudBackupWaitForCompletion(volDriver, createResp.Name, api.CloudBackupOp)
	if err != nil {
		c.log.Errorf("Error backing up volume %v: %v", volumeID, err)
		return "", err
	}
	statusResponse, err := volDriver.CloudBackupStatus(&api.CloudBackupStatusRequest{
		ID: createResp.Name,
	})
	if err != nil {
		return "", err
	}
	c.log.Infof("Finished cloud snapshot backup %v for %v to %v", createResp.Name, volumeID, statusResponse.Statuses[createResp.Name].ID)
	return statusResponse.Statuses[createResp.Name].ID, nil
}

func (c *cloudSnapshotPlugin) DeleteSnapshot(snapshotID string) error {
	volDriver, err := getVolumeDriver()
	if err != nil {
		return err
	}

	return volDriver.CloudBackupDelete(&api.CloudBackupDeleteRequest{
		ID:             snapshotID,
		CredentialUUID: c.credID,
		Force:          false,
	})
}
