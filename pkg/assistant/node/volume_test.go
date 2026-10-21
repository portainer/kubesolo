package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/pkg/assistant"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// volume makes a bound claim whose volume is a directory on the host, as the
// local-path provisioner's are, holding the given files.
func volume(t *testing.T, phase corev1.PersistentVolumeClaimPhase, files map[string][]byte) (*fake.Clientset, string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "models", Namespace: "ai"},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeName: "pvc-123"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: phase},
	}
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-123"},
		Spec: corev1.PersistentVolumeSpec{PersistentVolumeSource: corev1.PersistentVolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: dir}}},
	}
	return fake.NewSimpleClientset(pvc, pv), dir
}

func source(t *testing.T, s string) assistant.VolumeSource {
	t.Helper()
	v, err := assistant.ParseVolumeSource(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAModelInAVolumeIsReadInPlace(t *testing.T) {
	cs, dir := volume(t, corev1.ClaimBound, map[string][]byte{"qwen/model.gguf": gguf})

	path, err := ModelInVolume(context.Background(), cs, source(t, "pvc:ai/models/qwen/model.gguf"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "qwen/model.gguf"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
}

func TestVolumeFilesThatCannotBeUsed(t *testing.T) {
	escape := t.TempDir()
	if err := os.WriteFile(filepath.Join(escape, "secret.gguf"), gguf, 0o444); err != nil {
		t.Fatal(err)
	}
	cs, dir := volume(t, corev1.ClaimBound, map[string][]byte{"notes.txt": []byte("not a model")})
	if err := os.Symlink(filepath.Join(escape, "secret.gguf"), filepath.Join(dir, "link.gguf")); err != nil {
		t.Fatal(err)
	}
	pending, _ := volume(t, corev1.ClaimPending, map[string][]byte{"model.gguf": gguf})

	for _, tc := range []struct {
		name      string
		cs        *fake.Clientset
		src       string
		want      string
		notUsable bool // fetching again will not help
	}{
		{"not bound yet", pending, "pvc:ai/models/model.gguf", "not bound", false},
		{"no such claim", cs, "pvc:ai/other/model.gguf", "not found", false},
		{"no such file", cs, "pvc:ai/models/missing.gguf", "no such file", false},
		{"not a model", cs, "pvc:ai/models/notes.txt", "not a GGUF file", true},
		{"a link out of the volume", cs, "pvc:ai/models/link.gguf", "outside the volume", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ModelInVolume(context.Background(), tc.cs, source(t, tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error mentioning %q", err, tc.want)
			}
			if errors.Is(err, ErrNotAModel) != tc.notUsable {
				t.Errorf("ErrNotAModel = %v, want %v", errors.Is(err, ErrNotAModel), tc.notUsable)
			}
		})
	}
}

func TestKnowledgeInAVolume(t *testing.T) {
	cs, _ := volume(t, corev1.ClaimBound, map[string][]byte{"k.md": []byte("## kine\nsigns")})
	if _, err := KnowledgeInVolume(context.Background(), cs, source(t, "pvc:ai/models/k.md")); err != nil {
		t.Errorf("got %v", err)
	}
}
