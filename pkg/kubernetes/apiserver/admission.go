package apiserver

import (
	"io"

	"k8s.io/apiserver/pkg/admission"
)

const (
	pluginName = "KubeSoloNodeSetter"
)

type nodeSetter struct {
	*admission.Handler
	nodeName string
}

func newNodeSetter(nodeName string) *nodeSetter {
	return &nodeSetter{
		Handler:  admission.NewHandler(admission.Create),
		nodeName: nodeName,
	}
}

func register(plugins *admission.Plugins, nodeName string) {
	plugins.Register(pluginName, func(_ io.Reader) (admission.Interface, error) {
		return newNodeSetter(nodeName), nil
	})
}
