package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/portainer/kubesolo/pkg/assistant"
)

// store is a containerd content store on disk, as KubeSolo's containerd keeps it.
func store(t *testing.T) (content.Store, string) {
	t.Helper()
	dir := t.TempDir()
	cs, err := local.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cs, dir
}

func put(t *testing.T, cs content.Store, mediaType string, b []byte) ocispec.Descriptor {
	t.Helper()
	desc := ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(b), Size: int64(len(b))}
	if err := content.WriteBlob(context.Background(), cs, desc.Digest.String(), bytes.NewReader(b), desc); err != nil {
		t.Fatal(err)
	}
	return desc
}

// manifest stores an artifact manifest over the given layers, as oras push
// writes it.
func manifest(t *testing.T, cs content.Store, layers ...[]byte) ocispec.Descriptor {
	t.Helper()
	m := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    put(t, cs, ocispec.MediaTypeEmptyJSON, []byte("{}")),
	}
	m.SchemaVersion = 2
	for _, l := range layers {
		m.Layers = append(m.Layers, put(t, cs, "application/vnd.docker.ai.gguf.v3", l))
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return put(t, cs, ocispec.MediaTypeImageManifest, raw)
}

var gguf = []byte("GGUF\x03\x00\x00\x00rest of the model")

// The model must be the layer itself, so llama-server can read it where
// containerd stored it, without a second copy.
func TestModelFileIsTheStoredLayer(t *testing.T) {
	cs, dir := store(t)
	target := manifest(t, cs, gguf)

	path, err := modelFile(context.Background(), cs, target, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "blobs", "sha256", digest.FromBytes(gguf).Encoded())
	if path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
}

func TestModelArtifactsThatCannotBeReadInPlace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layers [][]byte
		want   string
	}{
		{"no layers", nil, "one layer"},
		{"two layers", [][]byte{gguf, gguf}, "one layer"},
		// What a Dockerfile COPY produces: the model inside a tar archive.
		{"tar layer", [][]byte{[]byte("model.gguf\x00\x00\x00 tar header")}, "not a GGUF file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, dir := store(t)
			target := manifest(t, cs, tc.layers...)

			_, err := modelFile(context.Background(), cs, target, dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected an error mentioning %q, got %v", tc.want, err)
			}
			// Fetching again cannot fix the format, so it is not retried.
			if !errors.Is(err, ErrNotAModel) {
				t.Errorf("error should be ErrNotAModel: %v", err)
			}
		})
	}
}

func TestModelIndexIsRejected(t *testing.T) {
	cs, dir := store(t)
	raw, _ := json.Marshal(ocispec.Index{MediaType: ocispec.MediaTypeImageIndex})
	target := put(t, cs, ocispec.MediaTypeImageIndex, raw)

	if _, err := modelFile(context.Background(), cs, target, dir); !errors.Is(err, ErrNotAModel) || !strings.Contains(err.Error(), "multi-platform") {
		t.Errorf("expected ErrNotAModel about a multi-platform image, got %v", err)
	}
}

func TestKnowledgeFileIsTheStoredLayer(t *testing.T) {
	cs, dir := store(t)
	knowledge := []byte("## kine\nA locked database means another process holds it.")
	target := manifest(t, cs, knowledge)

	path, err := knowledgeFile(context.Background(), cs, target, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "blobs", "sha256", digest.FromBytes(knowledge).Encoded()); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
}

func TestKnowledgeArtifactsThatCannotBeUsed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layers [][]byte
		want   string
	}{
		{"two layers", [][]byte{[]byte("a"), []byte("b")}, "one layer"},
		{"too large", [][]byte{bytes.Repeat([]byte("x"), assistant.MaxKnowledgeBytes+1)}, "at most"},
		{"a model", [][]byte{gguf}, "not text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, dir := store(t)
			_, err := knowledgeFile(context.Background(), cs, manifest(t, cs, tc.layers...), dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.Is(err, ErrNotKnowledge) {
				t.Errorf("expected ErrNotKnowledge mentioning %q, got %v", tc.want, err)
			}
		})
	}
}
