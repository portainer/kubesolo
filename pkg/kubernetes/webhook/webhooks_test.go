package webhook

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const testNodeName = "argos"

// mutate runs raw through the webhook's mutation for kind and returns the
// object with the resulting JSON Patch applied, as the API server would.
func mutate(t *testing.T, kind string, raw string) []byte {
	t.Helper()

	w := NewService(testNodeName, "", "", "", false)
	review := &admissionv1.AdmissionReview{Request: &admissionv1.AdmissionRequest{Object: runtime.RawExtension{Raw: []byte(raw)}}}
	review.Request.Kind.Kind = kind

	patches, err := w.processResource(review)
	require.NoError(t, err)
	if len(patches) == 0 {
		return []byte(raw)
	}

	patchBytes, err := json.Marshal(patches)
	require.NoError(t, err)
	patch, err := jsonpatch.DecodePatch(patchBytes)
	require.NoError(t, err)
	out, err := patch.Apply([]byte(raw))
	require.NoError(t, err)
	return out
}

func TestPVCMutationKeepsAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations string
		want        map[string]string
	}{
		{
			name: "no annotations",
			want: map[string]string{"volume.kubernetes.io/selected-node": testNodeName},
		},
		{
			name:        "empty annotations",
			annotations: `,"annotations":{}`,
			want:        map[string]string{"volume.kubernetes.io/selected-node": testNodeName},
		},
		{
			name:        "user annotations",
			annotations: `,"annotations":{"example.com/test":"x","cnpg.io/nodeSerial":"1"}`,
			want: map[string]string{
				"example.com/test":                   "x",
				"cnpg.io/nodeSerial":                 "1",
				"volume.kubernetes.io/selected-node": testNodeName,
			},
		},
		{
			name:        "node already selected",
			annotations: `,"annotations":{"example.com/test":"x","volume.kubernetes.io/selected-node":"other"}`,
			want: map[string]string{
				"example.com/test":                   "x",
				"volume.kubernetes.io/selected-node": "other",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"anno-probe","namespace":"default"` + tt.annotations + `}}`

			var got struct {
				Metadata struct {
					Annotations map[string]string `json:"annotations"`
				} `json:"metadata"`
			}
			require.NoError(t, json.Unmarshal(mutate(t, "PersistentVolumeClaim", raw), &got))
			require.Equal(t, tt.want, got.Metadata.Annotations)
		})
	}
}
