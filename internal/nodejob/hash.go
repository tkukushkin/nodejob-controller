package nodejob

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	batchv1 "k8s.io/api/batch/v1"
)

const hashLen = 16

// TemplateHash is stable across controller restarts: encoding/json emits struct fields in
// declaration order and map keys sorted.
//
// Node selection — including tolerations, which decide which taints a node may carry — is
// stripped before hashing: it says where the task runs, not what it does, so retargeting
// must not re-run it on nodes that stayed in the target set.
func TemplateHash(tpl batchv1.JobTemplateSpec) (string, error) {
	hashable := tpl.DeepCopy()
	hashable.Spec.Template.Spec.NodeSelector = nil
	hashable.Spec.Template.Spec.Affinity = nil
	hashable.Spec.Template.Spec.Tolerations = nil

	data, err := json.Marshal(hashable)
	if err != nil {
		return "", err
	}
	return sum(data), nil
}

func RunHash(templateHash, bootID string) string {
	if bootID == "" {
		return templateHash
	}
	return sum([]byte(templateHash + "/" + bootID))
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])[:hashLen]
}
