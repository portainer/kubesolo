package localpath

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCreateStorageClass(t *testing.T) {
	cs := fake.NewSimpleClientset()
	require.NoError(t, createStorageClass(context.Background(), cs))

	sc, err := cs.StorageV1().StorageClasses().Get(context.Background(), "local-path", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, "true", sc.Annotations["storageclass.kubernetes.io/is-default-class"],
		"local-path must be the default StorageClass")
	assert.Equal(t, "rancher.io/local-path", sc.Provisioner)
	require.NotNil(t, sc.VolumeBindingMode)
	assert.Equal(t, storagev1.VolumeBindingWaitForFirstConsumer, *sc.VolumeBindingMode)
}

func TestCreateStorageClass_Idempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx := context.Background()
	require.NoError(t, createStorageClass(ctx, cs))
	// AlreadyExists must be swallowed.
	require.NoError(t, createStorageClass(ctx, cs))
}
