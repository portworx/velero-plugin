package main

import (
	veleroplugin "github.com/heptio/velero/pkg/plugin/framework"
	"github.com/portworx/velero-plugin/pkg/snapshot"
	"github.com/sirupsen/logrus"
)

func main() {
	veleroplugin.NewServer().
		RegisterVolumeSnapshotter("portworx.io/portworx", newSnapshotPlugin).
		Serve()
}

func newSnapshotPlugin(logger logrus.FieldLogger) (interface{}, error) {
	return &snapshot.Plugin{Log: logger}, nil
}
