package e2e_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorapi "github.com/openshift/api/operator/v1"

	imageregistryv1 "github.com/openshift/cluster-image-registry-operator/pkg/apis/imageregistry/v1"
	"github.com/openshift/cluster-image-registry-operator/pkg/testframework"
)

func TestFailing(t *testing.T) {
	client := testframework.MustNewClientset(t, nil)

	defer testframework.MustRemoveImageRegistry(t, client)

	testframework.MustDeployImageRegistry(t, client, &imageregistryv1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: imageregistryv1.ImageRegistryResourceName,
		},
		Spec: imageregistryv1.ImageRegistrySpec{
			ManagementState: operatorapi.Managed,
			Storage: imageregistryv1.ImageRegistryConfigStorage{
				Filesystem: &imageregistryv1.ImageRegistryConfigStorageFilesystem{
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			Replicas: -1,
		},
	})
	cr := testframework.MustEnsureImageRegistryIsProcessed(t, client)

	var failing operatorapi.OperatorCondition
	for _, cond := range cr.Status.Conditions {
		switch cond.Type {
		case operatorapi.OperatorStatusTypeFailing:
			failing = cond
		}
	}
	if failing.Status != operatorapi.ConditionTrue {
		testframework.DumpObject(t, "the latest observed image registry resource", cr)
		testframework.DumpOperatorLogs(t, client)
		t.Fatal("the imageregistry resource is expected to be failing")
	}

	if expected := "replicas must be greater than or equal to 0"; !strings.Contains(failing.Message, expected) {
		t.Errorf("expected failing message to contain %q, got %q", expected, failing.Message)
	}
}