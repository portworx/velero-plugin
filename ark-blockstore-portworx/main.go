package main

import (
	"github.com/heptio/ark/pkg/plugin"
	"github.com/portworx/ark-plugin/pkg/snapshot"
	"github.com/sirupsen/logrus"
)

func main() {
	plugin.NewServer(plugin.NewLogger()).
		RegisterBlockStore("portworx", newSnapshotPlugin).
		Serve()
}

func newSnapshotPlugin(logger logrus.FieldLogger) (interface{}, error) {
	return &snapshot.Plugin{Log: logger}, nil
}
