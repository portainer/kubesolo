package webhook

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

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
	wg                 sync.WaitGroup
	server             *http.Server
	nodeName           string
	nodeIP             string
	pkiPath            string
	clientset          *kubernetes.Clientset
	hostsEntries       map[string]string
	nodeNamePatch      []byte
	nodeSelectorPatch  []byte
	pvcAnnotationPatch []map[string]any
	requestMutex       sync.Mutex
	lastRequest        time.Time
	adminKubeconfig    string
	loadBalancer       bool
}

// NewService creates a new webhook server
func NewService(nodeName, nodeIP, pkiPath, adminKubeconfig string, loadBalancer bool) *Service {
	nodeNamePatch, _ := json.Marshal([]map[string]any{
		{
			"op":    "add",
			"path":  "/spec/nodeName",
			"value": nodeName,
		},
	})

	nodeSelectorPatch, _ := json.Marshal([]map[string]any{
		{
			"op":   "add",
			"path": "/spec/template/spec/nodeSelector",
			"value": map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
		},
	})

	pvcAnnotationPatch := []map[string]any{
		{
			"op":   "add",
			"path": "/metadata/annotations",
			"value": map[string]string{
				"volume.kubernetes.io/selected-node": nodeName,
			},
		},
	}

	return &Service{
		nodeName:           nodeName,
		nodeIP:             nodeIP,
		pkiPath:            pkiPath,
		adminKubeconfig:    adminKubeconfig,
		hostsEntries:       make(map[string]string),
		nodeNamePatch:      nodeNamePatch,
		nodeSelectorPatch:  nodeSelectorPatch,
		pvcAnnotationPatch: pvcAnnotationPatch,
		loadBalancer:       loadBalancer,
	}
}
