package webhook

import (
	"encoding/json"
	"net/http"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

// Service is a webhook that handles pod mutations for KubeSolo
type Service struct {
	wg                      sync.WaitGroup
	server                  *http.Server
	nodeName                string
	loadBalancerIP          string
	pkiPath                 string
	clientsetMu             sync.Mutex
	clientset               *kubernetes.Clientset
	hostsEntries            map[string]string
	nodeNamePatch           []byte
	nodeSelectorPatch       []byte
	nodeNamePatchObj        []map[string]any
	nodeSelectorPatchObj    []map[string]any
	pvcAnnotationPatch      []map[string]any
	loadBalancerStatusPatch []byte
	adminKubeconfig         string
	loadBalancer            bool
	loadBalancerUpdateLocks sync.Map
}

// NewService creates a new webhook server
func NewService(nodeName, loadBalancerIP, pkiPath, adminKubeconfig string, loadBalancer bool) *Service {
	// define struct form first, marshal from it — no round trip
	nodeNamePatchObj := []map[string]any{
		{
			"op":    "add",
			"path":  "/spec/nodeName",
			"value": nodeName,
		},
	}

	nodeSelectorPatchObj := []map[string]any{
		{
			"op":   "add",
			"path": "/spec/template/spec/nodeSelector",
			"value": map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
		},
	}

	nodeNamePatch, _ := json.Marshal(nodeNamePatchObj)
	nodeSelectorPatch, _ := json.Marshal(nodeSelectorPatchObj)

	pvcAnnotationPatch := []map[string]any{
		{
			"op":   "add",
			"path": "/metadata/annotations",
			"value": map[string]string{
				"volume.kubernetes.io/selected-node": nodeName,
			},
		},
	}

	loadBalancerStatusPatch, _ := json.Marshal(map[string]interface{}{
		"status": map[string]interface{}{
			"loadBalancer": map[string]interface{}{
				"ingress": []map[string]interface{}{
					{
						"ip": loadBalancerIP,
					},
				},
			},
		},
	})

	return &Service{
		nodeName:                nodeName,
		loadBalancerIP:          loadBalancerIP,
		pkiPath:                 pkiPath,
		adminKubeconfig:         adminKubeconfig,
		hostsEntries:            make(map[string]string),
		nodeNamePatch:           nodeNamePatch,
		nodeSelectorPatch:       nodeSelectorPatch,
		nodeNamePatchObj:        nodeNamePatchObj,
		nodeSelectorPatchObj:    nodeSelectorPatchObj,
		pvcAnnotationPatch:      pvcAnnotationPatch,
		loadBalancerStatusPatch: loadBalancerStatusPatch,
		loadBalancer:            loadBalancer,
	}
}

// getClientset returns the Kubernetes clientset, or nil if RegisterWebhook has not completed.
func (w *Service) getClientset() *kubernetes.Clientset {
	w.clientsetMu.Lock()
	defer w.clientsetMu.Unlock()
	return w.clientset
}
