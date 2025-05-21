package localpath

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func createConfigMap(ctx context.Context, clientset *kubernetes.Clientset) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-path-config",
			Namespace: LocalPathNamespace,
		},
		Data: map[string]string{
			"config.json": `{
    "nodePathMap":[
    {
        "node":"DEFAULT_PATH_FOR_NON_LISTED_NODES",
        "paths":["/opt/local-path-provisioner"]
    }
    ]
}`,
			"setup": `#!/bin/sh
set -eu
mkdir -m 0777 -p "$VOL_DIR"`,
			"teardown": `#!/bin/sh
set -eu
rm -rf "$VOL_DIR"`,
			"helperPod.yaml": `apiVersion: v1
kind: Pod
metadata:
  name: helper-pod
spec:
  priorityClassName: system-node-critical
  tolerations:
    - key: node.kubernetes.io/disk-pressure
      operator: Exists
      effect: NoSchedule
  containers:
  - name: helper-pod
    image: busybox
    imagePullPolicy: IfNotPresent`,
		},
	}

	_, err := clientset.CoreV1().ConfigMaps(LocalPathNamespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	if errors.IsAlreadyExists(err) {
		_, err = clientset.CoreV1().ConfigMaps(LocalPathNamespace).Update(ctx, configMap, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}
