package snapshot

import (
	"fmt"

	"github.com/libopenstorage/openstorage/api"
	"github.com/libopenstorage/openstorage/volume"
	"github.com/sirupsen/logrus"
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
	// volume name. Ark already has it so it can pass it down to us.
	// CloudBackupRestore can also be updated to restore to the original volume
	// name.
	enumRequest := &api.CloudBackupEnumerateRequest{}
	enumRequest.CredentialUUID = c.credID
	enumResponse, err := volDriver.CloudBackupEnumerate(enumRequest)
	if err != nil {
		return "", err
	}

	volumeName := ""
	for _, backup := range enumResponse.Backups {
		if backup.ID == snapshotID {
			volumeName = backup.SrcVolumeName
			break
		}
	}

	if volumeName == "" {
		return "", fmt.Errorf("Couldn't find volume name from cloudsnap")
	}

	response, err := volDriver.CloudBackupRestore(&api.CloudBackupRestoreRequest{
		ID:                snapshotID,
		CredentialUUID:    c.credID,
		RestoreVolumeName: volumeName,
	})
	if err != nil {
		return "", err
	}

	c.log.Infof("Started cloud snapshot restore %v to volume %v", snapshotID, response.RestoreVolumeID)
	err = volume.CloudBackupWaitForCompletion(volDriver, response.RestoreVolumeID,
		api.CloudRestoreOp)
	if err != nil {
		c.log.Errorf("Error restoring %v to volume %v: %v", snapshotID, response.RestoreVolumeID, err)
		return "", err
	}

	c.log.Infof("Finished cloud snapshot restore %v to volume %v", snapshotID, response.RestoreVolumeID)
	return response.RestoreVolumeID, nil
}

func (c *cloudSnapshotPlugin) GetVolumeInfo(volumeID, volumeAZ string) (string, *int64, error) {
	return "portworx-cloudsnapshot", nil, nil
}

func (c *cloudSnapshotPlugin) IsVolumeReady(volumeID, volumeAZ string) (ready bool, err error) {
	volDriver, err := getVolumeDriver()
	if err != nil {
		return false, err
	}

	vols, err := volDriver.Inspect([]string{volumeID})
	if err != nil {
		return false, err
	}
	if len(vols) == 0 {
		return false, fmt.Errorf("Volume %v not found", volumeID)
	}
	return true, nil
}

func (c *cloudSnapshotPlugin) CreateSnapshot(volumeID, volumeAZ string, tags map[string]string) (string, error) {
	volDriver, err := getVolumeDriver()
	if err != nil {
		return "", err
	}

	err = volDriver.CloudBackupCreate(&api.CloudBackupCreateRequest{
		VolumeID:       volumeID,
		Full:           true,
		CredentialUUID: c.credID,
	})
	if err != nil {
		return "", err
	}

	response, err := volDriver.CloudBackupStatus(&api.CloudBackupStatusRequest{
		SrcVolumeID: volumeID,
	})
	if err != nil {
		return "", err
	}
	c.log.Infof("Started cloud snapshot backup %v for %v", response.Statuses[volumeID].ID, volumeID)
	err = volume.CloudBackupWaitForCompletion(volDriver, volumeID, api.CloudBackupOp)
	if err != nil {
		c.log.Errorf("Error backing up volume %v: %v", volumeID, err)
		return "", err
	}
	c.log.Infof("Finished cloud snapshot backup %v for %v", response.Statuses[volumeID].ID, volumeID)
	return response.Statuses[volumeID].ID, nil
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
