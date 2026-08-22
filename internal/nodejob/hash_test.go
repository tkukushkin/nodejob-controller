package nodejob

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func templateWithImage(image string) batchv1.JobTemplateSpec {
	return batchv1.JobTemplateSpec{
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: "fix", Image: image}},
				},
			},
		},
	}
}

func TestTemplateHashIsStable(t *testing.T) {
	first, err := TemplateHash(templateWithImage("busybox:1"))
	if err != nil {
		t.Fatalf("TemplateHash: %v", err)
	}
	second, err := TemplateHash(templateWithImage("busybox:1"))
	if err != nil {
		t.Fatalf("TemplateHash: %v", err)
	}
	if first != second {
		t.Errorf("hash not stable: %q != %q", first, second)
	}
	if len(first) != 16 {
		t.Errorf("hash length = %d, want 16", len(first))
	}
}

func TestTemplateHashChangesWithTemplate(t *testing.T) {
	a, _ := TemplateHash(templateWithImage("busybox:1"))
	b, _ := TemplateHash(templateWithImage("busybox:2"))
	if a == b {
		t.Errorf("different templates produced the same hash %q", a)
	}
}

func TestRunHashWithoutBootIDEqualsTemplateHash(t *testing.T) {
	if got := RunHash("abcdef0123456789", ""); got != "abcdef0123456789" {
		t.Errorf("RunHash = %q, want the template hash unchanged", got)
	}
}

func TestRunHashDependsOnBootID(t *testing.T) {
	a := RunHash("abcdef0123456789", "boot-1")
	b := RunHash("abcdef0123456789", "boot-2")
	if a == b {
		t.Errorf("different bootIDs produced the same hash %q", a)
	}
	if len(a) != 16 {
		t.Errorf("hash length = %d, want 16", len(a))
	}
}

func TestTemplateHashIgnoresNodeSelection(t *testing.T) {
	plain := templateWithImage("busybox:1")

	selected := templateWithImage("busybox:1")
	selected.Spec.Template.Spec.NodeSelector = map[string]string{"role": "worker"}
	selected.Spec.Template.Spec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      "zone",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"a"},
					}},
				}},
			},
		},
	}

	a, err := TemplateHash(plain)
	if err != nil {
		t.Fatalf("TemplateHash: %v", err)
	}
	b, err := TemplateHash(selected)
	if err != nil {
		t.Fatalf("TemplateHash: %v", err)
	}
	if a != b {
		t.Errorf("node selection changed the hash: %q != %q", a, b)
	}
}

func TestTemplateHashDoesNotMutateTemplate(t *testing.T) {
	tpl := templateWithImage("busybox:1")
	tpl.Spec.Template.Spec.NodeSelector = map[string]string{"role": "worker"}

	if _, err := TemplateHash(tpl); err != nil {
		t.Fatalf("TemplateHash: %v", err)
	}
	if tpl.Spec.Template.Spec.NodeSelector == nil {
		t.Error("TemplateHash stripped node selection from the caller's template")
	}
}

func TestTemplateHashIgnoresTolerations(t *testing.T) {
	plain := templateWithImage("busybox:1")

	tolerating := templateWithImage("busybox:1")
	tolerating.Spec.Template.Spec.Tolerations = []corev1.Toleration{{
		Key: "dedicated", Operator: corev1.TolerationOpExists,
	}}

	a, err := TemplateHash(plain)
	if err != nil {
		t.Fatalf("TemplateHash: %v", err)
	}
	b, err := TemplateHash(tolerating)
	if err != nil {
		t.Fatalf("TemplateHash: %v", err)
	}
	if a != b {
		t.Errorf("tolerations changed the hash: %q != %q", a, b)
	}
}
