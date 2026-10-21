package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/portainer/kubesolo/pkg/assistant"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ModelInVolume returns the host path of a model kept in a
// PersistentVolumeClaim, which llama-server reads in place. A file that is
// not a model, or that leads out of the volume, is ErrNotAModel: fetching
// again will not help. A claim not yet bound, or a file not yet there, may be
// later.
func ModelInVolume(ctx context.Context, cs kubernetes.Interface, src assistant.VolumeSource) (string, error) {
	return fileInVolume(ctx, cs, src, ErrNotAModel, func(f *os.File, _ int64) error {
		if !isGGUF(f) {
			return errors.New("is not a GGUF file")
		}
		return nil
	})
}

// KnowledgeInVolume is ModelInVolume for the knowledge.
func KnowledgeInVolume(ctx context.Context, cs kubernetes.Interface, src assistant.VolumeSource) (string, error) {
	return fileInVolume(ctx, cs, src, ErrNotKnowledge, func(f *os.File, size int64) error { return checkKnowledge(f, size) })
}

func fileInVolume(ctx context.Context, cs kubernetes.Interface, src assistant.VolumeSource, notUsable error, check func(*os.File, int64) error) (string, error) {
	pvc, err := cs.CoreV1().PersistentVolumeClaims(src.Namespace).Get(ctx, src.Claim, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if pvc.Status.Phase != corev1.ClaimBound {
		return "", fmt.Errorf("claim %s/%s is not bound yet", src.Namespace, src.Claim)
	}
	pv, err := cs.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	var root string
	switch {
	case pv.Spec.HostPath != nil:
		root = pv.Spec.HostPath.Path
	case pv.Spec.Local != nil:
		root = pv.Spec.Local.Path
	default:
		return "", fmt.Errorf("%w: volume %s is not a directory on this host", notUsable, pv.Name)
	}

	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, src.Path))
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(root, path); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%w: %s leads outside the volume", notUsable, src.Path)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if err := check(f, info.Size()); err != nil {
		return "", fmt.Errorf("%w: %s %w", notUsable, src.Path, err)
	}
	return path, nil
}
