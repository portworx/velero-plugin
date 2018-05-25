package main

import (
	"github.com/heptio/ark/pkg/plugin"
	"github.com/portworx/ark-plugin/pkg/snapshot"
)

func main() {
	portworxPlugin := &snapshot.Plugin{Log: plugin.NewLogger()}
	plugin.Serve(plugin.NewBlockStorePlugin(portworxPlugin))
}
