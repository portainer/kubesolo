package localpath

import (
	"context"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func createStorageClass(ctx context.Context, clientset *kubernetes.Clientset) error {
	waitForFirstConsumer := storagev1.VolumeBindingWaitForFirstConsumer
	deletePolicy := v1.PersistentVolumeReclaimRetain

	storageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-path",
			Annotations: map[string]string{
				"storageclass.kubernetes.io/is-default-class": "true",
			},
		},
		Provisioner:       "rancher.io/local-path",
		VolumeBindingMode: &waitForFirstConsumer,
		ReclaimPolicy:     &deletePolicy,
	}

	_, err := clientset.StorageV1().StorageClasses().Create(ctx, storageClass, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
